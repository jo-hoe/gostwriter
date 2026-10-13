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
  github:
    enabled: true
    repositoryOwner: "example"
    repositoryName: "repo"
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
	if !cfg.Target.GitHub.Enabled {
		t.Fatalf("github target not enabled")
	}
	if cfg.Target.GitHub.RepositoryOwner != "example" || cfg.Target.GitHub.RepositoryName != "repo" || cfg.Target.GitHub.Branch != "main" {
		t.Fatalf("github target repository/branch mismatch")
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

func makeMinimalGitHubYAML(dir, extra string) string {
	return `
server:
  address: ":0"
  storageDir: "` + escapeBackslashes(dir) + `"
llm:
  provider: "mock"
target:
  github:
    enabled: true
    repositoryOwner: "org"
    repositoryName: "repo"
    branch: "main"
    filenameTemplate: "{{ .JobID }}.md"
    commitMessageTemplate: "Add {{ .JobID }}"
    auth:
      token: "tok"
` + extra
}

func TestLoad_NamingStrategyDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.NamingStrategy != "template" {
		t.Fatalf("expected default namingStrategy=template, got %q", cfg.Target.GitHub.NamingStrategy)
	}
}

func TestLoad_NewGitHubFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    namingStrategy: "ai"
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.NamingStrategy != "ai" {
		t.Fatalf("expected namingStrategy=ai, got %q", cfg.Target.GitHub.NamingStrategy)
	}
}

func TestLoad_NamingStrategyValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    namingStrategy: "invalid"
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid namingStrategy")
	}
	if !strings.Contains(err.Error(), "namingStrategy") {
		t.Fatalf("error should mention namingStrategy, got: %v", err)
	}
}

func TestLoad_NamingStrategyAIAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    namingStrategy: "ai"
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.NamingStrategy != "ai" {
		t.Fatalf("expected namingStrategy=ai, got %q", cfg.Target.GitHub.NamingStrategy)
	}
}

func TestLoad_NamingStrategyAIAllowsMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  address: ":0"
  storageDir: "` + escapeBackslashes(dir) + `"
llm:
  provider: "mock"
target:
  github:
    enabled: true
    repositoryOwner: "org"
    repositoryName: "repo"
    branch: "main"
    namingStrategy: "ai"
    commitMessageTemplate: "Add {{ .JobID }}"
    auth:
      token: "tok"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("ai strategy should not require filenameTemplate: %v", err)
	}
}

func TestLoad_NonAIStrategyRequiresTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  address: ":0"
  storageDir: "` + escapeBackslashes(dir) + `"
llm:
  provider: "mock"
target:
  github:
    enabled: true
    repositoryOwner: "org"
    repositoryName: "repo"
    branch: "main"
    namingStrategy: "template"
    commitMessageTemplate: "Add {{ .JobID }}"
    auth:
      token: "tok"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error: template strategy requires filenameTemplate")
	}
	if !strings.Contains(err.Error(), "filenameTemplate") {
		t.Fatalf("error should mention filenameTemplate, got: %v", err)
	}
}

func TestLoad_ArchiveDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.Archive.Enabled {
		t.Fatal("archive should be disabled by default")
	}
}

func TestLoad_ArchiveEnabledDefaultsToBasePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    basePath: "inbox/"
    archive:
      enabled: true
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.Archive.Path != "inbox/" {
		t.Fatalf("expected archive path to default to basePath inbox/, got %q", cfg.Target.GitHub.Archive.Path)
	}
}

func TestLoad_ArchivePathNormalized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    archive:
      enabled: true
      path: "docs"
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.Archive.Path != "docs/" {
		t.Fatalf("expected normalized archive path docs/, got %q", cfg.Target.GitHub.Archive.Path)
	}
}

func TestLoad_ArchivePathEqualsBasePathAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    basePath: "inbox/"
    archive:
      enabled: true
      path: "inbox/"
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("archive.path equal to basePath should be accepted: %v", err)
	}
	if cfg.Target.GitHub.Archive.Path != "inbox/" {
		t.Fatalf("expected archive path inbox/, got %q", cfg.Target.GitHub.Archive.Path)
	}
}

func TestLoad_ArchiveLinkInFrontmatterDefaultsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    archive:
      enabled: true
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.Archive.LinkInFrontmatter == nil || !*cfg.Target.GitHub.Archive.LinkInFrontmatter {
		t.Fatal("archive.linkInFrontmatter should default to true when archive is enabled")
	}
}

func TestLoad_ArchiveLinkInFrontmatterExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	extra := `    archive:
      enabled: true
      linkInFrontmatter: false
`
	if err := os.WriteFile(path, []byte(makeMinimalGitHubYAML(dir, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Target.GitHub.Archive.LinkInFrontmatter == nil || *cfg.Target.GitHub.Archive.LinkInFrontmatter {
		t.Fatal("archive.linkInFrontmatter should be false when explicitly set to false")
	}
}

func TestLoad_UnknownProviderRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  address: ":0"
  storageDir: "` + escapeBackslashes(dir) + `"
llm:
  provider: "bogus"
target:
  github:
    enabled: true
    repositoryOwner: "org"
    repositoryName: "repo"
    branch: "main"
    filenameTemplate: "{{ .JobID }}.md"
    commitMessageTemplate: "Add {{ .JobID }}"
    auth:
      token: "tok"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown llm.provider")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error should mention provider, got: %v", err)
	}
}

func TestLoad_OpenAIProviderLoadsSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  address: ":0"
  storageDir: "` + escapeBackslashes(dir) + `"
llm:
  provider: "openai"
  openai:
    baseUrl: "http://proxy:8900"
    apiKey: "sk-test"
    model: "gpt-5"
target:
  github:
    enabled: true
    repositoryOwner: "org"
    repositoryName: "repo"
    branch: "main"
    filenameTemplate: "{{ .JobID }}.md"
    commitMessageTemplate: "Add {{ .JobID }}"
    auth:
      token: "tok"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.Provider != "openai" {
		t.Fatalf("expected provider=openai, got %q", cfg.LLM.Provider)
	}
	if cfg.LLM.OpenAI.BaseURL != "http://proxy:8900" {
		t.Fatalf("baseUrl mismatch: %q", cfg.LLM.OpenAI.BaseURL)
	}
	if cfg.LLM.OpenAI.APIKey != "sk-test" || cfg.LLM.OpenAI.Model != "gpt-5" {
		t.Fatalf("openai settings mismatch: %+v", cfg.LLM.OpenAI)
	}
}

func TestLoad_OpenAIProviderDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  address: ":0"
  storageDir: "` + escapeBackslashes(dir) + `"
llm:
  provider: "openai"
target:
  github:
    enabled: true
    repositoryOwner: "org"
    repositoryName: "repo"
    branch: "main"
    filenameTemplate: "{{ .JobID }}.md"
    commitMessageTemplate: "Add {{ .JobID }}"
    auth:
      token: "tok"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LLM.OpenAI.BaseURL != "http://localhost:8900" {
		t.Fatalf("expected default baseUrl, got %q", cfg.LLM.OpenAI.BaseURL)
	}
	if cfg.LLM.OpenAI.Model != "gpt-5" {
		t.Fatalf("expected default model gpt-5, got %q", cfg.LLM.OpenAI.Model)
	}
}
