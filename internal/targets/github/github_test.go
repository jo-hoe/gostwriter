package github

import (
	"context"
	"encoding/base64"
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

func boolPtr(b bool) *bool { return &b }

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

	slug := "note"
	inner := naming.NewAINamer("inbox/", ".md")
	checker := naming.NewGitHubPathChecker(srv.Client(), srv.URL, "org", "repo", "main", "tok")
	collisionNamer := naming.NewCollisionNamer(inner, checker, 10)

	tg := newTestTarget(t, cfg, collisionNamer)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID: "j1", Markdown: "hello", Timestamp: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		SuggestedSlug: &slug,
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	// Verify the final PUT used the suffixed name (AINamer produces YYYYMMDD-HHMMSS-<slug>)
	lastPath := requestPaths[len(requestPaths)-1]
	if !strings.HasSuffix(lastPath, "note-1.md") {
		t.Fatalf("expected PUT to suffixed path, got: %s", lastPath)
	}
}

type recordedPut struct {
	Path string
	Body map[string]any
}

// newArchiveServer records every PUT and returns a success response.
func newArchiveServer(puts *[]recordedPut) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var body map[string]any
			defer func() { _ = r.Body.Close() }()
			_ = json.NewDecoder(r.Body).Decode(&body)
			*puts = append(*puts, recordedPut{Path: r.URL.Path, Body: body})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"path": "x"},
			"commit":  map[string]any{"sha": "sha-1"},
		})
	}))
}

func decodeContent(t *testing.T, p recordedPut) string {
	t.Helper()
	enc, _ := p.Body["content"].(string)
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("decode base64 content: %v", err)
	}
	return string(raw)
}

func archiveTestConfig(apiBaseURL string, archive bool) appcfg.GitHubTargetConfig {
	return appcfg.GitHubTargetConfig{
		RepositoryOwner:       "org",
		RepositoryName:        "repo",
		Branch:                "main",
		BasePath:              "inbox/",
		FilenameTemplate:      "{{ .JobID }}.md",
		CommitMessageTemplate: "Add {{ .JobID }}",
		APIBaseURL:            apiBaseURL,
		Auth:                  appcfg.GitHubAuthConfig{Token: "tok"},
		Archive:               appcfg.ArchiveConfig{Enabled: archive, Path: "originals/"},
	}
}

func TestPost_ArchiveTwoCommits(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	tg := newTestTarget(t, archiveTestConfig(srv.URL, true), nil)
	tg.WithHTTPClient(srv.Client())

	original := []byte("%PDF-1.4 fake pdf bytes")
	res, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody text",
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: original, Extension: ".pdf", MimeType: "application/pdf"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	if len(puts) != 2 {
		t.Fatalf("expected exactly 2 PUTs, got %d: %+v", len(puts), puts)
	}
	if !strings.HasSuffix(puts[0].Path, "/contents/originals/job-xyz.pdf") {
		t.Fatalf("first PUT should be the original, got: %s", puts[0].Path)
	}
	if !strings.HasSuffix(puts[1].Path, "/contents/inbox/job-xyz.md") {
		t.Fatalf("second PUT should be the transcription, got: %s", puts[1].Path)
	}
	md := decodeContent(t, puts[1])
	if strings.Contains(md, "> Original:") {
		t.Fatalf("archive without linkInFrontmatter must not inject a body blockquote:\n%s", md)
	}
	if !strings.Contains(res.Location, "inbox/job-xyz.md") {
		t.Fatalf("Location should point at the transcription, got: %s", res.Location)
	}
}

func TestPost_ArchiveDisabled_SingleCommit(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	tg := newTestTarget(t, archiveTestConfig(srv.URL, false), nil)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody",
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: []byte("x"), Extension: ".png"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(puts) != 1 {
		t.Fatalf("expected 1 PUT when archive disabled, got %d", len(puts))
	}
	if strings.Contains(decodeContent(t, puts[0]), "> Original:") {
		t.Fatalf("no link should be injected when archive disabled")
	}
}

func TestPost_ArchiveEnabled_OriginalNil(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	tg := newTestTarget(t, archiveTestConfig(srv.URL, true), nil)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody",
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(puts) != 1 {
		t.Fatalf("expected 1 PUT when Original is nil, got %d", len(puts))
	}
}

func TestPost_ArchiveSameFolderAsTranscription(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	// archive.path equal to basePath co-locates the original with the .md.
	cfg := archiveTestConfig(srv.URL, true)
	cfg.Archive.Path = "inbox/"
	tg := newTestTarget(t, cfg, nil)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody",
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: []byte("%PDF"), Extension: ".pdf", MimeType: "application/pdf"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(puts) != 2 {
		t.Fatalf("expected 2 PUTs, got %d: %+v", len(puts), puts)
	}
	if !strings.HasSuffix(puts[0].Path, "/contents/inbox/job-xyz.pdf") {
		t.Fatalf("original should sit in inbox/, got: %s", puts[0].Path)
	}
	if !strings.HasSuffix(puts[1].Path, "/contents/inbox/job-xyz.md") {
		t.Fatalf("transcription should sit in inbox/, got: %s", puts[1].Path)
	}
	// Without linkInFrontmatter the archive is silent — no link injected.
	if md := decodeContent(t, puts[1]); strings.Contains(md, "> Original:") {
		t.Fatalf("archive without linkInFrontmatter must not inject a body blockquote:\n%s", md)
	}
}

func TestPost_ArchiveSameFolderMarkdownOriginalRejected(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	// A Markdown original co-located with the transcription would overwrite it.
	cfg := archiveTestConfig(srv.URL, true)
	cfg.Archive.Path = "inbox/"
	tg := newTestTarget(t, cfg, nil)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody",
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: []byte("# raw"), Extension: ".md"},
	})
	if err == nil {
		t.Fatal("expected error when archiving a .md original into the transcription folder")
	}
	if len(puts) != 0 {
		t.Fatalf("no PUT should occur when the archive path would overwrite the transcription, got %d", len(puts))
	}
}

func TestSwapDirExt(t *testing.T) {
	cases := []struct {
		repoPath, newDir, newExt, want string
	}{
		{"inbox/foo.md", "originals/", ".pdf", "originals/foo.pdf"},
		{"inbox/2024-note.md", "archive/", ".png", "archive/2024-note.png"},
		{"a/b/c/x.md", "orig/", ".jpg", "orig/x.jpg"},
	}
	for _, c := range cases {
		if got := swapDirExt(c.repoPath, c.newDir, c.newExt); got != c.want {
			t.Errorf("swapDirExt(%q,%q,%q)=%q want %q", c.repoPath, c.newDir, c.newExt, got, c.want)
		}
	}
}

func TestRelLink(t *testing.T) {
	cases := []struct {
		from, to, want string
	}{
		{"inbox/a.md", "originals/a.pdf", "../originals/a.pdf"},
		{"a.md", "originals/a.pdf", "originals/a.pdf"},
		{"inbox/a.md", "inbox/a.pdf", "a.pdf"},
		{"x/y/a.md", "x/orig/a.pdf", "../orig/a.pdf"},
		{"deep/nested/a.md", "originals/a.pdf", "../../originals/a.pdf"},
	}
	for _, c := range cases {
		if got := relLink(c.from, c.to); got != c.want {
			t.Errorf("relLink(%q,%q)=%q want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestPost_ArchiveLinkInFrontmatter_SubFolder(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	cfg := archiveTestConfig(srv.URL, true)
	cfg.Archive.LinkInFrontmatter = boolPtr(true) // archive.path stays "originals/"
	tg := newTestTarget(t, cfg, nil)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody text",
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: []byte("%PDF"), Extension: ".pdf", MimeType: "application/pdf"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	md := decodeContent(t, puts[1])
	if !strings.Contains(md, "original: ../originals/job-xyz.pdf") {
		t.Fatalf("frontmatter missing original field:\n%s", md)
	}
	if strings.Contains(md, "> Original:") {
		t.Fatalf("body blockquote must not be present:\n%s", md)
	}
}

func TestPost_ArchiveLinkInFrontmatter_SameFolder(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	cfg := archiveTestConfig(srv.URL, true)
	cfg.Archive.Path = "inbox/"
	cfg.Archive.LinkInFrontmatter = boolPtr(true)
	tg := newTestTarget(t, cfg, nil)
	tg.WithHTTPClient(srv.Client())

	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  "# Title\n\nbody",
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: []byte("%PDF"), Extension: ".pdf", MimeType: "application/pdf"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	md := decodeContent(t, puts[1])
	if !strings.Contains(md, "original: job-xyz.pdf") {
		t.Fatalf("frontmatter missing sibling original field:\n%s", md)
	}
}

func TestPost_ArchiveLinkInFrontmatter_ExistingFrontmatter(t *testing.T) {
	var puts []recordedPut
	srv := newArchiveServer(&puts)
	defer srv.Close()

	cfg := archiveTestConfig(srv.URL, true)
	cfg.Archive.LinkInFrontmatter = boolPtr(true)
	tg := newTestTarget(t, cfg, nil)
	tg.WithHTTPClient(srv.Client())

	existingFM := "---\nsource: scan.png\nsource_hash: abc123\ntranscriptionDate: 2024-01-01T00:00:00Z\n---\n\n# Title\n\nbody"
	_, err := tg.Post(context.Background(), targets.TargetRequest{
		JobID:     "job-xyz",
		Markdown:  existingFM,
		Timestamp: time.Now().UTC(),
		Original:  &targets.OriginalDocument{Content: []byte("%PDF"), Extension: ".pdf", MimeType: "application/pdf"},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	md := decodeContent(t, puts[1])
	if !strings.Contains(md, "original: ../originals/job-xyz.pdf") {
		t.Fatalf("original field not injected into existing frontmatter:\n%s", md)
	}
	// Must not create a second frontmatter block — only one pair of ---.
	if strings.Count(md, "---") != 2 {
		t.Fatalf("expected exactly one frontmatter block (2 --- markers), got:\n%s", md)
	}
	if !strings.Contains(md, "source: scan.png") {
		t.Fatalf("existing frontmatter fields must be preserved:\n%s", md)
	}
}

func TestInjectFrontmatterOriginal(t *testing.T) {
	// No existing frontmatter — minimal block created.
	got := injectFrontmatterOriginal("# Title\n\nbody", "orig/a.pdf")
	if !strings.HasPrefix(got, "---\noriginal: orig/a.pdf\n---\n\n# Title") {
		t.Errorf("no-FM case:\n%q", got)
	}

	// Existing frontmatter — field appended inside it.
	fm := "---\nsource: x\n---\n\nbody"
	got = injectFrontmatterOriginal(fm, "orig/a.pdf")
	if !strings.Contains(got, "source: x") || !strings.Contains(got, "original: orig/a.pdf") {
		t.Errorf("existing-FM case field missing:\n%q", got)
	}
	if strings.Count(got, "---") != 2 {
		t.Errorf("existing-FM case should have exactly 2 --- markers:\n%q", got)
	}
	// original field must be inside the frontmatter block (before the second ---)
	closingIdx := strings.Index(got, "\n---\n")
	if closingIdx < 0 {
		closingIdx = strings.Index(got, "\n---")
	}
	if !strings.Contains(got[:closingIdx], "original:") {
		t.Errorf("original field not inside frontmatter block:\n%q", got)
	}
}
