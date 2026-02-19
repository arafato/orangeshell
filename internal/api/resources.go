package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResourceItem is a lightweight name+ID pair returned by list endpoints.
type ResourceItem struct {
	ID   string
	Name string
}

// ResourceListClient performs raw HTTP calls against Cloudflare v4 API endpoints
// that are not covered by the SDK. It reuses the Client's credentials.
type ResourceListClient struct {
	accountID string
	token     string // bearer token (API Token or OAuth)
	email     string // for X-Auth-Email + X-Auth-Key auth
	key       string
	http      *http.Client
}

// NewResourceListClientWithCreds creates a ResourceListClient with explicit credentials.
func NewResourceListClientWithCreds(accountID, email, key, token string) *ResourceListClient {
	return &ResourceListClient{
		accountID: accountID,
		email:     email,
		key:       key,
		token:     token,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *ResourceListClient) doGet(ctx context.Context, path string) ([]byte, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/%s", r.accountID, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	} else if r.key != "" {
		req.Header.Set("X-Auth-Email", r.email)
		req.Header.Set("X-Auth-Key", r.key)
	}

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading API response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("API authentication failed (HTTP %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncateBody(body, 200))
	}

	return body, nil
}

// --- List functions ---

// ListVectorizeIndexes returns all Vectorize v2 indexes.
func (r *ResourceListClient) ListVectorizeIndexes(ctx context.Context) ([]ResourceItem, error) {
	body, err := r.doGet(ctx, "vectorize/v2/indexes")
	if err != nil {
		return nil, err
	}
	return parseResourceList(body, "name", "name")
}

// ListHyperdriveConfigs returns all Hyperdrive configurations.
func (r *ResourceListClient) ListHyperdriveConfigs(ctx context.Context) ([]ResourceItem, error) {
	body, err := r.doGet(ctx, "hyperdrive/configs")
	if err != nil {
		return nil, err
	}
	return parseResourceList(body, "id", "name")
}

// ListMTLSCertificates returns all mTLS certificates.
func (r *ResourceListClient) ListMTLSCertificates(ctx context.Context) ([]ResourceItem, error) {
	body, err := r.doGet(ctx, "mtls_certificates")
	if err != nil {
		return nil, err
	}
	return parseResourceList(body, "id", "name")
}

// ListSecretsStoreStores returns all Secrets Store stores.
func (r *ResourceListClient) ListSecretsStoreStores(ctx context.Context) ([]ResourceItem, error) {
	body, err := r.doGet(ctx, "secrets_store/stores")
	if err != nil {
		return nil, err
	}
	return parseResourceList(body, "id", "name")
}

// --- Response parsing ---

// cfListResponse is the generic Cloudflare v4 list response envelope.
type cfListResponse struct {
	Success bool              `json:"success"`
	Result  []json.RawMessage `json:"result"`
	Errors  []cfResError      `json:"errors"`
}

type cfResError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// parseResourceList extracts ID and Name from a Cloudflare list response.
// idField and nameField are the JSON field names to extract from each result item.
func parseResourceList(body []byte, idField, nameField string) ([]ResourceItem, error) {
	var resp cfListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing API response: %w", err)
	}
	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("API error: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("API returned success=false")
	}

	var items []ResourceItem
	for _, raw := range resp.Result {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		id := stringFromMap(m, idField)
		name := stringFromMap(m, nameField)
		if id == "" && name == "" {
			continue
		}
		items = append(items, ResourceItem{ID: id, Name: name})
	}
	return items, nil
}

func stringFromMap(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}
