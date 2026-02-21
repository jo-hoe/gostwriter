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
	mu    sync.Mutex
	jobs  map[string]*jobs.Job
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
	out string
	err error
}

func (m *llmMock) TranscribeImage(ctx context.Context, r io.Reader, mime string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	_, _ = io.Copy(io.Discard, r)
	return m.out, nil
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
		name: "docs",
		res: targets.TargetResult{
			TargetName: "docs",
			Location:   "git:repo@main:path/file.md",
			Commit:     "deadbeef",
		},
	}
	reg := targets.NewRegistry()
	reg.Add(tgt)

	cfg := &config.Config{
		Server: config.ServerConfig{
			CallbackRetries:  2,
			CallbackBackoff:  10 * time.Millisecond,
			StorageDir:       t.TempDir(),
			MaxUploadSize:    config.ByteSize(10 * 1024 * 1024),
		},
		Target: config.TargetEntry{
			Type: "git",
			Name: "docs",
		},
	}

	worker := New(discardLogger(), cfg, store, llmClient, reg)

	// Temp image file
	imgPath := filepathJoin(t.TempDir(), "img.png")
	if err := os.WriteFile(imgPath, []byte("fakeimg"), 0o644); err != nil {
		t.Fatalf("write img: %v", err)
	}

	cbURL := cbSrv.URL
	title := "Title"
	meta := map[string]any{"k": "v"}
	job := jobs.Job{
		ID:           "job-1",
		ImagePath:    imgPath,
		MimeType:     common.MimeImagePNG,
		TargetName:   "docs",
		CallbackURL:  &cbURL,
		Title:        &title,
		Metadata:     meta,
		Stage:        jobs.StageQueued,
		CreatedAt:    time.Now().UTC(),
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
	tgt := &targetMock{name: "docs"}
	reg := targets.NewRegistry()
	reg.Add(tgt)

	cfg := &config.Config{
		Server: config.ServerConfig{
			CallbackRetries:  1,
			CallbackBackoff:  10 * time.Millisecond,
			StorageDir:       t.TempDir(),
			MaxUploadSize:    config.ByteSize(10 * 1024 * 1024),
		},
		Target: config.TargetEntry{
			Type: "git",
			Name: "docs",
		},
	}
	worker := New(discardLogger(), cfg, store, llmClient, reg)

	// Temp image file
	imgPath := filepathJoin(t.TempDir(), "img.png")
	if err := os.WriteFile(imgPath, []byte("fakeimg"), 0o644); err != nil {
		t.Fatalf("write img: %v", err)
	}

	job := jobs.Job{
		ID:         "job-2",
		ImagePath:  imgPath,
		MimeType:   common.MimeImagePNG,
		TargetName: "docs",
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