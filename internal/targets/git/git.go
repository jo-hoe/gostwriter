package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/jo-hoe/gostwriter/internal/common"
	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

// Target implements a Git markdown post target using the git CLI via os/exec
// to comply with the "stdlib only" requirement (except YAML and SQLite).
type Target struct {
	name      string
	cfg       appcfg.GitTargetConfig
	cacheRoot string // base dir for cached clones
}

// New creates a Git Target.
// cacheRoot is the directory where clones will be cached (e.g., storage_dir/repos).
func New(name string, cfg appcfg.GitTargetConfig, cacheRoot string) (*Target, error) {
	if strings.ToLower(cfg.Auth.Type) != "basic" {
		return nil, fmt.Errorf("git auth type %q not supported", cfg.Auth.Type)
	}
	if cfg.CloneCacheDir != "" {
		cacheRoot = cfg.CloneCacheDir
	}
	if cacheRoot == "" {
		return nil, fmt.Errorf("cacheRoot must not be empty")
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, fmt.Errorf("ensure cache root: %w", err)
	}
	return &Target{
		name:      name,
		cfg:       cfg,
		cacheRoot: cacheRoot,
	}, nil
}

func (t *Target) Name() string { return t.name }

func (t *Target) Post(ctx context.Context, req targets.TargetRequest) (targets.TargetResult, error) {
	repoDir := t.repoCacheDir()
	if _, statErr := os.Stat(repoDir); os.IsNotExist(statErr) {
		if err := t.cloneRepo(ctx, repoDir); err != nil {
			return targets.TargetResult{}, err
		}
	} else {
		if err := t.syncRepo(ctx, repoDir); err != nil {
			return targets.TargetResult{}, err
		}
	}

	// Prepare path and content
	filename, err := t.renderFilename(req)
	if err != nil {
		return targets.TargetResult{}, err
	}
	fullPath := filepath.Join(repoDir, filename)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return targets.TargetResult{}, fmt.Errorf("ensure dir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(req.Markdown), 0o644); err != nil {
		return targets.TargetResult{}, fmt.Errorf("write file: %w", err)
	}

	relPath, err := filepath.Rel(repoDir, fullPath)
	if err != nil {
		relPath = filename
	}

	// git add
	if err := runGit(ctx, repoDir, "add", "--", relPath); err != nil {
		return targets.TargetResult{}, fmt.Errorf("git add: %w", err)
	}

	// commit
	commitMsg, err := t.renderCommitMessage(req)
	if err != nil {
		return targets.TargetResult{}, err
	}
	commitErr := runGit(ctx, repoDir, "-c", "user.name="+t.cfg.AuthorName, "-c", "user.email="+t.cfg.AuthorEmail, "commit", "-m", commitMsg)
	if commitErr != nil {
		// If nothing to commit, bail out gracefully (but then we cannot push new content)
		if isNothingToCommit(commitErr) {
			// Still return success with current HEAD hash
		} else {
			return targets.TargetResult{}, fmt.Errorf("git commit: %w", commitErr)
		}
	}

	// Read HEAD hash
	hashOut := &bytes.Buffer{}
	if err := runGitWithOutput(ctx, repoDir, hashOut, nil, "rev-parse", "HEAD"); err != nil {
		return targets.TargetResult{}, fmt.Errorf("git rev-parse: %w", err)
	}
	commitHash := strings.TrimSpace(hashOut.String())

	// push
	if err := t.pushRepo(ctx, repoDir); err != nil {
		return targets.TargetResult{}, err
	}

	loc := fmt.Sprintf("git:%s@%s:%s", t.cfg.RepoURL, t.cfg.Branch, filepath.ToSlash(filename))
	return targets.TargetResult{
		TargetName: t.name,
		Location:   loc,
		Commit:     commitHash,
	}, nil
}

func (t *Target) cloneRepo(ctx context.Context, repoDir string) error {
	authURL, err := withAuth(t.cfg.RepoURL, t.cfg.Auth.Username, t.cfg.Auth.Token)
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}
	// git clone --branch <branch> --single-branch --depth 1 <authURL> <repoDir>
	if err := runGit(ctx, "", "clone", "--branch", t.cfg.Branch, "--single-branch", "--depth", "1", authURL, repoDir); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	// Set remote back to tokenless URL to avoid storing secret in .git/config
	if err := runGit(ctx, repoDir, "remote", "set-url", common.GitRemoteName, t.cfg.RepoURL); err != nil {
		// Not fatal, but warn
		return fmt.Errorf("git remote set-url: %w", err)
	}
	return nil
}

func (t *Target) syncRepo(ctx context.Context, repoDir string) error {
	branch := t.cfg.Branch
	// Ensure branch is checked out
	if err := runGit(ctx, repoDir, "checkout", branch); err != nil {
		// Try to create tracking branch from origin
		_ = runGit(ctx, repoDir, "fetch", common.GitRemoteName)
		if err2 := runGit(ctx, repoDir, "checkout", "-b", branch, "--track", fmt.Sprintf("%s/%s", common.GitRemoteName, branch)); err2 != nil {
			return fmt.Errorf("git checkout %s: %w", branch, err)
		}
	}

	// Temporarily set remote origin to authenticated URL to fetch
	authURL, err := withAuth(t.cfg.RepoURL, t.cfg.Auth.Username, t.cfg.Auth.Token)
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}
	if err := runGit(ctx, repoDir, "remote", "set-url", common.GitRemoteName, authURL); err != nil {
		return fmt.Errorf("set auth remote: %w", err)
	}
	defer func() {
		_ = runGit(context.Background(), repoDir, "remote", "set-url", common.GitRemoteName, t.cfg.RepoURL)
	}()

	// Fetch and hard reset to origin/branch to ensure clean state
	_ = runGit(ctx, repoDir, "fetch", common.GitRemoteName, "--depth", "1")
	if err := runGit(ctx, repoDir, "reset", "--hard", fmt.Sprintf("%s/%s", common.GitRemoteName, branch)); err != nil {
		return fmt.Errorf("git reset --hard origin/%s: %w", branch, err)
	}
	return nil
}

func (t *Target) pushRepo(ctx context.Context, repoDir string) error {
	authURL, err := withAuth(t.cfg.RepoURL, t.cfg.Auth.Username, t.cfg.Auth.Token)
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}
	// Push using URL directly so we don't persist the token in .git/config
	// Command: git push <authURL> <branch>
	if err := runGit(ctx, repoDir, "push", authURL, t.cfg.Branch); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

func (t *Target) renderFilename(req targets.TargetRequest) (string, error) {
	tplStr := strings.TrimSpace(t.cfg.FilenameTemplate)
	if tplStr == "" {
		tplStr = "{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md"
	}
	var buf bytes.Buffer
	data := map[string]any{
		"JobID":          req.JobID,
		"Timestamp":      req.Timestamp,
		"SuggestedTitle": req.SuggestedTitle,
		"Metadata":       req.Metadata,
	}
	if err := template.Must(template.New("filename").Parse(tplStr)).Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render filename: %w", err)
	}
	name := strings.TrimSpace(buf.String())
	if name == "" {
		name = fmt.Sprintf("%s-%s.md", req.Timestamp.Format("20060102-150405"), req.JobID)
	}
	if t.cfg.BasePath != "" {
		name = filepath.Join(t.cfg.BasePath, name)
	}
	return name, nil
}

func (t *Target) renderCommitMessage(req targets.TargetRequest) (string, error) {
	tplStr := strings.TrimSpace(t.cfg.CommitMessageTemplate)
	if tplStr == "" {
		tplStr = "Add transcription {{ .JobID }}"
	}
	var buf bytes.Buffer
	data := map[string]any{
		"JobID":          req.JobID,
		"Timestamp":      req.Timestamp,
		"SuggestedTitle": req.SuggestedTitle,
		"Metadata":       req.Metadata,
	}
	if err := template.Must(template.New("commit").Parse(tplStr)).Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render commit message: %w", err)
	}
	msg := strings.TrimSpace(buf.String())
	if msg == "" {
		msg = "Add transcription"
	}
	return msg, nil
}

func (t *Target) repoCacheDir() string {
	// Derive a stable directory name from repo URL and branch.
	safeURL := sanitizePath(t.cfg.RepoURL)
	safeBranch := sanitizePath(t.cfg.Branch)
	return filepath.Join(t.cacheRoot, fmt.Sprintf("%s_%s", safeURL, safeBranch))
}

func sanitizePath(s string) string {
	s = strings.ReplaceAll(s, "://", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, ":", "_")
	return s
}

func withAuth(rawURL, username, token string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	// Avoid placing '@' or ':' in username/token improperly
	u.User = url.UserPassword(username, token)
	return u.String(), nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	return runGitWithOutput(ctx, dir, nil, nil, args...)
}

func runGitWithOutput(ctx context.Context, dir string, stdout, stderr *bytes.Buffer, args ...string) error {
	cmd := exec.CommandContext(ctx, common.GitExecutable, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	// For convenience, if no buffers provided, capture stderr to include in error
	var errBuf bytes.Buffer
	if stdout == nil {
		cmd.Stdout = nil
	}
	if stderr == nil {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func isNothingToCommit(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Match common git output when nothing to commit
	return strings.Contains(strings.ToLower(msg), "nothing to commit")
}

// Safety check to ensure git is available (optional invocation before use).
func ensureGitAvailable() error {
	if _, err := exec.LookPath(common.GitExecutable); err != nil {
		return errors.New("git executable not found in PATH")
	}
	return nil
}
