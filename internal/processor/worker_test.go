package processor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

type memStore struct {
	mu   sync.Mutex
	jobs map[string]*jobs.Job
}

func newMemStore() *memStore {
	return &memStore{jobs: make(map[string]*jobs.Job)}
}

func (s *memStore) CreateJob(_ context.Context, job *jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *job
	s.jobs[job.ID] = &c
	return nil
}

func (s *memStore) UpdateStage(_ context.Context, id string, stage jobs.Stage, startedAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Stage = stage
		if startedAt != nil {
			st := *startedAt
			j.StartedAt = &st
		}
	}
	return nil
}

func (s *memStore) SaveResult(_ context.Context, id string, location, commit string, completedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Stage = jobs.StageCompleted
		loc := location
		com := commit
		j.TargetLocation = &loc
		j.TargetCommit = &com
		ct := completedAt
		j.CompletedAt = &ct
	}
	return nil
}

func (s *memStore) SaveError(_ context.Context, id string, errMsg string, completedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Stage = jobs.StageFailed
		em := errMsg
		j.ErrorMessage = &em
		ct := completedAt
		j.CompletedAt = &ct
	}
	return nil
}

func (s *memStore) GetJob(_ context.Context, id string) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		c := *j
		return &c, nil
	}
	return nil, nil
}

func (s *memStore) Close() error { return nil }

type llmMock struct {
	out           string
	err           error
	suggestedSlug string
	slugErr       error
	slugCalled    bool
}

type llmConv struct{}

func (llmConv) Provider() string { return "mock" }

func (m *llmMock) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, llm.Conversation, error) {
	if m.err != nil {
		return "", nil, m.err
	}
	_, _ = io.Copy(io.Discard, r)
	return m.out, llmConv{}, nil
}

func (m *llmMock) SuggestFilename(_ context.Context, _ llm.Conversation) (string, error) {
	m.slugCalled = true
	return m.suggestedSlug, m.slugErr
}

type capturingTarget struct {
	name    string
	lastReq targets.TargetRequest
}

func (t *capturingTarget) Name() string { return t.name }
func (t *capturingTarget) Post(_ context.Context, req targets.TargetRequest) (targets.TargetResult, error) {
	t.lastReq = req
	return targets.TargetResult{TargetName: t.name}, nil
}

type targetMock struct {
	name string
	res  targets.TargetResult
	err  error
}

func (t *targetMock) Name() string { return t.name }
func (t *targetMock) Post(ctx context.Context, req targets.TargetRequest) (targets.TargetResult, error) {
	if t.err != nil {
		return targets.TargetResult{}, t.err
	}
	return t.res, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestWorker_Process_SuccessWithCallback(t *testing.T) {
	// Callback collector
	var cbMu sync.Mutex
	var cbBodies []map[string]any
	cbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cbMu.Lock()
		cbBodies = append(cbBodies, body)
		cbMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer cbSrv.Close()

	// Setup store and initial job record
	store := newMemStore()

	// LLM and target mocks
	llmClient := &llmMock{out: "markdown"}
	tgt := &targetMock{
		name: "github",
		res: targets.TargetResult{
			TargetName: "github",
			Location:   "github:repo@main:path/file.md",
			Commit:     "deadbeef",
		},
	}
	reg := targets.NewRegistry()
	reg.Add(tgt)

	cfg := &config.Config{
		Server: config.ServerConfig{
			CallbackRetries: 2,
			CallbackBackoff: 10 * time.Millisecond,
			StorageDir:      t.TempDir(),
			MaxUploadSize:   config.ByteSize(10 * 1024 * 1024),
		},
		Target: config.TargetsConfig{
			GitHub: config.GitHubTargetConfig{
				Enabled: true,
			},
		},
	}

	worker := New(discardLogger(), cfg, store, llmClient, reg)

	// Temp image file
	imgPath := filepathJoin(t.TempDir(), "img.png")
	if err := os.WriteFile(imgPath, []byte("fakeimg"), 0o600); err != nil {
		t.Fatalf("write img: %v", err)
	}

	cbURL := cbSrv.URL
	meta := map[string]any{"k": "v"}
	job := jobs.Job{
		ID:          "job-1",
		ImagePath:   imgPath,
		MimeType:    common.MimeImagePNG,
		TargetName:  "github",
		CallbackURL: &cbURL,
		Metadata:    meta,
		Stage:       jobs.StageQueued,
		CreatedAt:   time.Now().UTC(),
	}
	_ = store.CreateJob(context.Background(), &job)

	// Process
	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	got, _ := store.GetJob(context.Background(), job.ID)
	if got == nil || got.Stage != jobs.StageCompleted {
		t.Fatalf("job not completed: %+v", got)
	}
	if got.TargetLocation == nil || got.TargetCommit == nil {
		t.Fatalf("result not saved: loc=%v commit=%v", got.TargetLocation, got.TargetCommit)
	}

	// Callback asserted
	cbMu.Lock()
	defer cbMu.Unlock()
	if len(cbBodies) == 0 {
		t.Fatalf("expected callback to be posted")
	}
	if cbBodies[0]["status"] != common.StatusCompleted {
		t.Fatalf("callback status mismatch: %v", cbBodies[0]["status"])
	}
}

func TestWorker_Process_LLMError_SetsFailed(t *testing.T) {
	store := newMemStore()
	llmClient := &llmMock{err: errors.New("boom")}
	tgt := &targetMock{name: "github"}
	reg := targets.NewRegistry()
	reg.Add(tgt)

	cfg := &config.Config{
		Server: config.ServerConfig{
			CallbackRetries: 1,
			CallbackBackoff: 10 * time.Millisecond,
			StorageDir:      t.TempDir(),
			MaxUploadSize:   config.ByteSize(10 * 1024 * 1024),
		},
		Target: config.TargetsConfig{
			GitHub: config.GitHubTargetConfig{
				Enabled: true,
			},
		},
	}
	worker := New(discardLogger(), cfg, store, llmClient, reg)

	// Temp image file
	imgPath := filepathJoin(t.TempDir(), "img.png")
	if err := os.WriteFile(imgPath, []byte("fakeimg"), 0o600); err != nil {
		t.Fatalf("write img: %v", err)
	}

	job := jobs.Job{
		ID:         "job-2",
		ImagePath:  imgPath,
		MimeType:   common.MimeImagePNG,
		TargetName: "github",
		Stage:      jobs.StageQueued,
		CreatedAt:  time.Now().UTC(),
	}
	_ = store.CreateJob(context.Background(), &job)

	// Process (should fail)
	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err == nil {
		t.Fatalf("expected error")
	}
	got, _ := store.GetJob(context.Background(), job.ID)
	if got == nil || got.Stage != jobs.StageFailed {
		t.Fatalf("job not failed: %+v", got)
	}
}

// filepathJoin to avoid importing path/filepath in multiple places in this test.
func filepathJoin(dir, name string) string {
	return dir + string(os.PathSeparator) + name
}

func makeTempImage(t *testing.T) string {
	t.Helper()
	p := filepathJoin(t.TempDir(), "img.png")
	if err := os.WriteFile(p, []byte("fakeimg"), 0o600); err != nil {
		t.Fatalf("write img: %v", err)
	}
	return p
}

func makeWorkerWithArchive(t *testing.T, llmClient *llmMock, archive bool) (*Worker, *capturingTarget, jobs.Store) {
	t.Helper()
	store := newMemStore()
	tgt := &capturingTarget{name: "github"}
	reg := targets.NewRegistry()
	reg.Add(tgt)
	cfg := &config.Config{
		Server: config.ServerConfig{
			CallbackRetries: 1,
			CallbackBackoff: 10 * time.Millisecond,
			StorageDir:      t.TempDir(),
			MaxUploadSize:   config.ByteSize(10 * 1024 * 1024),
		},
		Target: config.TargetsConfig{
			GitHub: config.GitHubTargetConfig{
				Enabled: true,
				Archive: config.ArchiveConfig{Enabled: archive, Path: "originals/"},
			},
		},
	}
	return New(discardLogger(), cfg, store, llmClient, reg), tgt, store
}

func TestWorker_PopulatesOriginal_WhenArchiveEnabled(t *testing.T) {
	llmClient := &llmMock{out: "body text"}
	worker, tgt, store := makeWorkerWithArchive(t, llmClient, true)

	job := jobs.Job{
		ID: "ja", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(context.Background(), &job)

	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if tgt.lastReq.Original == nil {
		t.Fatal("expected Original to be populated when archive enabled")
	}
	if tgt.lastReq.Original.Extension != ".png" {
		t.Fatalf("expected .png extension, got %q", tgt.lastReq.Original.Extension)
	}
	if string(tgt.lastReq.Original.Content) != "fakeimg" {
		t.Fatalf("original content mismatch: %q", tgt.lastReq.Original.Content)
	}
}

func TestWorker_OriginalNil_WhenArchiveDisabled(t *testing.T) {
	llmClient := &llmMock{out: "body text"}
	worker, tgt, store := makeWorkerWithArchive(t, llmClient, false)

	job := jobs.Job{
		ID: "jb", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(context.Background(), &job)

	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if tgt.lastReq.Original != nil {
		t.Fatal("expected Original to be nil when archive disabled")
	}
}

func makeWorkerWithNaming(t *testing.T, llmClient *llmMock, strategy string) (*Worker, *capturingTarget, jobs.Store) {
	t.Helper()
	store := newMemStore()
	tgt := &capturingTarget{name: "github"}
	reg := targets.NewRegistry()
	reg.Add(tgt)
	cfg := &config.Config{
		Server: config.ServerConfig{
			CallbackRetries: 1,
			CallbackBackoff: 10 * time.Millisecond,
			StorageDir:      t.TempDir(),
			MaxUploadSize:   config.ByteSize(10 * 1024 * 1024),
		},
		Target: config.TargetsConfig{
			GitHub: config.GitHubTargetConfig{
				Enabled:        true,
				NamingStrategy: strategy,
			},
		},
	}
	return New(discardLogger(), cfg, store, llmClient, reg), tgt, store
}

func runAINamingJob(t *testing.T, llmClient *llmMock) *capturingTarget {
	t.Helper()
	worker, tgt, store := makeWorkerWithNaming(t, llmClient, "ai")
	job := jobs.Job{
		ID: "jn", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(context.Background(), &job)
	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return tgt
}

func TestWorker_AINaming_PropagatesSanitisedSlug(t *testing.T) {
	llmClient := &llmMock{out: "body", suggestedSlug: "Invoice November 2024!"}
	tgt := runAINamingJob(t, llmClient)

	if !llmClient.slugCalled {
		t.Fatal("expected SuggestFilename to be called for ai strategy")
	}
	if tgt.lastReq.SuggestedSlug == nil {
		t.Fatal("expected SuggestedSlug to be propagated")
	}
	if *tgt.lastReq.SuggestedSlug != "invoice-november-2024" {
		t.Fatalf("slug not sanitised: %q", *tgt.lastReq.SuggestedSlug)
	}
}

func TestWorker_AINaming_CapsLongSlug(t *testing.T) {
	long := strings.Repeat("word ", 40) // far exceeds slugMaxLen
	llmClient := &llmMock{out: "body", suggestedSlug: long}
	tgt := runAINamingJob(t, llmClient)

	if tgt.lastReq.SuggestedSlug == nil {
		t.Fatal("expected SuggestedSlug to be propagated")
	}
	got := *tgt.lastReq.SuggestedSlug
	if len(got) > slugMaxLen {
		t.Fatalf("slug not capped: len=%d %q", len(got), got)
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("capped slug must not end with hyphen: %q", got)
	}
}

func TestWorker_AINaming_EmptySlugFallsBack(t *testing.T) {
	llmClient := &llmMock{out: "body", suggestedSlug: "   "}
	tgt := runAINamingJob(t, llmClient)

	if tgt.lastReq.SuggestedSlug != nil {
		t.Fatalf("expected nil SuggestedSlug (fallback to JobID), got %q", *tgt.lastReq.SuggestedSlug)
	}
}

func TestWorker_AINaming_LLMErrorFallsBack(t *testing.T) {
	llmClient := &llmMock{out: "body", slugErr: errors.New("llm down")}
	tgt := runAINamingJob(t, llmClient)

	if tgt.lastReq.SuggestedSlug != nil {
		t.Fatalf("expected nil SuggestedSlug on LLM error, got %q", *tgt.lastReq.SuggestedSlug)
	}
}

func TestWorker_AINaming_SkippedForNonAIStrategy(t *testing.T) {
	llmClient := &llmMock{out: "body", suggestedSlug: "should-not-be-used"}
	worker, tgt, store := makeWorkerWithNaming(t, llmClient, "title")
	job := jobs.Job{
		ID: "jt", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(context.Background(), &job)
	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if llmClient.slugCalled {
		t.Fatal("SuggestFilename must not be called when strategy != ai")
	}
	if tgt.lastReq.SuggestedSlug != nil {
		t.Fatalf("expected nil SuggestedSlug for non-ai strategy, got %q", *tgt.lastReq.SuggestedSlug)
	}
}
