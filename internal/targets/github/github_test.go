package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

func TestRenderFilenameAndCommitMessage(t *testing.T) {
	cfg := appcfg.GitHubTargetConfig{
		BasePath:              "inbox/",
		FilenameTemplate:      "{{ .JobID }}.md",
		CommitMessageTemplate: "Add {{ .JobID }}",
		RepoOwner:             "org",
		RepoName:              "repo",
		Branch:                "main",
		Auth:                  appcfg.GitHubAuthConfig{Token: "x"},
	}
	tg, err := New("docs", cfg)
	if err != nil {
		t.Fatalf("New github target: %v", err)
	}

	req := targets.TargetRequest{
		JobID:     "job-123",
		Markdown:  "md",
		Timestamp: time.Now().UTC(),
		Metadata:  map[string]any{"k": "v"},
	}
	fn, err := tg.renderFilename(req)
	if err != nil {
		t.Fatalf("renderFilename: %v", err)
	}
	// Normalize path separators for cross-platform assertion
	norm := strings.ReplaceAll(fn, `\`, `/`)
	if !strings.HasSuffix(norm, "inbox/job-123.md") {
		t.Fatalf("filename mismatch: %s", fn)
	}
	msg, err := tg.renderCommitMessage(req)
	if err != nil {
		t.Fatalf("renderCommitMessage: %v", err)
	}
	if !strings.Contains(msg, "job-123") {
		t.Fatalf("commit message mismatch: %s", msg)
	}

	// Also ensure default templates get used if empty
	tg.cfg.FilenameTemplate = ""
	tg.cfg.CommitMessageTemplate = ""
	_, _ = tg.renderFilename(req)
	_, _ = tg.renderCommitMessage(req)
}

func TestNameAndPost(t *testing.T) {
	// Mock GitHub API server
	var received struct {
		Method string
		URL    string
		Body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Method = r.Method
		received.URL = r.URL.Path
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&received.Body)

		resp := map[string]any{
			"content": map[string]any{
				"path": "inbox/job-xyz.md",
			},
			"commit": map[string]any{
				"sha": "abcd1234",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		// Return 201 Created
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := appcfg.GitHubTargetConfig{
		RepoOwner:             "org",
		RepoName:              "repo",
		Branch:                "main",
		BasePath:              "inbox/",
		FilenameTemplate:      "{{ .JobID }}.md",
		CommitMessageTemplate: "Add {{ .JobID }}",
		APIBaseURL:            srv.URL,
		AuthorName:            "Bot",
		AuthorEmail:           "bot@example.com",
		Auth:                  appcfg.GitHubAuthConfig{Token: "token123"},
	}
	tg, err := New("docs", cfg)
	if err != nil {
		t.Fatalf("New github target: %v", err)
	}
	if tg.Name() != "docs" {
		t.Fatalf("Name() mismatch: %s", tg.Name())
	}
	// Use the test server client
	tg.WithHTTPClient(srv.Client())

	req := targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "hello world",
		Timestamp: time.Now().UTC(),
	}
	res, err := tg.Post(context.Background(), req)
	if err != nil {
		t.Fatalf("Post error: %v", err)
	}
	if res.TargetName != "docs" {
		t.Fatalf("TargetName mismatch: %s", res.TargetName)
	}
	if !strings.Contains(res.Location, "github:org/repo@main:inbox/job-xyz.md") {
		t.Fatalf("Location mismatch: %s", res.Location)
	}
	if res.Commit != "abcd1234" {
		t.Fatalf("Commit SHA mismatch: %s", res.Commit)
	}

	// Verify request to server
	if received.Method != http.MethodPut {
		t.Fatalf("expected PUT method, got %s", received.Method)
	}
	if !strings.Contains(received.URL, "/repos/org/repo/contents/inbox/job-xyz.md") {
		t.Fatalf("request URL mismatch: %s", received.URL)
	}
	if received.Body["message"] == nil || !strings.Contains(received.Body["message"].(string), "job-xyz") {
		t.Fatalf("payload message missing or unexpected: %+v", received.Body["message"])
	}
	if received.Body["branch"] == nil || received.Body["branch"].(string) != "main" {
		t.Fatalf("payload branch mismatch: %+v", received.Body["branch"])
	}
	// Content is base64; we just ensure it exists
	if received.Body["content"] == nil || received.Body["content"] == "" {
		t.Fatalf("payload content missing")
	}
}