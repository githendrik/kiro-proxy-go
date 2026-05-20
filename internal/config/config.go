package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration.
type Config struct {
	// Server
	Host string
	Port int

	// Proxy auth
	ProxyAPIKey string

	// Kiro credentials
	CredsFile    string // path to JSON credentials file (kiro-cli)
	RefreshToken string // direct refresh token (alternative to file)
	Region       string // SSO/auth region (from creds file or KIRO_REGION)
	APIRegion    string // Q API region override (KIRO_API_REGION, defaults to Region)

	// API URLs (derived from APIRegion)
	KiroAPIHost string

	// Timeouts
	StreamingReadTimeout int // seconds
	MaxRetries           int

	// Features
	FakeReasoning          bool
	FakeReasoningMaxTokens int

	// Logging
	LogLevel slog.Level
}

// Load reads configuration from environment variables (with .env fallback).
func Load() (*Config, error) {
	// Load .env file if it exists (does not override existing env vars)
	_ = godotenv.Load()

	cfg := &Config{
		Host:                   getEnv("SERVER_HOST", "0.0.0.0"),
		Port:                   getEnvInt("SERVER_PORT", 8000),
		ProxyAPIKey:            getEnv("PROXY_API_KEY", "my-super-secret-password-123"),
		CredsFile:              getEnv("KIRO_CREDS_FILE", ""),
		RefreshToken:           getEnv("REFRESH_TOKEN", ""),
		Region:                 getEnv("KIRO_REGION", "us-east-1"),
		APIRegion:              getEnv("KIRO_API_REGION", ""),
		StreamingReadTimeout:   getEnvInt("STREAMING_READ_TIMEOUT", 300),
		MaxRetries:             getEnvInt("MAX_RETRIES", 3),
		FakeReasoning:          getEnvBool("FAKE_REASONING", true),
		FakeReasoningMaxTokens: getEnvInt("FAKE_REASONING_MAX_TOKENS", 4000),
		LogLevel:               parseLogLevel(getEnv("LOG_LEVEL", "info")),
	}

	// API region defaults to auth region if not explicitly overridden
	if cfg.APIRegion == "" {
		cfg.APIRegion = cfg.Region
	}

	// Derive API host from API region (using q.amazonaws.com endpoint which works with OIDC auth)
	cfg.KiroAPIHost = fmt.Sprintf("https://q.%s.amazonaws.com", cfg.APIRegion)

	// Need at least one credential source
	if cfg.CredsFile == "" && cfg.RefreshToken == "" {
		return nil, fmt.Errorf("either KIRO_CREDS_FILE or REFRESH_TOKEN is required")
	}

	return cfg, nil
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

func getEnvBool(key string, fallback bool) bool {
	v := strings.ToLower(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1" || v == "yes"
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
