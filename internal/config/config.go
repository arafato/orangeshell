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

	// Per-account fallback API tokens for Cloudflare APIs that OAuth scopes don't
	// cover (e.g. Access Applications, Workers Builds). Maps accountID → token.
	FallbackTokens map[string]string `toml:"fallback_tokens,omitempty"`

	// AI settings
	AIProvider     AIProvider    `toml:"ai_provider,omitempty"`
	AIModelPreset  AIModelPreset `toml:"ai_model_preset,omitempty"`
	AIWorkerURL    string        `toml:"ai_worker_url,omitempty"`
	AIWorkerSecret string        `toml:"ai_worker_secret,omitempty"`

	// Tracks which fields were set from environment variables (never serialized).
	// Save() uses these to strip env-sourced values so they don't leak to disk.
	envOverrides map[string]bool `toml:"-"`
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

	// Environment variable overrides (highest priority).
	// Track which fields come from env vars so Save() can strip them.
	cfg.envOverrides = make(map[string]bool)
	if v := os.Getenv("CLOUDFLARE_API_KEY"); v != "" {
		cfg.APIKey = v
		cfg.envOverrides["APIKey"] = true
	}
	if v := os.Getenv("CLOUDFLARE_EMAIL"); v != "" {
		cfg.Email = v
		cfg.envOverrides["Email"] = true
	}
	if v := os.Getenv("CLOUDFLARE_API_TOKEN"); v != "" {
		cfg.APIToken = v
		cfg.envOverrides["APIToken"] = true
	}
	if v := os.Getenv("CLOUDFLARE_ACCOUNT_ID"); v != "" {
		cfg.AccountID = v
		cfg.envOverrides["AccountID"] = true
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
// Fields that were populated from environment variables are stripped before
// writing so that secrets from env vars never leak into the config file.
func (c *Config) Save() error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Temporarily clear fields that should not be persisted:
	// 1. Fields sourced from environment variables
	// 2. Global API Key + Email when auth method is OAuth (these are only
	//    needed at runtime for fallback auth and should not leak to disk)
	saved := c.stripTransientFields()
	defer c.restoreTransientFields(saved)

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

// stripTransientFields clears fields that should not be persisted to disk
// and returns the original values for restoration after encoding.
// This covers: (a) fields sourced from environment variables, and
// (b) Global API Key + Email when auth method is OAuth (runtime-only fallback).
func (c *Config) stripTransientFields() map[string]string {
	saved := make(map[string]string)

	// Strip env-var-sourced fields
	if c.envOverrides["APIKey"] {
		saved["APIKey"] = c.APIKey
		c.APIKey = ""
	}
	if c.envOverrides["Email"] {
		saved["Email"] = c.Email
		c.Email = ""
	}
	if c.envOverrides["APIToken"] {
		saved["APIToken"] = c.APIToken
		c.APIToken = ""
	}
	if c.envOverrides["AccountID"] {
		saved["AccountID"] = c.AccountID
		c.AccountID = ""
	}

	// For OAuth auth, also strip API Key + Email even if they came from the
	// config file (legacy). These are only used at runtime for fallback auth
	// and should not be persisted alongside OAuth tokens.
	if c.AuthMethod == AuthMethodOAuth {
		if _, already := saved["APIKey"]; !already && c.APIKey != "" {
			saved["APIKey"] = c.APIKey
			c.APIKey = ""
		}
		if _, already := saved["Email"]; !already && c.Email != "" {
			saved["Email"] = c.Email
			c.Email = ""
		}
	}

	return saved
}

// restoreTransientFields puts back the stripped field values after Save().
func (c *Config) restoreTransientFields(saved map[string]string) {
	if v, ok := saved["APIKey"]; ok {
		c.APIKey = v
	}
	if v, ok := saved["Email"]; ok {
		c.Email = v
	}
	if v, ok := saved["APIToken"]; ok {
		c.APIToken = v
	}
	if v, ok := saved["AccountID"]; ok {
		c.AccountID = v
	}
}

// FallbackTokenFor returns the per-account fallback API token for the given accountID,
// or "" if none is configured.
func (c *Config) FallbackTokenFor(accountID string) string {
	if c.FallbackTokens == nil {
		return ""
	}
	return c.FallbackTokens[accountID]
}

// SetFallbackToken stores a fallback API token for the given accountID.
func (c *Config) SetFallbackToken(accountID, token string) {
	if c.FallbackTokens == nil {
		c.FallbackTokens = make(map[string]string)
	}
	c.FallbackTokens[accountID] = token
}

// HasFallbackAuthFor returns true if a fallback token exists for the given accountID.
func (c *Config) HasFallbackAuthFor(accountID string) bool {
	return c.FallbackTokenFor(accountID) != ""
}

// HasFallbackAuth returns true if fallback credentials are available for APIs
// that the primary OAuth token cannot access (e.g. Access Applications, Workers Builds).
// Checks the per-account fallback token for the current AccountID.
func (c *Config) HasFallbackAuth() bool {
	return c.HasFallbackAuthFor(c.AccountID)
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
