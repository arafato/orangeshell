package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Permission group IDs for scopes that OAuth tokens cannot provide.
const (
	// "Access: Apps and Policies Read" — account-scoped
	permAccessAppsRead = "7ea222f6d5064cfa89ea366d7c1fee89"
	// "Workers CI Read" — account-scoped
	permWorkersCIRead = "ad99c5ae555e45c4bef5bdf2678388ba"
	// "Workers CI Write" — account-scoped (required for creating triggers/connections)
	permWorkersCIWrite = "2e095cf436e2455fa62c9a9c2e18c478"
	// "Account Analytics Read" — account-scoped (required for GraphQL Analytics API)
	permAccountAnalyticsRead = "b89a480218d04ceb98b4fe57ca29dc1f"

	// Build token permissions — required by Workers Builds to deploy.
	// See https://developers.cloudflare.com/workers/ci-cd/builds/configuration/#api-token
	permAccountSettingsRead = "c1fde68c7bcc44588cbb6ddbc16d6480"
	permWorkersScriptsWrite = "e086da7e2179491d91ee5f35b3ca210a"
	permWorkersKVWrite      = "f7f0eda5697f475c90846e879bab8666"
	permWorkersR2Write      = "bf7481a1826f439697cb59a20b22293e"
	permWorkersRoutesWrite  = "28f4b596e7d643029c524985477ae49a"
	permUserDetailsRead     = "8acbe5bb0d54464ab867149d7f7cf8ac"
	permMembershipsRead     = "3518d0f75557482e952c6762d3e64903"
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

// ScopedTokenResult holds both the token value and Cloudflare token ID.
type ScopedTokenResult struct {
	Value string // The actual API token value (bearer token)
	ID    string // The Cloudflare token ID (UUID) — needed for build token registration
}

// CreateScopedToken creates an API token with all permissions needed by orangeshell:
//   - Access Apps Read, Workers CI Read/Write, Account Analytics Read (for orangeshell features)
//   - Workers Scripts Write, KV Write, R2 Write, Account Settings Read (for Workers Builds deploys)
//   - Workers Routes Write (zone-scoped, for Workers Builds)
//   - User Details Read, Memberships Read (user-scoped, for Workers Builds)
//
// Uses Global API Key (email + key) auth.
// If a permission group ID is invalid (e.g. Cloudflare changed it), retries
// without the invalid permission so the token is still useful for other features.
// Returns both the token value and its Cloudflare ID on success, or an error.
func CreateScopedToken(ctx context.Context, authEmail, authKey, accountID string) (ScopedTokenResult, error) {
	// Resolve the user's Cloudflare ID — required for user-scoped permissions.
	// Cloudflare rejects wildcard user resources ("com.cloudflare.api.user.*")
	// with error 1001: "Access can only be scoped to a specific user".
	userID, err := getUserID(ctx, authEmail, authKey)
	if err != nil {
		// getUserID failed — will create token without user-scoped perms
	}

	policies := buildTokenPolicies(accountID, userID)

	result, err := createTokenWithPolicies(ctx, authEmail, authKey, policies)
	if err == nil {
		return result, nil
	}

	// If a permission group is not found, retry without invalid ones.
	errStr := err.Error()
	if !strings.Contains(errStr, "not found") {
		return ScopedTokenResult{}, err
	}

	// Filter out invalid permission IDs from all policies
	var validPolicies []tokenPolicy
	for _, p := range policies {
		var validPerms []tokenPermGroup
		for _, pg := range p.PermissionGroups {
			if strings.Contains(errStr, pg.ID) {
				continue
			}
			validPerms = append(validPerms, pg)
		}
		if len(validPerms) > 0 {
			p.PermissionGroups = validPerms
			validPolicies = append(validPolicies, p)
		}
	}

	if len(validPolicies) == 0 {
		return ScopedTokenResult{}, fmt.Errorf("all permission groups are invalid: %w", err)
	}

	return createTokenWithPolicies(ctx, authEmail, authKey, validPolicies)
}

// getUserID resolves the authenticated user's Cloudflare ID (user tag) via GET /user.
// This is needed because Cloudflare rejects wildcard user resources in token policies.
func getUserID(ctx context.Context, authEmail, authKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/user", nil)
	if err != nil {
		return "", fmt.Errorf("creating user request: %w", err)
	}
	req.Header.Set("X-Auth-Email", authEmail)
	req.Header.Set("X-Auth-Key", authKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("user request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading user response: %w", err)
	}

	var parsed struct {
		Success bool `json:"success"`
		Result  struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parsing user response: %w", err)
	}
	if !parsed.Success || parsed.Result.ID == "" {
		return "", fmt.Errorf("user API returned no ID (HTTP %d)", resp.StatusCode)
	}
	return parsed.Result.ID, nil
}

// buildTokenPolicies returns the policies needed for the orangeshell fallback token:
// account-scoped, zone-scoped, and (if userID is known) user-scoped.
func buildTokenPolicies(accountID, userID string) []tokenPolicy {
	policies := []tokenPolicy{
		{
			// Account-scoped permissions
			Effect: "allow",
			Resources: map[string]string{
				fmt.Sprintf("com.cloudflare.api.account.%s", accountID): "*",
			},
			PermissionGroups: []tokenPermGroup{
				// Orangeshell core features
				{ID: permAccessAppsRead},
				{ID: permWorkersCIRead},
				{ID: permWorkersCIWrite},
				{ID: permAccountAnalyticsRead},
				// Workers Builds deploy permissions
				{ID: permAccountSettingsRead},
				{ID: permWorkersScriptsWrite},
				{ID: permWorkersKVWrite},
				{ID: permWorkersR2Write},
			},
		},
		{
			// Zone-scoped permissions (Workers Routes)
			Effect: "allow",
			Resources: map[string]string{
				"com.cloudflare.api.account.zone.*": "*",
			},
			PermissionGroups: []tokenPermGroup{
				{ID: permWorkersRoutesWrite},
			},
		},
	}

	// User-scoped permissions — only included if we resolved the user ID.
	// Cloudflare rejects wildcard user resources with error 1001:
	// "Access can only be scoped to a specific user".
	if userID != "" {
		policies = append(policies, tokenPolicy{
			Effect: "allow",
			Resources: map[string]string{
				fmt.Sprintf("com.cloudflare.api.user.%s", userID): "*",
			},
			PermissionGroups: []tokenPermGroup{
				{ID: permUserDetailsRead},
				{ID: permMembershipsRead},
			},
		})
	}

	return policies
}

// VerifyTokenID calls GET /user/tokens/verify with a bearer token to retrieve
// its Cloudflare token ID. This is useful when we have a fallback token value
// but don't have its ID stored in config.
func VerifyTokenID(ctx context.Context, bearerToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return "", fmt.Errorf("creating verify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token verify request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading verify response: %w", err)
	}

	var parsed struct {
		Success bool `json:"success"`
		Result  struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"result"`
		Errors []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parsing verify response: %w", err)
	}
	if !parsed.Success || parsed.Result.ID == "" {
		msg := "unknown error"
		if len(parsed.Errors) > 0 {
			msg = parsed.Errors[0].Message
		}
		return "", fmt.Errorf("token verify failed: %s", msg)
	}

	return parsed.Result.ID, nil
}

// DeleteCloudflareToken deletes a Cloudflare API token by its ID.
// Uses Global API Key (email + key) auth.
// endpoint: DELETE /user/tokens/{token_id}
func DeleteCloudflareToken(ctx context.Context, authEmail, authKey, tokenID string) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/user/tokens/%s", tokenID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("creating delete request: %w", err)
	}
	req.Header.Set("X-Auth-Email", authEmail)
	req.Header.Set("X-Auth-Key", authKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("token delete request failed: %w", err)
	}
	defer resp.Body.Close()

	// Accept any 2xx (Cloudflare DELETE APIs may return 200 or 204)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("token delete failed (HTTP %d): %s", resp.StatusCode, truncateBody(body, 200))
}

// createTokenWithPolicies creates a scoped API token with the given policies.
func createTokenWithPolicies(ctx context.Context, authEmail, authKey string, policies []tokenPolicy) (ScopedTokenResult, error) {
	body := tokenCreateRequest{
		Name:     "orangeshell (auto-provisioned)",
		Policies: policies,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return ScopedTokenResult{}, fmt.Errorf("marshalling token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.cloudflare.com/client/v4/user/tokens",
		bytes.NewReader(payload))
	if err != nil {
		return ScopedTokenResult{}, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Email", authEmail)
	req.Header.Set("X-Auth-Key", authKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ScopedTokenResult{}, fmt.Errorf("token creation request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ScopedTokenResult{}, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return ScopedTokenResult{}, fmt.Errorf("token creation failed (HTTP %d): %s", resp.StatusCode, truncateBody(respBody, 200))
	}

	var parsed tokenCreateResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ScopedTokenResult{}, fmt.Errorf("parsing token response: %w", err)
	}

	if !parsed.Success || parsed.Result.Value == "" {
		msg := "unknown error"
		if len(parsed.Errors) > 0 {
			msg = parsed.Errors[0].Message
		}
		return ScopedTokenResult{}, fmt.Errorf("token creation failed: %s", msg)
	}

	return ScopedTokenResult{
		Value: parsed.Result.Value,
		ID:    parsed.Result.ID,
	}, nil
}
