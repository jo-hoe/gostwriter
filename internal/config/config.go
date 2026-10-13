// Package config loads and validates gostwriter configuration from YAML.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration loaded from YAML.
type Config struct {
	Server ServerConfig  `yaml:"server"`
	LLM    LLMConfig     `yaml:"llm"`
	Target TargetsConfig `yaml:"target"`
}

// ServerConfig holds HTTP server and runtime settings.
type ServerConfig struct {
	Addr            string        `yaml:"address"`
	ReadTimeout     time.Duration `yaml:"readTimeout"`
	WriteTimeout    time.Duration `yaml:"writeTimeout"`
	IdleTimeout     time.Duration `yaml:"idleTimeout"`
	MaxUploadSize   ByteSize      `yaml:"maxUploadSize"`
	WorkerCount     int           `yaml:"workerCount"`
	StorageDir      string        `yaml:"storageDir"`
	APIKey          string        `yaml:"apiKey"`          // optional static API key header (X-API-Key)
	DatabasePath    string        `yaml:"databasePath"`    // optional, overrides default storage_dir/gostwriter.db
	ShutdownGrace   time.Duration `yaml:"shutdownGrace"`   // time to wait for workers before forced stop
	CallbackRetries int           `yaml:"callbackRetries"` // number of callback attempts
	CallbackBackoff time.Duration `yaml:"callbackBackoff"` // base backoff duration
	LogLevel        string        `yaml:"logLevel"`        // debug|info|warn|error
}

// LLMConfig selects provider and provider-specific options.
type LLMConfig struct {
	Provider string         `yaml:"provider"` // "mock" or "openai"
	Mock     MockSettings   `yaml:"mock"`
	OpenAI   OpenAISettings `yaml:"openai"`
}

// MockSettings config for the mock LLM.
type MockSettings struct {
	Delay  time.Duration `yaml:"delay"`
	Prefix string        `yaml:"prefix"`
}

// OpenAISettings configures the OpenAI-compatible chat provider. BaseURL may
// point at api.openai.com, an ai-proxy reverse proxy, or any compatible gateway.
type OpenAISettings struct {
	BaseURL      string        `yaml:"baseUrl"`      // e.g. http://localhost:8900 (ai-proxy) or https://api.openai.com
	APIKey       string        `yaml:"apiKey"`       // optional
	Model        string        `yaml:"model"`        // e.g. gpt-5
	SystemPrompt string        `yaml:"systemPrompt"` // optional system message override
	Instructions string        `yaml:"instructions"` // optional user instruction override
	Temperature  float32       `yaml:"temperature"`  // optional
	MaxTokens    int           `yaml:"maxTokens"`    // optional
	Timeout      time.Duration `yaml:"timeout"`      // HTTP client timeout; 0 → default of 5m
}

// TargetsConfig groups all possible target backends.
type TargetsConfig struct {
	GitHub GitHubTargetConfig `yaml:"github"`
}

// GitHubTargetConfig config for posting to a GitHub repository via REST API.
type GitHubTargetConfig struct {
	Enabled               bool             `yaml:"enabled"`
	RepositoryOwner       string           `yaml:"repositoryOwner"`
	RepositoryName        string           `yaml:"repositoryName"`
	Branch                string           `yaml:"branch"`
	BasePath              string           `yaml:"basePath"`
	FilenameTemplate      string           `yaml:"filenameTemplate"`
	CommitMessageTemplate string           `yaml:"commitMessageTemplate"`
	AuthorName            string           `yaml:"authorName"`
	AuthorEmail           string           `yaml:"authorEmail"`
	APIBaseURL            string           `yaml:"apiBaseUrl"` // optional, default https://api.github.com
	Auth                  GitHubAuthConfig `yaml:"auth"`
	// NamingStrategy selects the FileNamer implementation.
	// Valid values: "template" (default), "ai"
	NamingStrategy string `yaml:"namingStrategy"`
	// Archive, when enabled, commits the original uploaded document to the
	// repository alongside the transcription and links to it from the Markdown.
	Archive ArchiveConfig `yaml:"archive"`
	// Frontmatter, when enabled, prepends a YAML frontmatter block to each
	// transcription with provenance fields (source, source_hash, transcribed).
	Frontmatter FrontmatterConfig `yaml:"frontmatter"`
}

// ArchiveConfig controls archiving of the original uploaded document into the
// target repository.
type ArchiveConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Path              string `yaml:"path"`              // repository sub-path for originals, e.g. "originals/"
	LinkInFrontmatter *bool  `yaml:"linkInFrontmatter"` // nil = default (true when archive enabled); false = opt-out
}

// FrontmatterConfig controls YAML frontmatter prepended to each transcription.
type FrontmatterConfig struct {
	Enabled bool `yaml:"enabled"`
}

// GitHubAuthConfig holds token-based auth (Personal Access Token).
type GitHubAuthConfig struct {
	Token string `yaml:"token"` // PAT; supports env expansion
}

// ByteSize represents a size in bytes that unmarshals from strings like "10Mi", "20MB", "512KiB", "1024".
type ByteSize uint64

// UnmarshalYAML implements yaml unmarshalling for ByteSize.
func (b *ByteSize) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		str := strings.TrimSpace(value.Value)
		parsed, err := ParseByteSize(str)
		if err != nil {
			return err
		}
		*b = ByteSize(parsed)
		return nil
	}
	return fmt.Errorf("invalid bytesize node kind: %v", value.Kind)
}

var reNumeric = regexp.MustCompile(`^\d+$`)

// ParseByteSize parses a string like "10Mi", "20MB", "512KiB", "1024" into bytes.
// Supports Kubernetes-style quantities for binary units: Ki, Mi, Gi (case-insensitive).
// Also accepts KiB/MiB/GiB and decimal KB/MB/GB, and bare bytes.
func ParseByteSize(s string) (uint64, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty size")
	}
	// Numeric only
	if reNumeric.MatchString(s) {
		val, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size number: %w", err)
		}
		return val, nil
	}

	// Normalize to upper for suffix matching but keep numeric part as-is
	up := strings.ToUpper(s)

	type unit struct {
		suffix string
		value  uint64
	}
	units := []unit{
		// Kubernetes binary-style without 'B'
		{"KI", 1024},
		{"MI", 1024 * 1024},
		{"GI", 1024 * 1024 * 1024},
		// Binary with B
		{"KIB", 1024},
		{"MIB", 1024 * 1024},
		{"GIB", 1024 * 1024 * 1024},
		// Decimal
		{"KB", 1000},
		{"MB", 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(up, u.suffix) {
			num := strings.TrimSpace(s[:len(s)-len(u.suffix)])
			val, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size number in %q: %w", orig, err)
			}
			return uint64(val * float64(u.value)), nil
		}
	}
	return 0, fmt.Errorf("unknown size suffix in %q", orig)
}

// Load reads YAML config from path, expands environment variables, and validates it.
// If path is empty, it will attempt to read from env var GOSTWRITER_CONFIG, then default to "config.yaml".
func Load(path string) (*Config, error) {
	if path == "" {
		if env := os.Getenv("GOSTWRITER_CONFIG"); env != "" {
			path = env
		} else {
			path = "config.yaml"
		}
	}
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath) // #nosec G304 - reading sanitized config file path is expected
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	// Expand environment variables in file content.
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(&cfg)

	if err := postProcessTargets(&cfg); err != nil {
		return nil, err
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	// Ensure storage dir exists
	if cfg.Server.StorageDir != "" {
		if err := os.MkdirAll(cfg.Server.StorageDir, 0o750); err != nil {
			return nil, fmt.Errorf("ensure storage_dir: %w", err)
		}
	}
	// Default DB path under storage dir if not set.
	if cfg.Server.DatabasePath == "" {
		cfg.Server.DatabasePath = filepath.Join(cfg.Server.StorageDir, "gostwriter.db")
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	// Server defaults
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = ":8080"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 15 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 2 * time.Minute
	}
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = 60 * time.Second
	}
	if cfg.Server.MaxUploadSize == 0 {
		cfg.Server.MaxUploadSize = ByteSize(10 * 1024 * 1024) // 10 MiB default
	}
	if cfg.Server.WorkerCount <= 0 {
		cfg.Server.WorkerCount = 4
	}
	if cfg.Server.StorageDir == "" {
		cfg.Server.StorageDir = "data"
	}
	if cfg.Server.ShutdownGrace == 0 {
		cfg.Server.ShutdownGrace = 15 * time.Second
	}
	if cfg.Server.CallbackRetries == 0 {
		cfg.Server.CallbackRetries = 3
	}
	if cfg.Server.CallbackBackoff == 0 {
		cfg.Server.CallbackBackoff = 2 * time.Second
	}
	// Default log level
	if strings.TrimSpace(cfg.Server.LogLevel) == "" {
		cfg.Server.LogLevel = "info"
	}

	// LLM defaults
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "mock"
	}
	if cfg.LLM.Mock.Delay == 0 {
		cfg.LLM.Mock.Delay = 2 * time.Second
	}
	if cfg.LLM.Mock.Prefix == "" {
		cfg.LLM.Mock.Prefix = "Transcribed by Mock"
	}
	// OpenAI sensible defaults (used if provider == "openai")
	if strings.EqualFold(cfg.LLM.Provider, "openai") {
		if strings.TrimSpace(cfg.LLM.OpenAI.BaseURL) == "" {
			cfg.LLM.OpenAI.BaseURL = "http://localhost:8900"
		}
		if strings.TrimSpace(cfg.LLM.OpenAI.Model) == "" {
			cfg.LLM.OpenAI.Model = "gpt-5"
		}
	}
}

// postProcessTargets performs any normalization/defaulting needed for enabled targets.
func postProcessTargets(cfg *Config) error {
	// GitHub target
	if cfg.Target.GitHub.Enabled {
		cfg.Target.GitHub.BasePath = normalizePathPrefix(cfg.Target.GitHub.BasePath)
		if strings.TrimSpace(cfg.Target.GitHub.APIBaseURL) == "" {
			cfg.Target.GitHub.APIBaseURL = "https://api.github.com"
		}
		if strings.TrimSpace(cfg.Target.GitHub.NamingStrategy) == "" {
			cfg.Target.GitHub.NamingStrategy = "template"
		}
		if cfg.Target.GitHub.Archive.Enabled {
			// When no archive path is given, co-locate originals in the same
			// folder as the transcription (basePath). Originals keep the
			// transcription's filename stem but their own extension, so they do
			// not collide with the .md file.
			if strings.TrimSpace(cfg.Target.GitHub.Archive.Path) == "" {
				cfg.Target.GitHub.Archive.Path = cfg.Target.GitHub.BasePath
			}
			cfg.Target.GitHub.Archive.Path = normalizePathPrefix(cfg.Target.GitHub.Archive.Path)
			// Default to linking via frontmatter so archived originals are
			// always discoverable. Users can opt out by setting
			// linkInFrontmatter: false explicitly.
			if cfg.Target.GitHub.Archive.LinkInFrontmatter == nil {
				t := true
				cfg.Target.GitHub.Archive.LinkInFrontmatter = &t
			}
		}
	}
	return nil
}

func validate(cfg *Config) error {
	// Validate the LLM provider selection.
	switch strings.ToLower(strings.TrimSpace(cfg.LLM.Provider)) {
	case "mock", "openai":
		// valid
	default:
		return fmt.Errorf("llm.provider must be \"mock\" or \"openai\", got %q", cfg.LLM.Provider)
	}

	// Ensure at least one target is enabled
	if !cfg.Target.GitHub.Enabled {
		return errors.New("no target enabled")
	}

	// Validate enabled targets
	if cfg.Target.GitHub.Enabled {
		g := cfg.Target.GitHub
		if strings.TrimSpace(g.RepositoryOwner) == "" {
			return fmt.Errorf("github.repositoryOwner is required")
		}
		if strings.TrimSpace(g.RepositoryName) == "" {
			return fmt.Errorf("github.repositoryName is required")
		}
		if strings.TrimSpace(g.Branch) == "" {
			return fmt.Errorf("github.branch is required")
		}
		if g.NamingStrategy != "ai" && strings.TrimSpace(g.FilenameTemplate) == "" {
			return fmt.Errorf("github.filenameTemplate is required")
		}
		if strings.TrimSpace(g.CommitMessageTemplate) == "" {
			return fmt.Errorf("github.commitMessageTemplate is required")
		}
		if strings.TrimSpace(g.Auth.Token) == "" {
			return fmt.Errorf("github.auth.token is required")
		}
		switch g.NamingStrategy {
		case "template", "ai":
			// valid
		default:
			return fmt.Errorf("github.namingStrategy must be \"template\" or \"ai\", got %q", g.NamingStrategy)
		}
		// archive.path may be empty (co-locate with basePath, applied as a
		// default above) or equal basePath; originals keep the transcription's
		// stem but a different extension, so they do not collide with the .md.
		// A same-folder original that is itself Markdown is rejected at archive
		// time by the target, where the original's extension is known.
	}
	return nil
}

func normalizePathPrefix(p string) string {
	if p == "" {
		return p
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	// Remove leading "./"
	p = strings.TrimPrefix(p, "./")
	return p
}
