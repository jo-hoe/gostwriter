package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/storage"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

type memStore struct {
	mu   sync.Mutex
	data map[string]*jobs.Job
}

func newMemStore() *memStore {
	return &memStore{data: make(map[string]*jobs.Job)}
}

func (s *memStore) CreateJob(job *jobs.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cpy := *job
	s.data[job.ID] = &cpy
	return nil
}

func (s *memStore) UpdateStage(id string, stage jobs.Stage, startedAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.data[id]; ok {
		j.Stage = stage
		if startedAt != nil {
			st := *startedAt
			j.StartedAt = &st
		}
		return nil
	}
	return nil
}

func (s *memStore) SaveResult(id string, location, commit string, completedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.data[id]; ok {
		j.Stage = jobs.StageCompleted
		loc := location
		com := commit
		j.TargetLocation = &loc
		j.TargetCommit = &com
		ct := completedAt
		j.CompletedAt = &ct
		return nil
	}
	return nil
}

func (s *memStore) SaveError(id string, errMsg string, completedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.data[id]; ok {
		j.Stage = jobs.StageFailed
		e := errMsg
		j.ErrorMessage = &e
		ct := completedAt
		j.CompletedAt = &ct
		return nil
	}
	return nil
}

func (s *memStore) GetJob(id string) (*jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.data[id]; ok {
		c := *j
		return &c, nil
	}
	return nil, nil
}

func (s *memStore) Close() error { return nil }

type fakeProcessor struct {
	store *memStore
}

func (p *fakeProcessor) Process(ctx context.Context, item jobs.WorkItem) error {
	// Simulate synchronous completion by marking the job complete
	return p.store.SaveResult(item.Job.ID, "git:loc", "deadbeef", time.Now().UTC())
}

func TestHealthz(t *testing.T) {
	svc := &Service{
		Log:       nil,
		Cfg:       &config.Config{Server: config.ServerConfig{Addr: ":0"}},
		Store:     newMemStore(),
		Queue:     nil,
		Uploader:  nil,
		Targets:   targets.NewRegistry(),
		Processor: nil,
	}
	srv := NewHTTPServer(svc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, common.PathHealthz, nil)
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func makeMultipart(t *testing.T, fieldName, filename, contentType string, content []byte) (string, *bytes.Buffer) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(content)); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ct := w.FormDataContentType()
	// Override content type header inside file header is not trivial here; uploader reads header from fileHeader
	// We rely on extension detection if header is empty, so pass .png
	_ = contentType
	return ct, &b
}

func TestCreateTranscription_Synchronous200(t *testing.T) {
	tmp := t.TempDir()
	store := newMemStore()
	uploader := storage.NewUploader(tmp)
	svc := &Service{
		Log: nil,
		Cfg: &config.Config{
			Server: config.ServerConfig{
				Addr:           ":0",
				MaxUploadSize:  config.ByteSize(10 * 1024 * 1024),
				StorageDir:     tmp,
				CallbackRetries: 1,
				CallbackBackoff: 10 * time.Millisecond,
			},
			Target: config.TargetEntry{
				Type: "git",
				Name: "docs",
			},
		},
		Store:     store,
		Queue:     nil, // not used in sync path
		Uploader:  uploader,
		Targets:   targets.NewRegistry(),
		Processor: &fakeProcessor{store: store},
	}
	server := NewHTTPServer(svc)

	ctype, body := makeMultipart(t, "file", "img.png", "image/png", []byte("img"))
	req := httptest.NewRequest(http.MethodPost, common.PathTranscriptions, body)
	req.Header.Set("Content-Type", ctype)
	// no Prefer header => synchronous
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp["stage"] != string(jobs.StageCompleted) {
		t.Fatalf("stage not completed: %v", resp["stage"])
	}
	tr, ok := resp["target_result"].(map[string]any)
	if !ok || tr["commit"] == "" {
		t.Fatalf("target_result missing: %v", tr)
	}
}

func TestCreateTranscription_Asynchronous202(t *testing.T) {
	tmp := t.TempDir()
	store := newMemStore()
	uploader := storage.NewUploader(tmp)

	// Real queue with no-op processor
	logger := slogDiscard{}
	queue := jobs.NewQueue(logger.Logger(), 2, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Processor for queue won't be used by handler, but worker needs something
	if err := queue.Start(ctx, &fakeProcessor{store: store}); err != nil {
		t.Fatalf("queue start: %v", err)
	}
	defer queue.Shutdown(1 * time.Second)

	svc := &Service{
		Log: nil,
		Cfg: &config.Config{
			Server: config.ServerConfig{
				Addr:           ":0",
				MaxUploadSize:  config.ByteSize(10 * 1024 * 1024),
				StorageDir:     tmp,
				CallbackRetries: 1,
				CallbackBackoff: 10 * time.Millisecond,
			},
			Target: config.TargetEntry{
				Type: "git",
				Name: "docs",
			},
		},
		Store:     store,
		Queue:     queue,
		Uploader:  uploader,
		Targets:   targets.NewRegistry(),
		Processor: &fakeProcessor{store: store}, // not used in async
	}
	server := NewHTTPServer(svc)

	ctype, body := makeMultipart(t, "file", "img.jpg", "image/jpeg", []byte("img"))
	req := httptest.NewRequest(http.MethodPost, common.PathTranscriptions, body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set(common.HeaderPrefer, common.PreferRespondAsync)
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := resp["job_id"]; !ok {
		t.Fatalf("missing job_id")
	}
	if su, ok := resp["status_url"].(string); !ok || !strings.HasPrefix(su, common.PathTranscriptions) {
		t.Fatalf("status_url invalid: %v", resp["status_url"])
	}
}

// slogDiscard wraps a no-op slog handler for tests.
type slogDiscard struct{}

func (s slogDiscard) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}