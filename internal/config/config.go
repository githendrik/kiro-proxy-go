package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	// Server
	Host string `yaml:"host,omitempty"`
	Port int    `yaml:"port,omitempty"`

	// Proxy auth
	ProxyAPIKey string `yaml:"proxy_api_key,omitempty"`

	// Kiro credentials
	CredsFile    string `yaml:"creds_file,omitempty"`
	RefreshToken string `yaml:"refresh_token,omitempty"`
	Region       string `yaml:"region,omitempty"`
	APIRegion    string `yaml:"api_region,omitempty"`

	// Timeouts
	StreamingReadTimeout int `yaml:"streaming_read_timeout,omitempty"`
	MaxRetries           int `yaml:"max_retries,omitempty"`

	// Features
	FakeReasoning          bool `yaml:"fake_reasoning,omitempty"`
	FakeReasoningMaxTokens int  `yaml:"fake_reasoning_max_tokens,omitempty"`

	// Logging
	LogLevel string `yaml:"log_level,omitempty"`

	// Derived fields (not from config file)
	KiroAPIHost     string
	LogLevelSlog    slog.Level
	APIRegionSet    bool // tracks if api_region was explicitly set in config
}

// DefaultConfig returns a config with default values.
func DefaultConfig() *Config {
	return &Config{
		Host:                   "0.0.0.0",
		Port:                   8000,
		ProxyAPIKey:            "my-super-secret-password-123",
		Region:                 "us-east-1",
		StreamingReadTimeout:   300,
		MaxRetries:             3,
		FakeReasoning:          true,
		FakeReasoningMaxTokens: 4000,
		LogLevel:               "info",
	}
}

// Load reads configuration from file, environment variables, and .env files.
// Priority: env vars > .env files > config file > defaults
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Load config file if it exists
	configFile := findConfigFile()
	if configFile != "" {
		if err := cfg.loadFromFile(configFile); err != nil {
			return nil, fmt.Errorf("failed to load config file: %w", err)
		}
	}

	// Load .env file from current directory (for backwards compatibility)
	_ = godotenv.Load()

	// Override with environment variables
	cfg.overrideFromEnv()

	// Validate
	if cfg.CredsFile == "" && cfg.RefreshToken == "" {
		return nil, fmt.Errorf("either creds_file or refresh_token is required")
	}

	// Derive API host from API region
	if cfg.APIRegion == "" {
		cfg.APIRegion = cfg.Region
	}
	cfg.KiroAPIHost = fmt.Sprintf("https://q.%s.amazonaws.com", cfg.APIRegion)
	cfg.LogLevelSlog = parseLogLevel(cfg.LogLevel)

	return cfg, nil
}

// loadFromFile reads configuration from a YAML file.
func (c *Config) loadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Temp struct to avoid recursive calls
	var raw struct {
		Host                   string `yaml:"host"`
		Port                   int    `yaml:"port"`
		ProxyAPIKey            string `yaml:"proxy_api_key"`
		CredsFile              string `yaml:"creds_file"`
		RefreshToken           string `yaml:"refresh_token"`
		Region                 string `yaml:"region"`
		APIRegion              string `yaml:"api_region"`
		StreamingReadTimeout   int    `yaml:"streaming_read_timeout"`
		MaxRetries             int    `yaml:"max_retries"`
		FakeReasoning          *bool  `yaml:"fake_reasoning"`
		FakeReasoningMaxTokens int    `yaml:"fake_reasoning_max_tokens"`
		LogLevel               string `yaml:"log_level"`
	}

	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}

	// Apply loaded values (only if set)
	if raw.Host != "" {
		c.Host = raw.Host
	}
	if raw.Port != 0 {
		c.Port = raw.Port
	}
	if raw.ProxyAPIKey != "" {
		c.ProxyAPIKey = raw.ProxyAPIKey
	}
	if raw.CredsFile != "" {
		c.CredsFile = raw.CredsFile
	}
	if raw.RefreshToken != "" {
		c.RefreshToken = raw.RefreshToken
	}
	if raw.Region != "" {
		c.Region = raw.Region
	}
	if raw.APIRegion != "" {
		c.APIRegion = raw.APIRegion
		c.APIRegionSet = true
	}
	if raw.StreamingReadTimeout != 0 {
		c.StreamingReadTimeout = raw.StreamingReadTimeout
	}
	if raw.MaxRetries != 0 {
		c.MaxRetries = raw.MaxRetries
	}
	if raw.FakeReasoning != nil {
		c.FakeReasoning = *raw.FakeReasoning
	}
	if raw.FakeReasoningMaxTokens != 0 {
		c.FakeReasoningMaxTokens = raw.FakeReasoningMaxTokens
	}
	if raw.LogLevel != "" {
		c.LogLevel = raw.LogLevel
	}

	return nil
}

// overrideFromEnv overrides config with environment variables.
func (c *Config) overrideFromEnv() {
	if v := os.Getenv("SERVER_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		c.Port, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("PROXY_API_KEY"); v != "" {
		c.ProxyAPIKey = v
	}
	if v := os.Getenv("KIRO_CREDS_FILE"); v != "" {
		c.CredsFile = v
	}
	if v := os.Getenv("REFRESH_TOKEN"); v != "" {
		c.RefreshToken = v
	}
	if v := os.Getenv("KIRO_REGION"); v != "" {
		c.Region = v
	}
	if v := os.Getenv("KIRO_API_REGION"); v != "" {
		c.APIRegion = v
		c.APIRegionSet = true
	}
	if v := os.Getenv("STREAMING_READ_TIMEOUT"); v != "" {
		c.StreamingReadTimeout, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("MAX_RETRIES"); v != "" {
		c.MaxRetries, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("FAKE_REASONING"); v != "" {
		c.FakeReasoning = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("FAKE_REASONING_MAX_TOKENS"); v != "" {
		c.FakeReasoningMaxTokens, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
}

// findConfigFile searches for a config file in standard locations.
func findConfigFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	locations := []string{
		"kiro-proxy.yaml",
		"kiro-proxy.yml",
		".kiro-proxy.yaml",
		".kiro-proxy.yml",
	}

	if home != "" {
		locations = append(locations,
			filepath.Join(home, ".config", "kiro-proxy", "config.yaml"),
			filepath.Join(home, ".config", "kiro-proxy", "config.yml"),
			filepath.Join(home, ".kiro-proxy.yaml"),
			filepath.Join(home, ".kiro-proxy.yml"),
		)
	}

	locations = append(locations,
		"/etc/kiro-proxy/config.yaml",
		"/etc/kiro-proxy/config.yml",
	)

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc
		}
	}

	return ""
}

// Addr returns the listen address as host:port.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
