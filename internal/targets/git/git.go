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
	"strconv"
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

	if err := t.ensureRepo(ctx, repoDir); err != nil {
		return targets.TargetResult{}, err
	}

	filename, err := t.renderFilename(req)
	if err != nil {
		return targets.TargetResult{}, err
	}

	_, relPath, err := t.writeContent(repoDir, filename, req.Markdown)
	if err != nil {
		return targets.TargetResult{}, err
	}

	if err := t.stageFile(ctx, repoDir, relPath); err != nil {
		return targets.TargetResult{}, err
	}

	commitMsg, err := t.renderCommitMessage(req)
	if err != nil {
		return targets.TargetResult{}, err
	}
	if err := t.gitCommit(ctx, repoDir, commitMsg); err != nil && !isNothingToCommit(err) {
		return targets.TargetResult{}, fmt.Errorf("git commit: %w", err)
	}

	commitHash, err := t.headHash(ctx, repoDir)
	if err != nil {
		return targets.TargetResult{}, err
	}

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

// ensureRepo ensures a local clone exists and the target branch is prepared.
func (t *Target) ensureRepo(ctx context.Context, repoDir string) error {
	if _, statErr := os.Stat(repoDir); os.IsNotExist(statErr) {
		if err := t.cloneRepo(ctx, repoDir); err != nil {
			return err
		}
		return t.syncRepo(ctx, repoDir)
	}
	return t.ensureBranch(ctx, repoDir)
}

func (t *Target) writeContent(repoDir, filename, markdown string) (fullPath, relPath string, err error) {
	fullPath = filepath.Join(repoDir, filename)
	if err = os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", "", fmt.Errorf("ensure dir: %w", err)
	}
	if err = os.WriteFile(fullPath, []byte(markdown), 0o644); err != nil {
		return "", "", fmt.Errorf("write file: %w", err)
	}
	relPath, relErr := filepath.Rel(repoDir, fullPath)
	if relErr != nil {
		relPath = filename
	}
	return fullPath, relPath, nil
}

func (t *Target) stageFile(ctx context.Context, repoDir, relPath string) error {
	if err := runGit(ctx, repoDir, "add", "--", relPath); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	return nil
}

func (t *Target) gitCommit(ctx context.Context, repoDir, message string) error {
	return runGit(ctx, repoDir,
		"-c", "user.name="+t.cfg.AuthorName,
		"-c", "user.email="+t.cfg.AuthorEmail,
		"commit", "-m", message,
	)
}

func (t *Target) headHash(ctx context.Context, repoDir string) (string, error) {
	hashOut := &bytes.Buffer{}
	if err := runGitWithOutput(ctx, repoDir, hashOut, nil, "rev-parse", "HEAD"); err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(hashOut.String()), nil
}

func (t *Target) cloneRepo(ctx context.Context, repoDir string) error {
	authURL, err := t.authURL()
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}
	// Try cloning the configured branch; if it doesn't exist on the remote, fall back to cloning the default branch.
	if err := runGit(ctx, "", "clone", "--branch", t.cfg.Branch, "--single-branch", "--depth", "1", authURL, repoDir); err != nil {
		_ = os.RemoveAll(repoDir)
		if err2 := runGit(ctx, "", "clone", "--depth", "1", authURL, repoDir); err2 != nil {
			return fmt.Errorf("git clone: %w", err)
		}
	}
	// Set remote back to tokenless URL to avoid storing secret in .git/config
	if err := runGit(ctx, repoDir, "remote", "set-url", common.GitRemoteName, t.cfg.RepoURL); err != nil {
		return fmt.Errorf("git remote set-url: %w", err)
	}
	return nil
}

func (t *Target) ensureBranch(ctx context.Context, repoDir string) error {
	branch := t.cfg.Branch
	if err := runGit(ctx, repoDir, "checkout", branch); err != nil {
		if err2 := runGit(ctx, repoDir, "checkout", "-b", branch); err2 != nil {
			return fmt.Errorf("git checkout %s: %w", branch, err)
		}
	}
	return nil
}

func (t *Target) syncRepo(ctx context.Context, repoDir string) error {
	if err := t.checkoutOrCreateBranch(ctx, repoDir); err != nil {
		return err
	}

	// Temporarily set remote origin to authenticated URL to fetch
	return t.withAuthRemote(ctx, repoDir, func() error {
		_ = runGit(ctx, repoDir, "fetch", common.GitRemoteName, "--prune")

		// If the remote branch exists, attempt to integrate it safely.
		if runGit(ctx, repoDir, "rev-parse", "--verify", "--quiet", t.remoteRefName()) == nil {
			behind, ahead, ok := t.aheadBehind(ctx, repoDir)
			if ok {
				switch {
				case behind > 0 && ahead == 0:
					if err := runGit(ctx, repoDir, "merge", "--ff-only", t.remoteBranchQualified()); err != nil {
						return fmt.Errorf("git merge --ff-only %s: %w", t.remoteBranchQualified(), err)
					}
				case behind > 0 && ahead > 0:
					if err := runGit(ctx, repoDir, "rebase", t.remoteBranchQualified()); err != nil {
						_ = runGit(ctx, repoDir, "rebase", "--abort")
						return fmt.Errorf("git rebase %s: %w", t.remoteBranchQualified(), err)
					}
				default:
					// up to date or only ahead, nothing to do
				}
			} else {
				_ = runGit(ctx, repoDir, "merge", "--ff-only", t.remoteBranchQualified())
			}
		}
		return nil
	})
}

func (t *Target) pushRepo(ctx context.Context, repoDir string) error {
	authURL, err := t.authURL()
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}

	// First attempt: push directly without pulling
	if err := runGit(ctx, repoDir, "push", authURL, t.cfg.Branch); err == nil {
		return nil
	} else if isNonFastForwardPush(err) {
		// Recovery path: fetch + rebase (or merge) then push again
		recovery := func() error {
			_ = runGit(ctx, repoDir, "fetch", common.GitRemoteName, "--prune")
			if rbErr := runGit(ctx, repoDir, "rebase", t.remoteBranchQualified()); rbErr != nil {
				_ = runGit(ctx, repoDir, "rebase", "--abort")
				if mgErr := runGit(ctx, repoDir, "merge", "--no-edit", t.remoteBranchQualified()); mgErr != nil {
					return fmt.Errorf("push recovery failed (rebase and merge): rebase=%v, merge=%v", rbErr, mgErr)
				}
			}
			if perr := runGit(ctx, repoDir, "push", authURL, t.cfg.Branch); perr != nil {
				return fmt.Errorf("git push after recovery: %w", perr)
			}
			return nil
		}
		return t.withAuthRemote(ctx, repoDir, recovery)
	} else {
		return fmt.Errorf("git push: %w", err)
	}
}

func (t *Target) checkoutOrCreateBranch(ctx context.Context, repoDir string) error {
	branch := t.cfg.Branch
	if err := runGit(ctx, repoDir, "checkout", branch); err != nil {
		_ = runGit(ctx, repoDir, "fetch", common.GitRemoteName)
		if err2 := runGit(ctx, repoDir, "checkout", "-b", branch, "--track", t.remoteBranchQualified()); err2 != nil {
			if err3 := runGit(ctx, repoDir, "checkout", "-b", branch); err3 != nil {
				return fmt.Errorf("git checkout %s: %w", branch, err)
			}
		}
	}
	return nil
}

func (t *Target) aheadBehind(ctx context.Context, repoDir string) (behind, ahead int, ok bool) {
	abOut := &bytes.Buffer{}
	if err := runGitWithOutput(ctx, repoDir, abOut, nil, "rev-list", "--left-right", "--count", fmt.Sprintf("%s...%s", t.remoteBranchQualified(), "HEAD")); err == nil {
		fields := strings.Fields(strings.TrimSpace(abOut.String()))
		if len(fields) >= 2 {
			behind, _ = strconv.Atoi(fields[0])
			ahead, _ = strconv.Atoi(fields[1])
			return behind, ahead, true
		}
	}
	return 0, 0, false
}

func (t *Target) renderFilename(req targets.TargetRequest) (string, error) {
	data := t.templateData(req)
	name, err := t.render(t.cfg.FilenameTemplate, "{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md", "filename", data)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = fmt.Sprintf("%s-%s.md", req.Timestamp.Format("20060102-150405"), req.JobID)
	}
	if t.cfg.BasePath != "" {
		name = filepath.Join(t.cfg.BasePath, name)
	}
	return name, nil
}

func (t *Target) renderCommitMessage(req targets.TargetRequest) (string, error) {
	data := t.templateData(req)
	msg, err := t.render(t.cfg.CommitMessageTemplate, "Add transcription {{ .JobID }}", "commit", data)
	if err != nil {
		return "", err
	}
	if msg == "" {
		msg = "Add transcription"
	}
	return msg, nil
}

func (t *Target) templateData(req targets.TargetRequest) map[string]any {
	return map[string]any{
		"JobID":          req.JobID,
		"Timestamp":      req.Timestamp,
		"SuggestedTitle": req.SuggestedTitle,
		"Metadata":       req.Metadata,
	}
}

func (t *Target) render(tplStr, defaultTpl, name string, data map[string]any) (string, error) {
	s := strings.TrimSpace(tplStr)
	if s == "" {
		s = defaultTpl
	}
	tpl, err := template.New(name).Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func (t *Target) repoCacheDir() string {
	// Derive a stable directory name from repo URL and branch.
	safeURL := sanitizePath(t.cfg.RepoURL)
	safeBranch := sanitizePath(t.cfg.Branch)
	return filepath.Join(t.cacheRoot, fmt.Sprintf("%s_%s", safeURL, safeBranch))
}

func (t *Target) authURL() (string, error) {
	return withAuth(t.cfg.RepoURL, t.cfg.Auth.Username, t.cfg.Auth.Token)
}

func (t *Target) remoteRefName() string {
	return fmt.Sprintf("refs/remotes/%s/%s", common.GitRemoteName, t.cfg.Branch)
}

func (t *Target) remoteBranchQualified() string {
	return fmt.Sprintf("%s/%s", common.GitRemoteName, t.cfg.Branch)
}

func (t *Target) withAuthRemote(ctx context.Context, repoDir string, fn func() error) error {
	authURL, err := t.authURL()
	if err != nil {
		return fmt.Errorf("auth url: %w", err)
	}
	if err := runGit(ctx, repoDir, "remote", "set-url", common.GitRemoteName, authURL); err != nil {
		return fmt.Errorf("set auth remote: %w", err)
	}
	defer func() {
		_ = runGit(context.Background(), repoDir, "remote", "set-url", common.GitRemoteName, t.cfg.RepoURL)
	}()
	return fn()
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
	return strings.Contains(strings.ToLower(msg), "nothing to commit")
}

func isNonFastForwardPush(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "non-fast-forward"):
		return true
	case strings.Contains(msg, "tip of your current branch is behind"):
		return true
	case strings.Contains(msg, "failed to push some refs") && strings.Contains(msg, "rejected"):
		return true
	case strings.Contains(msg, "fetch first"):
		return true
	default:
		return false
	}
}

// Safety check to ensure git is available (optional invocation before use).
func ensureGitAvailable() error {
	if _, err := exec.LookPath(common.GitExecutable); err != nil {
		return errors.New("git executable not found in PATH")
	}
	return nil
}