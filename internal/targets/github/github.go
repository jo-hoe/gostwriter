package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/naming"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

// Target implements a GitHub markdown post target using the GitHub REST API
// to create file contents without cloning the repository.
type Target struct {
	name  string
	cfg   appcfg.GitHubTargetConfig
	http  *http.Client
	namer naming.FileNamer
}

// New creates a GitHub Target with the provided config.
// Uses http.DefaultClient unless a custom client is provided via WithHTTPClient.
func New(name string, cfg appcfg.GitHubTargetConfig, namer naming.FileNamer) (*Target, error) {
	if strings.TrimSpace(cfg.Auth.Token) == "" {
		return nil, fmt.Errorf("github token must not be empty")
	}
	if strings.TrimSpace(cfg.RepositoryOwner) == "" || strings.TrimSpace(cfg.RepositoryName) == "" {
		return nil, fmt.Errorf("repository owner/name must not be empty")
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		return nil, fmt.Errorf("branch must not be empty")
	}
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		cfg.APIBaseURL = "https://api.github.com"
	}
	return &Target{
		name:  name,
		cfg:   cfg,
		http:  http.DefaultClient,
		namer: namer,
	}, nil
}

// WithHTTPClient allows tests to inject a custom HTTP client (e.g., pointing to httptest.Server).
func (t *Target) WithHTTPClient(c *http.Client) *Target {
	t.http = c
	return t
}

func (t *Target) Name() string { return t.name }

func (t *Target) Post(ctx context.Context, req targets.TargetRequest) (targets.TargetResult, error) {
	// Resolve filename via the injected naming strategy.
	nr := naming.NamingRequest{
		JobID:          req.JobID,
		Timestamp:      req.Timestamp,
		SuggestedTitle: req.SuggestedTitle,
		Metadata:       req.Metadata,
		BasePath:       t.cfg.BasePath,
		Extension:      ".md",
	}
	var filename string
	var err error
	if cn, ok := t.namer.(*naming.CollisionNamer); ok {
		filename, err = cn.NameWithContext(ctx, nr)
	} else {
		filename, err = t.namer.Name(nr)
	}
	if err != nil {
		return targets.TargetResult{}, err
	}
	mdPath := filepath.ToSlash(filename)

	commitMsg, err := t.renderCommitMessage(req)
	if err != nil {
		return targets.TargetResult{}, err
	}

	markdown := req.Markdown

	// Optionally archive the original document first, then link to it.
	if t.cfg.Archive.Enabled && req.Original != nil {
		originalPath := swapDirExt(mdPath, t.cfg.Archive.Path, req.Original.Extension)
		if _, err := t.putContents(ctx, originalPath, req.Original.Content, commitMsg); err != nil {
			return targets.TargetResult{}, fmt.Errorf("archive original: %w", err)
		}
		markdown = injectOriginalLink(markdown, relLink(mdPath, originalPath))
	}

	out, err := t.putContents(ctx, mdPath, []byte(markdown), commitMsg)
	if err != nil {
		return targets.TargetResult{}, err
	}

	loc := fmt.Sprintf("github:%s/%s@%s:%s", t.cfg.RepositoryOwner, t.cfg.RepositoryName, t.cfg.Branch, mdPath)
	return targets.TargetResult{
		TargetName: t.name,
		Location:   loc,
		Commit:     out.Commit.SHA,
	}, nil
}

// putContents creates a file in the repository via the GitHub Contents API.
func (t *Target) putContents(ctx context.Context, repoPath string, content []byte, commitMsg string) (createFileResponse, error) {
	payload := createFilePayload{
		Message: commitMsg,
		Content: base64.StdEncoding.EncodeToString(content),
		Branch:  t.cfg.Branch,
		Committer: &gitIdentity{
			Name:  t.cfg.AuthorName,
			Email: t.cfg.AuthorEmail,
		},
		Author: &gitIdentity{
			Name:  t.cfg.AuthorName,
			Email: t.cfg.AuthorEmail,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return createFileResponse{}, fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", strings.TrimRight(t.cfg.APIBaseURL, "/"), t.cfg.RepositoryOwner, t.cfg.RepositoryName, repoPath)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return createFileResponse{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+t.cfg.Auth.Token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.http.Do(httpReq)
	if err != nil {
		return createFileResponse{}, fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Successful create returns 201; update returns 200. We expect create.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var apiErr apiError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Message != "" {
			return createFileResponse{}, fmt.Errorf("github api: status %d: %s", resp.StatusCode, apiErr.Message)
		}
		return createFileResponse{}, fmt.Errorf("github api: status %d", resp.StatusCode)
	}

	var out createFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return createFileResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// swapDirExt derives a sibling path from a repo path by replacing its directory
// prefix with newDir and its extension with newExt. All paths are slash-separated.
func swapDirExt(repoPath, newDir, newExt string) string {
	base := path.Base(repoPath)
	stem := strings.TrimSuffix(base, path.Ext(base))
	return path.Join(newDir, stem+newExt)
}

// relLink returns a relative link from fromFile to toFile, both repo-root-relative
// slash-separated paths, usable as a Markdown link target.
func relLink(fromFile, toFile string) string {
	fromDir := path.Dir(fromFile)
	if fromDir == "." {
		fromDir = ""
	}
	fromParts := splitPath(fromDir)
	toParts := splitPath(toFile)

	i := 0
	for i < len(fromParts) && i < len(toParts)-1 && fromParts[i] == toParts[i] {
		i++
	}

	var out []string
	for range fromParts[i:] {
		out = append(out, "..")
	}
	out = append(out, toParts[i:]...)
	return path.Join(out...)
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// injectOriginalLink inserts a blockquote link to the original document. When the
// markdown begins with an H1 heading, the link is placed just below it; otherwise
// it is prepended.
func injectOriginalLink(md, link string) string {
	blockquote := fmt.Sprintf("> Original: [document](%s)", link)
	trimmed := strings.TrimLeft(md, "\n")
	if strings.HasPrefix(trimmed, "# ") {
		if idx := strings.Index(trimmed, "\n"); idx >= 0 {
			heading := trimmed[:idx]
			rest := strings.TrimLeft(trimmed[idx+1:], "\n")
			return fmt.Sprintf("%s\n\n%s\n\n%s", heading, blockquote, rest)
		}
		return fmt.Sprintf("%s\n\n%s", trimmed, blockquote)
	}
	return fmt.Sprintf("%s\n\n%s", blockquote, trimmed)
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

// Payload and response structures

type gitIdentity struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type createFilePayload struct {
	Message   string       `json:"message"`
	Content   string       `json:"content"` // base64
	Branch    string       `json:"branch,omitempty"`
	Committer *gitIdentity `json:"committer,omitempty"`
	Author    *gitIdentity `json:"author,omitempty"`
}

type createFileResponse struct {
	Content struct {
		Path string `json:"path"`
	} `json:"content"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type apiError struct {
	Message string `json:"message"`
}
