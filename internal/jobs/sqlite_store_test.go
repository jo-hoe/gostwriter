package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStore_JobLifecycle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "jobs.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)

	job := &Job{
		ID:         "job-1",
		ImagePath:  filepath.Join(dir, "img.png"),
		MimeType:   "image/png",
		TargetName: "docs",
		CallbackURL: func() *string {
			v := "http://example.com/callback"
			return &v
		}(),
		Title: func() *string {
			v := "Title"
			return &v
		}(),
		Metadata:  map[string]any{"k": "v"},
		Stage:     StageQueued,
		CreatedAt: now,
	}

	// Create a fake image file path for completeness (store doesn't validate it)
	if err := os.WriteFile(job.ImagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("write img: %v", err)
	}

	if err := store.CreateJob(job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Update stage to transcribing with startedAt
	start := now.Add(1 * time.Second)
	if err := store.UpdateStage(job.ID, StageTranscribing, &start); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}

	// Save result to mark completed
	comp := now.Add(2 * time.Second)
	if err := store.SaveResult(job.ID, "git:loc", "deadbeef", comp); err != nil {
		t.Fatalf("SaveResult: %v", err)
	}

	got, err := store.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ID != job.ID || got.Stage != StageCompleted {
		t.Fatalf("job mismatch or not completed: %+v", got)
	}
	if got.TargetLocation == nil || *got.TargetLocation != "git:loc" {
		t.Fatalf("location mismatch: %+v", got.TargetLocation)
	}
	if got.TargetCommit == nil || *got.TargetCommit != "deadbeef" {
		t.Fatalf("commit mismatch: %+v", got.TargetCommit)
	}

	// Save error to mark failed
	failTime := now.Add(3 * time.Second)
	if err := store.SaveError(job.ID, "boom", failTime); err != nil {
		t.Fatalf("SaveError: %v", err)
	}
	got2, err := store.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob after error: %v", err)
	}
	if got2.Stage != StageFailed {
		t.Fatalf("stage should be failed, got %s", got2.Stage)
	}
	if got2.ErrorMessage == nil || *got2.ErrorMessage != "boom" {
		t.Fatalf("error message mismatch: %+v", got2.ErrorMessage)
	}
}