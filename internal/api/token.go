package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Permission group IDs for scopes that OAuth tokens cannot provide.
const (
	// "Access: Apps and Policies Read" — account-scoped
	permAccessAppsRead = "7ea222f6d5064cfa89ea366d7c1fee89"
	// "Workers CI Read" — account-scoped
	permWorkersCIRead = "ad99c5ae555e45c4bef5bdf2678388ba"
	// "Account Analytics Read" — account-scoped (required for GraphQL Analytics API)
	permAccountAnalyticsRead = "b89a480218d04ceb98b4fe57ca29dc1f"
)

// tokenCreateRequest is the request body for POST /user/tokens.
type tokenCreateRequest struct {
	Name      string          `json:"name"`
	Policies  []tokenPolicy   `json:"policies"`
	Condition *tokenCondition `json:"condition,omitempty"`
	NotBefore *string         `json:"not_before,omitempty"`
	ExpiresOn *string         `json:"expires_on,omitempty"`
}

type tokenPolicy struct {
	Effect           string            `json:"effect"`
	Resources        map[string]string `json:"resources"`
	PermissionGroups []tokenPermGroup  `json:"permission_groups"`
}

type tokenPermGroup struct {
	ID string `json:"id"`
}

type tokenCondition struct{}

type tokenCreateResponse struct {
	Success bool        `json:"success"`
	Result  tokenResult `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

type tokenResult struct {
	ID    string `json:"id"`
	Value string `json:"value"` // The actual API token value (only returned on creation)
}

// CreateScopedToken creates an API token with Access Apps Read, Workers CI Read,
// and Account Analytics Read permissions, scoped to the given account.
// Uses Global API Key (email + key) auth.
// Returns the token value on success, or an error.
func CreateScopedToken(ctx context.Context, authEmail, authKey, accountID string) (string, error) {
	body := tokenCreateRequest{
		Name: "orangeshell (auto-provisioned)",
		Policies: []tokenPolicy{
			{
				Effect: "allow",
				Resources: map[string]string{
					fmt.Sprintf("com.cloudflare.api.account.%s", accountID): "*",
				},
				PermissionGroups: []tokenPermGroup{
					{ID: permAccessAppsRead},
					{ID: permWorkersCIRead},
					{ID: permAccountAnalyticsRead},
				},
			},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshalling token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.cloudflare.com/client/v4/user/tokens",
		bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Email", authEmail)
	req.Header.Set("X-Auth-Key", authKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token creation request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token creation failed (HTTP %d): %s", resp.StatusCode, truncateBody(respBody, 200))
	}

	var parsed tokenCreateResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}

	if !parsed.Success || parsed.Result.Value == "" {
		msg := "unknown error"
		if len(parsed.Errors) > 0 {
			msg = parsed.Errors[0].Message
		}
		return "", fmt.Errorf("token creation failed: %s", msg)
	}

	return parsed.Result.Value, nil
}
