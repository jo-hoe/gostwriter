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
	Provider string          `yaml:"provider"` // e.g. "mock" or "aiproxy"
	Mock     MockSettings    `yaml:"mock"`
	AIProxy  AIProxySettings `yaml:"aiproxy"`
}

// MockSettings config for the mock LLM.
type MockSettings struct {
	Delay  time.Duration `yaml:"delay"`
	Prefix string        `yaml:"prefix"`
}

// AIProxySettings config for the AI Proxy (OpenAI-compatible) LLM.
type AIProxySettings struct {
	BaseURL      string  `yaml:"baseUrl"`      // e.g. http://localhost:8900
	APIKey       string  `yaml:"apiKey"`       // optional
	Model        string  `yaml:"model"`        // e.g. gpt-5
	SystemPrompt string  `yaml:"systemPrompt"` // optional system message override
	Instructions string  `yaml:"instructions"` // optional user instruction override
	Temperature  float32 `yaml:"temperature"`  // optional
	MaxTokens    int     `yaml:"maxTokens"`    // optional
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
	// AI Proxy sensible defaults (used if provider == "aiproxy")
	if strings.EqualFold(cfg.LLM.Provider, "aiproxy") {
		if strings.TrimSpace(cfg.LLM.AIProxy.BaseURL) == "" {
			cfg.LLM.AIProxy.BaseURL = "http://localhost:8900"
		}
		if strings.TrimSpace(cfg.LLM.AIProxy.Model) == "" {
			cfg.LLM.AIProxy.Model = "gpt-5"
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
	}
	return nil
}

func validate(cfg *Config) error {
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
		if strings.TrimSpace(g.FilenameTemplate) == "" {
			return fmt.Errorf("github.filenameTemplate is required")
		}
		if strings.TrimSpace(g.CommitMessageTemplate) == "" {
			return fmt.Errorf("github.commitMessageTemplate is required")
		}
		if strings.TrimSpace(g.Auth.Token) == "" {
			return fmt.Errorf("github.auth.token is required")
		}
	}
	return nil
}

func normalizePathPrefix(p string) string {
	if p == "" {
		return p
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasSuffix(p, "/") {
		p = p + "/"
	}
	// Remove leading "./"
	p = strings.TrimPrefix(p, "./")
	return p
}
