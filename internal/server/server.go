package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/storage"
	"github.com/jo-hoe/gostwriter/internal/targets"
	"github.com/jo-hoe/gostwriter/internal/util"
)

type Service struct {
	Log       *slog.Logger
	Cfg       *config.Config
	Store     jobs.Store
	Queue     *jobs.Queue
	Uploader  *storage.Uploader
	Targets   *targets.Registry
	Processor jobs.Processor
}

// NewHTTPServer builds the http.Server with routes and middleware.
func NewHTTPServer(svc *Service) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(http.MethodGet+" "+common.PathHealthz, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc(http.MethodPost+" "+common.PathTranscriptions, svc.withCommon(svc.handleCreateTranscription))
	// Pattern match /v1/transcriptions/{id}
	mux.HandleFunc(http.MethodGet+" "+common.PathTranscriptions+"/", svc.withCommon(svc.handleGetTranscriptionByPrefix))

	s := &http.Server{
		Addr:         svc.Cfg.Server.Addr,
		Handler:      loggingMiddleware(recoveryMiddleware(mux), svc.Log),
		ReadTimeout:  svc.Cfg.Server.ReadTimeout,
		WriteTimeout: svc.Cfg.Server.WriteTimeout,
		IdleTimeout:  svc.Cfg.Server.IdleTimeout,
	}
	return s
}

func (svc *Service) withCommon(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Enforce API key if configured
		if key := strings.TrimSpace(svc.Cfg.Server.APIKey); key != "" {
			if r.Header.Get(common.HeaderAPIKey) != key {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		// Enforce max body size
		max := safeInt64(svc.Cfg.Server.MaxUploadSize)
		if max > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	}
}

type createResponse struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

func (svc *Service) handleCreateTranscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	// Parse multipart
	if err := r.ParseMultipartForm(safeInt64(svc.Cfg.Server.MaxUploadSize)); err != nil {
		http.Error(w, "invalid form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// File
	fileHeader := r.MultipartForm.File["file"]
	if len(fileHeader) == 0 {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	uploaded := fileHeader[0]

	// Target is fixed by configuration; request cannot override
	targetName := svc.Cfg.Target.Name

	// Optional fields
	callbackURLPtr, err := parseOptionalURL(r.FormValue("callback_url"))
	if err != nil {
		http.Error(w, "invalid callback_url", http.StatusBadRequest)
		return
	}
	titlePtr := parseOptionalString(r.FormValue("title"))
	metadata, err := parseOptionalJSONMap(r.FormValue("metadata"))
	if err != nil {
		http.Error(w, "invalid metadata json", http.StatusBadRequest)
		return
	}

	// Store upload
	imgPath, cleanup, mimeType, err := svc.Uploader.SaveMultipartImage(uploaded, safeInt64(svc.Cfg.Server.MaxUploadSize))
	if err != nil {
		http.Error(w, "upload failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Ensure we cleanup temp file if we fail later in this handler
	defer func() {
		// The worker will also call cleanup after processing, but if we failed before enqueue, cleanup here
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	// Build job
	jobID := util.NewID()
	job := jobs.Job{
		ID:          jobID,
		ImagePath:   imgPath,
		MimeType:    mimeType,
		TargetName:  targetName,
		CallbackURL: callbackURLPtr,
		Title:       titlePtr,
		Metadata:    metadata,
		Stage:       jobs.StageQueued,
		CreatedAt:   time.Now().UTC(),
	}

	if err := svc.Store.CreateJob(&job); err != nil {
		if svc.Log != nil {
			svc.Log.Error("persist job", "error", err)
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if svc.Log != nil {
		svc.Log.Info("job created", "job_id", jobID, "target", targetName)
	}

	// Determine sync vs async based on Prefer header
	prefer := strings.ToLower(strings.TrimSpace(r.Header.Get(common.HeaderPrefer)))
	async := strings.Contains(prefer, common.PreferRespondAsync)

	if async {
		// Enqueue for async processing; transfer cleanup responsibility to worker on success
		err = svc.Queue.Enqueue(jobs.WorkItem{
			Job:     job,
			Cleanup: cleanup,
		})
		if err != nil {
			// Failed to enqueue; cleanup will run due to defer
			http.Error(w, "queue full, try later", http.StatusServiceUnavailable)
			return
		}
		if svc.Log != nil {
			svc.Log.Info("job enqueued", "job_id", jobID)
		}
		// We handed cleanup to the worker. Prevent double-delete here.
		cleanup = nil

		writeJSON(w, http.StatusAccepted, createResponse{
			JobID:     jobID,
			StatusURL: path.Join(common.PathTranscriptions, jobID),
		})
		return
	}

	// Synchronous processing path: process the job inline and return result.
	if err := svc.Processor.Process(r.Context(), jobs.WorkItem{Job: job}); err != nil {
		if svc.Log != nil {
			svc.Log.Error("processing failed", "error", err)
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Synchronous success: return 200 with no details
	if svc.Log != nil {
		svc.Log.Info("job processed (sync)", "job_id", jobID)
	}
	w.WriteHeader(http.StatusOK)
}

var idPattern = regexp.MustCompile(fmt.Sprintf("^%s/([a-f0-9-]+)$", common.PathTranscriptions))

func (svc *Service) handleGetTranscriptionByPrefix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	m := idPattern.FindStringSubmatch(r.URL.Path)
	if len(m) != 2 {
		http.NotFound(w, r)
		return
	}
	id := m[1]
	job, err := svc.Store.GetJob(id)
	if err != nil || job == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, jobToOut(job))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func jobToOut(job *jobs.Job) map[string]any {
	type result struct {
		Target   string `json:"target"`
		Location string `json:"location"`
		Commit   string `json:"commit"`
	}
	var errVal any = nil
	if job.ErrorMessage != nil && *job.ErrorMessage != "" {
		errVal = "internal error"
	}
	out := map[string]any{
		"job_id":       job.ID,
		"stage":        string(job.Stage),
		"created_at":   job.CreatedAt,
		"started_at":   job.StartedAt,
		"completed_at": job.CompletedAt,
		"error":        errVal,
	}
	if job.TargetLocation != nil || job.TargetCommit != nil {
		out["target_result"] = result{
			Target:   job.TargetName,
			Location: deref(job.TargetLocation),
			Commit:   deref(job.TargetCommit),
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", common.ContentTypeJSON)
	if status != 0 {
		w.WriteHeader(status)
	}
	_ = json.NewEncoder(w).Encode(v)
}

func safeInt64(u config.ByteSize) int64 {
	if u > config.ByteSize(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(u) // #nosec G115 - safe cast after explicit upper-bound check
}

func parseOptionalURL(s string) (*string, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil, nil
	}
	if _, err := url.ParseRequestURI(v); err != nil {
		return nil, err
	}
	return &v, nil
}

func parseOptionalString(s string) *string {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil
	}
	return &v
}

func parseOptionalJSONMap(s string) (map[string]any, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func loggingMiddleware(next http.Handler, log *slog.Logger) http.Handler {
	// Fallback to a discard logger if none provided to avoid nil deref in tests or minimal setups.
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &writeWrap{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(ww, r)
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.code,
			"duration", time.Since(start).String(),
			"remote", r.RemoteAddr)
	})
}

type writeWrap struct {
	http.ResponseWriter
	code int
}

func (w *writeWrap) WriteHeader(statusCode int) {
	w.code = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
