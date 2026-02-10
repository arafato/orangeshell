package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/oarafat/orangeshell/internal/config"
)

// APIKeyAuth authenticates using Global API Key + Email.
type APIKeyAuth struct {
	apiKey string
	email  string
}

// NewAPIKeyAuth creates a new API Key authenticator.
func NewAPIKeyAuth(apiKey, email string) *APIKeyAuth {
	return &APIKeyAuth{
		apiKey: apiKey,
		email:  email,
	}
}

func (a *APIKeyAuth) Method() config.AuthMethod { return config.AuthMethodAPIKey }
func (a *APIKeyAuth) GetAPIKey() string         { return a.apiKey }
func (a *APIKeyAuth) GetEmail() string          { return a.email }
func (a *APIKeyAuth) GetToken() string          { return "" }

// Validate checks credentials by calling /user/tokens/verify equivalent for API keys.
// For API keys we use /user endpoint which returns user details.
func (a *APIKeyAuth) Validate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/user", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Auth-Key", a.apiKey)
	req.Header.Set("X-Auth-Email", a.email)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate credentials: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Success {
		if len(result.Errors) > 0 {
			return fmt.Errorf("authentication failed: %s", result.Errors[0].Message)
		}
		return fmt.Errorf("authentication failed (HTTP %d)", resp.StatusCode)
	}

	return nil
}
