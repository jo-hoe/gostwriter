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
	Server ServerConfig `yaml:"server"`
	LLM    LLMConfig    `yaml:"llm"`
	Target TargetEntry  `yaml:"target"`
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

// TargetEntry describes the single configured target (e.g., Git).
type TargetEntry struct {
	Type string `yaml:"type"`
	Name string `yaml:"name"`

	// Git-specific fields (used when Type == "git")
	Git GitTargetConfig `yaml:"-"`
	// Raw map to help custom unmarshalling
	raw map[string]any
}

// GitTargetConfig config for posting to a Git repository.
type GitTargetConfig struct {
	RepoURL               string        `yaml:"repoUrl"`
	Branch                string        `yaml:"branch"`
	BasePath              string        `yaml:"basePath"`
	FilenameTemplate      string        `yaml:"filenameTemplate"`
	CommitMessageTemplate string        `yaml:"commitMessageTemplate"`
	AuthorName            string        `yaml:"authorName"`
	AuthorEmail           string        `yaml:"authorEmail"`
	Auth                  GitAuthConfig `yaml:"auth"`
	CloneCacheDir         string        `yaml:"cloneCacheDir"` // optional override for where to cache clones; defaults under storage_dir
}

// GitAuthConfig supports basic auth with PAT/token for HTTPS.
type GitAuthConfig struct {
	Type     string `yaml:"type"`     // "basic"
	Username string `yaml:"username"` // often "git" for GitHub
	Token    string `yaml:"token"`    // PAT or password; supports env expansion
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
	data, err := os.ReadFile(path)
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

	if err := postProcessTarget(&cfg, expanded); err != nil {
		return nil, err
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	// Ensure storage dir exists
	if cfg.Server.StorageDir != "" {
		if err := os.MkdirAll(cfg.Server.StorageDir, 0o755); err != nil {
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
		cfg.Server.WriteTimeout = 30 * time.Second
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

// postProcessTarget unmarshals per-target type configs and expands env in token fields if needed.
func postProcessTarget(cfg *Config, expandedYAML string) error {
	var rawNode struct {
		Target map[string]any `yaml:"target"`
	}
	if err := yaml.Unmarshal([]byte(expandedYAML), &rawNode); err != nil {
		return fmt.Errorf("parse raw target: %w", err)
	}
	entry := &cfg.Target
	entry.raw = rawNode.Target
	switch strings.ToLower(entry.Type) {
	case "git":
		var git GitTargetConfig
		if err := decodeMap(entry.raw, &git); err != nil {
			return fmt.Errorf("parse git target %q: %w", entry.Name, err)
		}
		// Normalize base path to use forward slashes and ensure trailing slash if provided.
		git.BasePath = normalizePathPrefix(git.BasePath)
		entry.Git = git
	default:
		return fmt.Errorf("unsupported target type %q for %q", entry.Type, entry.Name)
	}
	return nil
}

func validate(cfg *Config) error {
	// Validate target presence
	if strings.TrimSpace(cfg.Target.Type) == "" || strings.TrimSpace(cfg.Target.Name) == "" {
		return errors.New("target.type and target.name are required")
	}

	// Validate git target
	switch strings.ToLower(cfg.Target.Type) {
	case "git":
		g := cfg.Target.Git
		if g.RepoURL == "" {
			return fmt.Errorf("target %q: git.repoUrl is required", cfg.Target.Name)
		}
		if g.Branch == "" {
			return fmt.Errorf("target %q: git.branch is required", cfg.Target.Name)
		}
		if g.FilenameTemplate == "" {
			return fmt.Errorf("target %q: git.filenameTemplate is required", cfg.Target.Name)
		}
		if g.CommitMessageTemplate == "" {
			return fmt.Errorf("target %q: git.commitMessageTemplate is required", cfg.Target.Name)
		}
		if strings.ToLower(g.Auth.Type) != "basic" {
			return fmt.Errorf("target %q: git.auth.type must be \"basic\"", cfg.Target.Name)
		}
		if g.Auth.Token == "" {
			return fmt.Errorf("target %q: git.auth.token is required", cfg.Target.Name)
		}
		if g.Auth.Username == "" {
			g.Auth.Username = "git"
			cfg.Target.Git.Auth.Username = "git"
		}
	default:
		// already rejected in postProcessTarget
	}
	return nil
}

// decodeMap marshals a generic map into a struct.
func decodeMap(m map[string]any, out any) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
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
