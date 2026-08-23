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
	"github.com/jo-hoe/gostwriter/internal/naming"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

// stubNamer returns a fixed path regardless of the request.
type stubNamer struct{ path string }

func (s *stubNamer) Name(_ naming.NamingRequest) (string, error) { return s.path, nil }

func newTestTarget(t *testing.T, cfg appcfg.GitHubTargetConfig, namer naming.FileNamer) *Target {
	t.Helper()
	if namer == nil {
		var err error
		namer, err = naming.NewTemplateNamer(cfg.FilenameTemplate, `{{ .JobID }}.md`, cfg.BasePath)
		if err != nil {
			t.Fatalf("NewTemplateNamer: %v", err)
		}
	}
	tg, err := New("docs", cfg, namer)
	if err != nil {
		t.Fatalf("New github target: %v", err)
	}
	return tg
}

func TestRenderCommitMessage(t *testing.T) {
	cfg := appcfg.GitHubTargetConfig{
		BasePath:              "inbox/",
		FilenameTemplate:      "{{ .JobID }}.md",
		CommitMessageTemplate: "Add {{ .JobID }}",
		RepositoryOwner:       "org",
		RepositoryName:        "repo",
		Branch:                "main",
		Auth:                  appcfg.GitHubAuthConfig{Token: "x"},
	}
	tg := newTestTarget(t, cfg, nil)

	req := targets.TargetRequest{
		JobID:     "job-123",
		Markdown:  "md",
		Timestamp: time.Now().UTC(),
		Metadata:  map[string]any{"k": "v"},
	}

	msg, err := tg.renderCommitMessage(req)
	if err != nil {
		t.Fatalf("renderCommitMessage: %v", err)
	}
	if !strings.Contains(msg, "job-123") {
		t.Fatalf("commit message mismatch: %s", msg)
	}

	// Default commit message template
	tg.cfg.CommitMessageTemplate = ""
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
		defer func() { _ = r.Body.Close() }()
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
		RepositoryOwner:       "org",
		RepositoryName:        "repo",
		Branch:                "main",
		BasePath:              "inbox/",
		FilenameTemplate:      "{{ .JobID }}.md",
		CommitMessageTemplate: "Add {{ .JobID }}",
		APIBaseURL:            srv.URL,
		AuthorName:            "Bot",
		AuthorEmail:           "bot@example.com",
		Auth:                  appcfg.GitHubAuthConfig{Token: "token123"},
	}
	tg := newTestTarget(t, cfg, nil)
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

func TestPost_UsesInjectedNamer(t *testing.T) {
	var putPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		putPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"path": "custom/fixed-name.md"},
			"commit":  map[string]any{"sha": "abc"},
		})
	}))
	defer srv.Close()

	cfg := appcfg.GitHubTargetConfig{
		RepositoryOwner:       "org",
		RepositoryName:        "repo",
		Branch:                "main",
		CommitMessageTemplate: "Add {{ .JobID }}",
		APIBaseURL:            srv.URL,
		Auth:                  appcfg.GitHubAuthConfig{Token: "tok"},
	}
	namer := &stubNamer{path: "custom/fixed-name.md"}
	tg := newTestTarget(t, cfg, namer)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID: "j1", Markdown: "hello", Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if !strings.HasSuffix(putPath, "custom/fixed-name.md") {
		t.Fatalf("PUT path should use injected namer path, got: %s", putPath)
	}
}

func TestPost_CollisionNamerContextPropagated(t *testing.T) {
	var requestPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPaths = append(requestPaths, r.URL.Path)
		if r.Method == http.MethodGet {
			// First GET: file exists; second GET: free
			if len(requestPaths) == 1 {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"name": "note.md"})
			} else {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
			}
			return
		}
		// PUT response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"path": "inbox/note-1.md"},
			"commit":  map[string]any{"sha": "abc"},
		})
	}))
	defer srv.Close()

	cfg := appcfg.GitHubTargetConfig{
		RepositoryOwner:       "org",
		RepositoryName:        "repo",
		Branch:                "main",
		CommitMessageTemplate: "Add {{ .JobID }}",
		APIBaseURL:            srv.URL,
		Auth:                  appcfg.GitHubAuthConfig{Token: "tok"},
	}

	title := "note"
	inner := naming.NewTitleNamer("inbox/", ".md")
	checker := naming.NewGitHubPathChecker(srv.Client(), srv.URL, "org", "repo", "main", "tok")
	collisionNamer := naming.NewCollisionNamer(inner, checker, 10)

	tg := newTestTarget(t, cfg, collisionNamer)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID: "j1", Markdown: "hello", Timestamp: time.Now(),
		SuggestedTitle: &title,
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	// Verify the final PUT used the suffixed name
	lastPath := requestPaths[len(requestPaths)-1]
	if !strings.HasSuffix(lastPath, "note-1.md") {
		t.Fatalf("expected PUT to suffixed path, got: %s", lastPath)
	}
}
