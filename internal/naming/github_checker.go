package naming

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// GitHubPathChecker checks whether a path already exists in a GitHub repository
// by issuing a GET to the Contents API (200 = exists, 404 = free).
type GitHubPathChecker struct {
	httpClient *http.Client
	apiBase    string
	owner      string
	repo       string
	branch     string
	token      string
}

func NewGitHubPathChecker(httpClient *http.Client, apiBase, owner, repo, branch, token string) *GitHubPathChecker {
	return &GitHubPathChecker{
		httpClient: httpClient,
		apiBase:    strings.TrimRight(apiBase, "/"),
		owner:      owner,
		repo:       repo,
		branch:     branch,
		token:      token,
	}
}

func (c *GitHubPathChecker) Exists(ctx context.Context, repoPath string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		c.apiBase, c.owner, c.repo, repoPath, c.branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected status %d checking path %q", resp.StatusCode, repoPath)
	}
}
