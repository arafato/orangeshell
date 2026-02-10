package auth

import (
	"context"
	"fmt"

	"github.com/oarafat/orangeshell/internal/config"
)

// Account represents a Cloudflare account.
type Account struct {
	ID   string
	Name string
}

// Authenticator provides credentials for Cloudflare API access.
type Authenticator interface {
	// Validate checks that the credentials are valid by calling the API.
	Validate(ctx context.Context) error

	// Method returns which auth method this authenticator uses.
	Method() config.AuthMethod

	// GetAPIKey returns the API key (empty for non-apikey methods).
	GetAPIKey() string

	// GetEmail returns the email (empty for non-apikey methods).
	GetEmail() string

	// GetToken returns the bearer token (API token or OAuth access token).
	GetToken() string
}

// New creates an Authenticator from the given config.
func New(cfg *config.Config) (Authenticator, error) {
	switch cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		return NewAPIKeyAuth(cfg.APIKey, cfg.Email), nil
	case config.AuthMethodAPIToken:
		return NewAPITokenAuth(cfg.APIToken), nil
	case config.AuthMethodOAuth:
		return NewOAuthAuth(cfg), nil
	default:
		return nil, fmt.Errorf("unknown auth method: %s", cfg.AuthMethod)
	}
}
