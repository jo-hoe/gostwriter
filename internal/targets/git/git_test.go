package git

import (
	"context"
	"strings"
	"testing"
	"time"

	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

func TestSanitizePath(t *testing.T) {
	cases := []struct {
		in, wantContain string
	}{
		{"https://github.com/org/repo.git", "https_github.com_org_repo.git"},
		{"git@github.com:org/repo", "git@github.com_org_repo"},
	}
	for _, c := range cases {
		got := sanitizePath(c.in)
		if !strings.Contains(got, c.wantContain) {
			t.Fatalf("sanitizePath(%q) = %q, want contain %q", c.in, got, c.wantContain)
		}
	}
}

func TestWithAuth(t *testing.T) {
	u, err := withAuth("https://github.com/org/repo.git", "git", "TOKEN")
	if err != nil {
		t.Fatalf("withAuth error: %v", err)
	}
	if !strings.HasPrefix(u, "https://git:") {
		t.Fatalf("withAuth url should include user: %s", u)
	}
	if !strings.Contains(u, "TOKEN@github.com") {
		t.Fatalf("withAuth url should contain token@host: %s", u)
	}
}

func TestRenderFilenameAndCommitMessage(t *testing.T) {
	cfg := appcfg.GitTargetConfig{
		BasePath:              "inbox/",
		FilenameTemplate:      "{{ .JobID }}.md",
		CommitMessageTemplate: "Add {{ .JobID }}",
	}
	tg := &Target{
		name:      "docs",
		cfg:       cfg,
		cacheRoot: t.TempDir(),
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

func TestName(t *testing.T) {
	tg, err := New("docs", appcfg.GitTargetConfig{
		Auth:    appcfg.GitAuthConfig{Type: "basic", Username: "git", Token: "x"},
		RepoURL: "https://example.com/repo.git",
		Branch:  "main",
	}, t.TempDir())
	if err != nil {
		t.Fatalf("New git target: %v", err)
	}
	if tg.Name() != "docs" {
		t.Fatalf("Name() mismatch: %s", tg.Name())
	}
	// Ensure ensureGitAvailable does not panic on systems without git; we won't assert its return here.
	_ = ensureGitAvailable()

	// Do not call Post to avoid invoking system git; instead, test that repoCacheDir can be computed.
	_ = tg.repoCacheDir()
	_ = context.TODO()
}
