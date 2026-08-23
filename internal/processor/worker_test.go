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
	"sync"
	"testing"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

type memStore struct {
	mu   sync.Mutex
	jobs map[string]*jobs.Job
}

func newMemStore() *memStore {
	return &memStore{jobs: make(map[string]*jobs.Job)}
}

func (s *memStore) CreateJob(job *jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *job
	s.jobs[job.ID] = &c
	return nil
}

func (s *memStore) UpdateStage(id string, stage jobs.Stage, startedAt *time.Time) error {
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

func (s *memStore) SaveResult(id string, location, commit string, completedAt time.Time) error {
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

func (s *memStore) SaveError(id string, errMsg string, completedAt time.Time) error {
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

func (s *memStore) GetJob(id string) (*jobs.Job, error) {
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
	out            string
	err            error
	generatedTitle string
	titleErr       error
	titleCalled    bool
}

func (m *llmMock) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	_, _ = io.Copy(io.Discard, r)
	return m.out, nil
}

func (m *llmMock) GenerateTitle(_ context.Context, _ string) (string, error) {
	m.titleCalled = true
	return m.generatedTitle, m.titleErr
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
	title := "Title"
	meta := map[string]any{"k": "v"}
	job := jobs.Job{
		ID:          "job-1",
		ImagePath:   imgPath,
		MimeType:    common.MimeImagePNG,
		TargetName:  "github",
		CallbackURL: &cbURL,
		Title:       &title,
		Metadata:    meta,
		Stage:       jobs.StageQueued,
		CreatedAt:   time.Now().UTC(),
	}
	_ = store.CreateJob(&job)

	// Process
	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process error: %v", err)
	}

	got, _ := store.GetJob(job.ID)
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
	_ = store.CreateJob(&job)

	// Process (should fail)
	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err == nil {
		t.Fatalf("expected error")
	}
	got, _ := store.GetJob(job.ID)
	if got == nil || got.Stage != jobs.StageFailed {
		t.Fatalf("job not failed: %+v", got)
	}
}

// filepathJoin to avoid importing path/filepath in multiple places in this test.
func filepathJoin(dir, name string) string {
	return dir + string(os.PathSeparator) + name
}

func makeWorkerWithCapture(t *testing.T, llmClient *llmMock, generateTitle bool) (*Worker, *capturingTarget, jobs.Store) {
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
				Enabled:       true,
				GenerateTitle: generateTitle,
			},
		},
	}
	return New(discardLogger(), cfg, store, llmClient, reg), tgt, store
}

func makeTempImage(t *testing.T) string {
	t.Helper()
	p := filepathJoin(t.TempDir(), "img.png")
	if err := os.WriteFile(p, []byte("fakeimg"), 0o600); err != nil {
		t.Fatalf("write img: %v", err)
	}
	return p
}

func TestWorker_GeneratesTitle_WhenEnabled(t *testing.T) {
	llmClient := &llmMock{out: "body text", generatedTitle: "Generated Title"}
	worker, tgt, store := makeWorkerWithCapture(t, llmClient, true)

	job := jobs.Job{
		ID: "j1", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(&job)

	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !llmClient.titleCalled {
		t.Fatal("expected GenerateTitle to be called")
	}
	if tgt.lastReq.SuggestedTitle == nil || *tgt.lastReq.SuggestedTitle != "Generated Title" {
		t.Fatalf("SuggestedTitle not propagated: %v", tgt.lastReq.SuggestedTitle)
	}
}

func TestWorker_SkipsTitle_WhenDisabled(t *testing.T) {
	llmClient := &llmMock{out: "body text", generatedTitle: "Should Not Appear"}
	worker, _, store := makeWorkerWithCapture(t, llmClient, false)

	job := jobs.Job{
		ID: "j2", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(&job)
	_ = worker.Process(context.Background(), jobs.WorkItem{Job: job})

	if llmClient.titleCalled {
		t.Fatal("GenerateTitle should not be called when generateTitle=false")
	}
}

func TestWorker_UserTitlePreserved_WhenGenerateEnabled(t *testing.T) {
	llmClient := &llmMock{out: "body text", generatedTitle: "LLM Title"}
	worker, tgt, store := makeWorkerWithCapture(t, llmClient, true)

	userTitle := "User Supplied"
	job := jobs.Job{
		ID: "j3", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Title: &userTitle, Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(&job)
	_ = worker.Process(context.Background(), jobs.WorkItem{Job: job})

	if llmClient.titleCalled {
		t.Fatal("GenerateTitle should not be called when user already supplied a title")
	}
	if tgt.lastReq.SuggestedTitle == nil || *tgt.lastReq.SuggestedTitle != "User Supplied" {
		t.Fatalf("user title not preserved: %v", tgt.lastReq.SuggestedTitle)
	}
}

func TestWorker_TitleGenerationFailureIsNonFatal(t *testing.T) {
	llmClient := &llmMock{out: "body text", titleErr: errors.New("llm unavailable")}
	worker, _, store := makeWorkerWithCapture(t, llmClient, true)

	job := jobs.Job{
		ID: "j4", ImagePath: makeTempImage(t), MimeType: common.MimeImagePNG,
		TargetName: "github", Stage: jobs.StageQueued, CreatedAt: time.Now().UTC(),
	}
	_ = store.CreateJob(&job)

	if err := worker.Process(context.Background(), jobs.WorkItem{Job: job}); err != nil {
		t.Fatalf("job should complete despite title generation failure, got: %v", err)
	}
	got, _ := store.GetJob(job.ID)
	if got.Stage != jobs.StageCompleted {
		t.Fatalf("expected completed, got %q", got.Stage)
	}
}
