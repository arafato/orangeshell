package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// AuthMethod represents the type of authentication being used.
type AuthMethod string

const (
	AuthMethodAPIKey   AuthMethod = "apikey"
	AuthMethodAPIToken AuthMethod = "apitoken"
	AuthMethodOAuth    AuthMethod = "oauth"
	AuthMethodNone     AuthMethod = ""
)

// AIProvider identifies the AI backend.
type AIProvider string

const (
	AIProviderNone      AIProvider = ""
	AIProviderWorkersAI AIProvider = "workers_ai"
	// AIProviderAnthropic AIProvider = "anthropic" // future
)

// AIModelPreset identifies a Workers AI model tier.
type AIModelPreset string

const (
	AIModelFast     AIModelPreset = "fast"     // llama-3.1-8b-instruct-fast
	AIModelBalanced AIModelPreset = "balanced" // llama-3.3-70b-instruct-fp8-fast (default)
	AIModelDeep     AIModelPreset = "deep"     // deepseek-r1-distill-qwen-32b
)

// Config holds all persistent configuration for orangeshell.
type Config struct {
	// Auth settings
	AuthMethod AuthMethod `toml:"auth_method"`
	AccountID  string     `toml:"account_id"`
	Email      string     `toml:"email,omitempty"`
	APIKey     string     `toml:"api_key,omitempty"`
	APIToken   string     `toml:"api_token,omitempty"`

	// OAuth tokens
	OAuthAccessToken  string    `toml:"oauth_access_token,omitempty"`
	OAuthRefreshToken string    `toml:"oauth_refresh_token,omitempty"`
	OAuthExpiresAt    time.Time `toml:"oauth_expires_at,omitempty"`
	OAuthScopes       []string  `toml:"oauth_scopes,omitempty"`

	// AI settings
	AIProvider     AIProvider    `toml:"ai_provider,omitempty"`
	AIModelPreset  AIModelPreset `toml:"ai_model_preset,omitempty"`
	AIWorkerURL    string        `toml:"ai_worker_url,omitempty"`
	AIWorkerSecret string        `toml:"ai_worker_secret,omitempty"`
}

// configDir returns the path to ~/.orangeshell/
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".orangeshell"), nil
}

// ConfigPath returns the full path to the config file.
func ConfigPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads the config from disk and applies environment variable overrides.
// If the config file does not exist, it returns a zero-value Config (not an error).
func Load() (*Config, error) {
	cfg := &Config{}

	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config at %s: %w", path, err)
		}
	}

	// Environment variable overrides (highest priority)
	if v := os.Getenv("CLOUDFLARE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("CLOUDFLARE_EMAIL"); v != "" {
		cfg.Email = v
	}
	if v := os.Getenv("CLOUDFLARE_API_TOKEN"); v != "" {
		cfg.APIToken = v
	}
	if v := os.Getenv("CLOUDFLARE_ACCOUNT_ID"); v != "" {
		cfg.AccountID = v
	}

	// Infer auth method from env vars if not set in config
	if cfg.AuthMethod == AuthMethodNone {
		switch {
		case cfg.APIToken != "":
			cfg.AuthMethod = AuthMethodAPIToken
		case cfg.APIKey != "" && cfg.Email != "":
			cfg.AuthMethod = AuthMethodAPIKey
		case cfg.OAuthAccessToken != "":
			cfg.AuthMethod = AuthMethodOAuth
		}
	}

	return cfg, nil
}

// Save writes the config to disk, creating the directory if needed.
func (c *Config) Save() error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	path := filepath.Join(dir, "config.toml")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	return nil
}

// IsConfigured returns true if enough auth info exists to attempt authentication.
func (c *Config) IsConfigured() bool {
	switch c.AuthMethod {
	case AuthMethodAPIKey:
		return c.APIKey != "" && c.Email != "" && c.AccountID != ""
	case AuthMethodAPIToken:
		return c.APIToken != "" && c.AccountID != ""
	case AuthMethodOAuth:
		return c.OAuthAccessToken != "" && c.AccountID != ""
	default:
		return false
	}
}
