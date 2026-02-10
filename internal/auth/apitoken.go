package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/oarafat/orangeshell/internal/config"
)

// APITokenAuth authenticates using a scoped API Token (Bearer).
type APITokenAuth struct {
	token string
}

// NewAPITokenAuth creates a new API Token authenticator.
func NewAPITokenAuth(token string) *APITokenAuth {
	return &APITokenAuth{token: token}
}

func (a *APITokenAuth) Method() config.AuthMethod { return config.AuthMethodAPIToken }
func (a *APITokenAuth) GetAPIKey() string         { return "" }
func (a *APITokenAuth) GetEmail() string          { return "" }
func (a *APITokenAuth) GetToken() string          { return a.token }

// Validate checks credentials by calling /user/tokens/verify.
func (a *APITokenAuth) Validate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate token: %w", err)
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
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Success {
		if len(result.Errors) > 0 {
			return fmt.Errorf("token validation failed: %s", result.Errors[0].Message)
		}
		return fmt.Errorf("token validation failed (HTTP %d)", resp.StatusCode)
	}

	if result.Result.Status != "active" {
		return fmt.Errorf("token is not active (status: %s)", result.Result.Status)
	}

	return nil
}
