package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	appcfg "github.com/jo-hoe/gostwriter/internal/config"
	"github.com/jo-hoe/gostwriter/internal/targets"
)

// Target implements a GitHub markdown post target using the GitHub REST API
// to create file contents without cloning the repository.
type Target struct {
	name string
	cfg  appcfg.GitHubTargetConfig
	http *http.Client
}

// New creates a GitHub Target with the provided config.
// Uses http.DefaultClient unless a custom client is provided via WithHTTPClient.
func New(name string, cfg appcfg.GitHubTargetConfig) (*Target, error) {
	if strings.TrimSpace(cfg.Auth.Token) == "" {
		return nil, fmt.Errorf("github token must not be empty")
	}
	if strings.TrimSpace(cfg.RepoOwner) == "" || strings.TrimSpace(cfg.RepoName) == "" {
		return nil, fmt.Errorf("repo owner/name must not be empty")
	}
	if strings.TrimSpace(cfg.Branch) == "" {
		return nil, fmt.Errorf("branch must not be empty")
	}
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		cfg.APIBaseURL = "https://api.github.com"
	}
	return &Target{
		name: name,
		cfg:  cfg,
		http: http.DefaultClient,
	}, nil
}

// WithHTTPClient allows tests to inject a custom HTTP client (e.g., pointing to httptest.Server).
func (t *Target) WithHTTPClient(c *http.Client) *Target {
	t.http = c
	return t
}

func (t *Target) Name() string { return t.name }

func (t *Target) Post(ctx context.Context, req targets.TargetRequest) (targets.TargetResult, error) {
	// Render filename/path
	filename, err := t.renderFilename(req)
	if err != nil {
		return targets.TargetResult{}, err
	}
	path := filepath.ToSlash(filename)

	// Render commit message
	commitMsg, err := t.renderCommitMessage(req)
	if err != nil {
		return targets.TargetResult{}, err
	}

	// Build payload per GitHub API: Create or update file contents
	// https://docs.github.com/en/rest/repos/contents?apiVersion=2022-11-28#create-or-update-file-contents
	payload := createFilePayload{
		Message: commitMsg,
		Content: base64.StdEncoding.EncodeToString([]byte(req.Markdown)),
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

	// Marshal JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return targets.TargetResult{}, fmt.Errorf("marshal payload: %w", err)
	}

	// Construct URL: {apiBase}/repos/{owner}/{repo}/contents/{path}
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", strings.TrimRight(t.cfg.APIBaseURL, "/"), t.cfg.RepoOwner, t.cfg.RepoName, path)

	// Prepare request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return targets.TargetResult{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+t.cfg.Auth.Token)
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	// Use the API version mentioned in docs
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	httpReq.Header.Set("Content-Type", "application/json")

	// Perform request
	resp, err := t.http.Do(httpReq)
	if err != nil {
		return targets.TargetResult{}, fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Successful create returns 201; update returns 200. We expect create.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		// Attempt to read error details
		var apiErr apiError
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Message != "" {
			return targets.TargetResult{}, fmt.Errorf("github api: status %d: %s", resp.StatusCode, apiErr.Message)
		}
		return targets.TargetResult{}, fmt.Errorf("github api: status %d", resp.StatusCode)
	}

	var out createFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return targets.TargetResult{}, fmt.Errorf("decode response: %w", err)
	}

	commitSHA := ""
	if out.Commit.SHA != "" {
		commitSHA = out.Commit.SHA
	}

	loc := fmt.Sprintf("github:%s/%s@%s:%s", t.cfg.RepoOwner, t.cfg.RepoName, t.cfg.Branch, path)
	return targets.TargetResult{
		TargetName: t.name,
		Location:   loc,
		Commit:     commitSHA,
	}, nil
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

// Ensure UTC timestamps in templates behave similarly to git target expectations.
func nowUTC() time.Time { return time.Now().UTC() }
