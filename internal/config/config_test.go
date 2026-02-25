package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestParseByteSize_K8sAndCommonUnits(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"1024", 1024},
		{"1Ki", 1024},
		{"1KiB", 1024},
		{"2Mi", 2 * 1024 * 1024},
		{"2MiB", 2 * 1024 * 1024},
		{"3Gi", 3 * 1024 * 1024 * 1024},
		{"3GiB", 3 * 1024 * 1024 * 1024},
		{"10KB", 10 * 1000},
		{"10MB", 10 * 1000 * 1000},
		{"2GB", 2 * 1000 * 1000 * 1000},
	}
	for _, c := range cases {
		got, err := ParseByteSize(c.in)
		if err != nil {
			t.Fatalf("ParseByteSize(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseByteSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	// invalid
	if _, err := ParseByteSize("bad"); err == nil {
		t.Fatalf("expected error for invalid unit")
	}
}

func TestNormalizePathPrefix(t *testing.T) {
	if got := normalizePathPrefix(`foo\bar`); got != "foo/bar/" && got != "foo/bar" {
		t.Fatalf("normalizePathPrefix backslashes = %q", got)
	}
	if got := normalizePathPrefix("./docs"); got != "docs/" && got != "docs" {
		t.Fatalf("normalizePathPrefix removes leading ./ = %q", got)
	}
}

func TestLoad_WithEnvAndDefaults_SingleTarget(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Use env expansion for token
	t.Setenv("GIT_TOKEN", "secret123")

	yaml := `
server:
  address: ":0"
  readTimeout: 1s
  writeTimeout: 2s
  idleTimeout: 3s
  maxUploadSize: 1Mi
  workerCount: 1
  storageDir: "` + escapeBackslashes(dir) + `"
  apiKey: "key123"
  databasePath: ""
  shutdownGrace: 5s
  callbackRetries: 2
  callbackBackoff: 1s

llm:
  provider: "mock"
  mock:
    delay: 0s
    prefix: "prefix"

target:
  type: "github"
  name: "docs"
  repoOwner: "example"
  repoName: "repo"
  branch: "main"
  basePath: "inbox/"
  filenameTemplate: "{{ .JobID }}.md"
  commitMessageTemplate: "Add {{ .JobID }}"
  authorName: "Bot"
  authorEmail: "bot@example.com"
  auth:
    token: "${GIT_TOKEN}"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	// Server assertions
	if cfg.Server.Addr != ":0" {
		t.Fatalf("address = %q", cfg.Server.Addr)
	}
	if cfg.Server.ReadTimeout != 1*time.Second || cfg.Server.WriteTimeout != 2*time.Second || cfg.Server.IdleTimeout != 3*time.Second {
		t.Fatalf("timeouts not parsed correctly")
	}
	if uint64(cfg.Server.MaxUploadSize) != 1024*1024 {
		t.Fatalf("maxUploadSize not parsed: %d", cfg.Server.MaxUploadSize)
	}
	if cfg.Server.StorageDir != dir {
		t.Fatalf("storageDir = %q", cfg.Server.StorageDir)
	}
	if cfg.Server.APIKey != "key123" {
		t.Fatalf("apiKey mismatch")
	}
	if cfg.Server.DatabasePath == "" {
		t.Fatalf("databasePath should be defaulted to storageDir/gostwriter.db")
	}

	// LLM
	if cfg.LLM.Provider != "mock" || cfg.LLM.Mock.Prefix != "prefix" {
		t.Fatalf("llm config mismatch")
	}

	// Target
	if cfg.Target.Type != "github" || cfg.Target.Name != "docs" {
		t.Fatalf("target type/name mismatch")
	}
	if cfg.Target.GitHub.RepoOwner != "example" || cfg.Target.GitHub.RepoName != "repo" || cfg.Target.GitHub.Branch != "main" {
		t.Fatalf("github target repo/branch mismatch")
	}
	if cfg.Target.GitHub.Auth.Token != "secret123" {
		t.Fatalf("env expansion for token failed")
	}

	// Validate database path is under storageDir
	matched, _ := regexp.MatchString(`gostwriter\.db$`, cfg.Server.DatabasePath)
	if !matched {
		t.Fatalf("databasePath should end with gostwriter.db, got %s", cfg.Server.DatabasePath)
	}
}

func escapeBackslashes(p string) string {
	// On Windows, YAML literal may require escaping backslashes
	return strings.ReplaceAll(p, `\`, `\\`)
}
