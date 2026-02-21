package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jo-hoe/gostwriter/internal/common"
	"github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/jobs"
	"github.com/jo-hoe/gostwriter/internal/llm"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

// Worker implements jobs.Processor to handle transcription and posting.
type Worker struct {
	Log     *slog.Logger
	Cfg     *config.Config
	Store   jobs.Store
	LLM     llm.Client
	Targets *targets.Registry
}

// Ensure Worker implements jobs.Processor
var _ jobs.Processor = (*Worker)(nil)

func New(log *slog.Logger, cfg *config.Config, store jobs.Store, c llm.Client, regs *targets.Registry) *Worker {
	return &Worker{
		Log:     log,
		Cfg:     cfg,
		Store:   store,
		LLM:     c,
		Targets: regs,
	}
}

func (w *Worker) Process(ctx context.Context, item jobs.WorkItem) error {
	job := item.Job
	now := time.Now().UTC()
	if err := w.Store.UpdateStage(job.ID, jobs.StageTranscribing, &now); err != nil {
		return fmt.Errorf("update stage to transcribing: %w", err)
	}

	f, err := os.Open(job.ImagePath)
	if err != nil {
		w.finishWithError(job.ID, fmt.Errorf("open image: %w", err))
		return err
	}
	defer f.Close()

	md, err := w.LLM.TranscribeImage(ctx, f, job.MimeType)
	if err != nil {
		w.finishWithError(job.ID, fmt.Errorf("llm transcribe: %w", err))
		return err
	}

	// Optionally prepend title as Markdown H1.
	if job.Title != nil && *job.Title != "" {
		md = fmt.Sprintf("# %s\n\n%s", *job.Title, md)
	}

	// Posting stage
	startPost := time.Now().UTC()
	if err := w.Store.UpdateStage(job.ID, jobs.StagePosting, &startPost); err != nil {
		w.finishWithError(job.ID, fmt.Errorf("update stage to posting: %w", err))
		return err
	}

	t, ok := w.Targets.Get(job.TargetName)
	if !ok {
		w.finishWithError(job.ID, fmt.Errorf("target %q not registered", job.TargetName))
		return fmt.Errorf("unknown target %q", job.TargetName)
	}

	req := targets.TargetRequest{
		JobID:          job.ID,
		Markdown:       md,
		SuggestedTitle: job.Title,
		Metadata:       job.Metadata,
		Timestamp:      time.Now().UTC(),
	}

	res, err := t.Post(ctx, req)
	if err != nil {
		w.finishWithError(job.ID, fmt.Errorf("target post: %w", err))
		return err
	}

	// Success
	done := time.Now().UTC()
	if err := w.Store.SaveResult(job.ID, res.Location, res.Commit, done); err != nil {
		return fmt.Errorf("save result: %w", err)
	}

	// Callback if provided
	if job.CallbackURL != nil && *job.CallbackURL != "" {
		cbErr := w.sendCallbackWithRetry(ctx, *job.CallbackURL, callbackPayload{
			JobID:  job.ID,
			Status: common.StatusCompleted,
			Stage:  string(jobs.StageCompleted),
			Error:  nil,
			Result: &callbackResult{
				Target:   res.TargetName,
				Location: res.Location,
				Commit:   res.Commit,
			},
		})
		if cbErr != nil {
			w.Log.Warn("callback failed after retries", "job_id", job.ID, "err", cbErr)
		}
	}

	return nil
}

func (w *Worker) finishWithError(jobID string, err error) {
	done := time.Now().UTC()
	_ = w.Store.SaveError(jobID, err.Error(), done)
}

type callbackPayload struct {
	JobID  string          `json:"job_id"`
	Status string          `json:"status"` // completed|failed
	Stage  string          `json:"stage"`
	Error  *string         `json:"error,omitempty"`
	Result *callbackResult `json:"result,omitempty"`
}

type callbackResult struct {
	Target   string `json:"target"`
	Location string `json:"location"`
	Commit   string `json:"commit"`
}

func (w *Worker) sendCallbackWithRetry(ctx context.Context, url string, payload callbackPayload) error {
	max := w.Cfg.Server.CallbackRetries
	if max <= 0 {
		max = 3
	}
	backoff := w.Cfg.Server.CallbackBackoff
	if backoff <= 0 {
		backoff = 2 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= max; attempt++ {
		if err := w.postJSON(ctx, url, payload); err != nil {
			lastErr = err
			// If context was cancelled, stop retries.
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return err
			}
			// Sleep with simple backoff
			time.Sleep(time.Duration(attempt) * backoff)
			continue
		}
		return nil
	}
	return lastErr
}

func (w *Worker) postJSON(ctx context.Context, url string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", common.ContentTypeJSON)
	// Optional: include a simple signature or key if required in future

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback status %d", resp.StatusCode)
	}
	return nil
}
