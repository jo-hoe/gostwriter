package common

import "testing"

func TestConstantsValues(t *testing.T) {
	if ContentTypeJSON != "application/json" {
		t.Fatalf("ContentTypeJSON = %q", ContentTypeJSON)
	}
	if HeaderAPIKey != "X-API-Key" {
		t.Fatalf("HeaderAPIKey = %q", HeaderAPIKey)
	}
	if HeaderPrefer != "Prefer" {
		t.Fatalf("HeaderPrefer = %q", HeaderPrefer)
	}
	if PreferRespondAsync != "respond-async" {
		t.Fatalf("PreferRespondAsync = %q", PreferRespondAsync)
	}
	if PathHealthz != "/healthz" || PathTranscriptions != "/v1/transcriptions" {
		t.Fatalf("paths mismatch: %q, %q", PathHealthz, PathTranscriptions)
	}
	if DefaultQueueCapacity <= 0 || DefaultWorkerCount <= 0 {
		t.Fatalf("defaults should be positive")
	}
	if GitExecutable == "" || GitRemoteName == "" {
		t.Fatalf("git constants should be non-empty")
	}
	if MimeImagePNG != "image/png" || MimeImageJPEG != "image/jpeg" || MimeImageJPG != "image/jpg" {
		t.Fatalf("mime constants mismatch")
	}
	if UploadsDirName == "" || ReposDirName == "" {
		t.Fatalf("dir names should be non-empty")
	}
	if StatusCompleted != "completed" || StatusFailed != "failed" {
		t.Fatalf("status constants mismatch")
	}
}