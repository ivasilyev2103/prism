// Package config provides configuration types and loading for Prism.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config is the main Prism configuration loaded from config.yaml.
type Config struct {
	ListenAddr     string       `yaml:"listen_addr"`
	ManagementAddr string       `yaml:"management_addr"`
	DataDir        string       `yaml:"data_dir"`
	Tier           int          `yaml:"tier"`
	Ollama         OllamaConfig `yaml:"ollama"`
	Privacy        PrivacyConf  `yaml:"privacy"`
	Cache          CacheConf    `yaml:"cache"`
	RateLimitPerMinute int      `yaml:"rate_limit_per_minute"`
	LogLevel       string       `yaml:"log_level"`
}

// OllamaConfig holds Ollama connection settings.
type OllamaConfig struct {
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`
	EmbedModel string `yaml:"embed_model"`
	NERModel   string `yaml:"ner_model"`
}

// PrivacyConf holds privacy pipeline settings.
type PrivacyConf struct {
	DefaultProfile string                       `yaml:"default_profile"`
	CustomPatterns map[string][]PatternEntry     `yaml:"custom_patterns"`
}

// PatternEntry is a user-defined PII detection pattern.
type PatternEntry struct {
	Name  string  `yaml:"name"`
	Regex string  `yaml:"pattern"`
	Score float64 `yaml:"score"`
}

// CacheConf holds semantic cache settings.
type CacheConf struct {
	Enabled  bool                       `yaml:"enabled"`
	Policies map[string]CachePolicyConf `yaml:"policies"`
}

// CachePolicyConf is per-ServiceType cache policy.
type CachePolicyConf struct {
	Enabled bool `yaml:"enabled"`
	TTL     int  `yaml:"ttl"`
}

// BudgetsConfig is the top-level budgets.yaml structure.
type BudgetsConfig struct {
	Budgets []BudgetEntry `yaml:"budgets"`
}

// BudgetEntry represents a single budget rule.
type BudgetEntry struct {
	Level      string  `yaml:"level"`
	ProjectID  string  `yaml:"project_id,omitempty"`
	ProviderID string  `yaml:"provider_id,omitempty"`
	LimitUSD   float64 `yaml:"limit_usd"`
	Period     string  `yaml:"period"`
	Action     string  `yaml:"action"`
}

// Defaults fills zero-value fields with sensible defaults.
func (c *Config) Defaults() { //nolint:gocritic // pointer receiver needed to mutate
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8080"
	}
	if c.ManagementAddr == "" {
		c.ManagementAddr = "127.0.0.1:8081"
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.Tier == 0 {
		c.Tier = 1
	}
	if c.Ollama.BaseURL == "" {
		c.Ollama.BaseURL = "http://localhost:11434"
	}
	if c.Ollama.Model == "" {
		c.Ollama.Model = "llama3"
	}
	if c.Ollama.EmbedModel == "" {
		c.Ollama.EmbedModel = "nomic-embed-text"
	}
	if c.Ollama.NERModel == "" {
		c.Ollama.NERModel = "llama3"
	}
	if c.Privacy.DefaultProfile == "" {
		c.Privacy.DefaultProfile = "moderate"
	}
	if c.RateLimitPerMinute == 0 {
		c.RateLimitPerMinute = 60
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

// DefaultDataDir returns the platform-appropriate default data directory.
func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".prism")
	}
	return filepath.Join(home, ".prism")
}

// DBPath returns the full path for a named database file.
func (c Config) DBPath(name string) string {
	return filepath.Join(c.DataDir, name)
}

// RoutesPath returns the path to routes.yaml.
func (c Config) RoutesPath() string {
	return filepath.Join(c.DataDir, "routes.yaml")
}

// Load reads config.yaml from the given data directory.
// If the file does not exist, returns a Config with defaults.
func Load(dataDir string) (*Config, error) {
	cfg := &Config{DataDir: dataDir}

	path := filepath.Join(dataDir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Defaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.DataDir = dataDir
	cfg.Defaults()
	return cfg, nil
}

// LoadBudgets reads budgets.yaml from the data directory.
// Returns an empty config if the file does not exist.
func LoadBudgets(dataDir string) (*BudgetsConfig, error) {
	path := filepath.Join(dataDir, "budgets.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &BudgetsConfig{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var bc BudgetsConfig
	if err := yaml.Unmarshal(data, &bc); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &bc, nil
}

// LoadRoutes reads routes.yaml and returns the raw YAML bytes.
func LoadRoutes(dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, "routes.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return data, nil
}

// WriteDefault writes a default config file if it does not already exist.
// Returns true if the file was written, false if it already existed.
func WriteDefault(path string, content []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil // already exists
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		return false, fmt.Errorf("config: write %s: %w", path, err)
	}
	return true, nil
}
