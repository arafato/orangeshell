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

func (r *ResourceListClient) doRequest(ctx context.Context, method, path string) ([]byte, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/%s", r.accountID, path)
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncateBody(body, 200))
	}

	return body, nil
}

func (r *ResourceListClient) doGet(ctx context.Context, path string) ([]byte, error) {
	return r.doRequest(ctx, http.MethodGet, path)
}

// DoDelete performs an HTTP DELETE against a Cloudflare v4 API path.
// Returns nil on success (HTTP 200). Errors on auth failure or non-200 status.
func (r *ResourceListClient) DoDelete(ctx context.Context, path string) error {
	_, err := r.doRequest(ctx, http.MethodDelete, path)
	return err
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

// --- Get functions (single-resource detail) ---

// VectorizeIndexDetail holds detail fields for a Vectorize index.
type VectorizeIndexDetail struct {
	Name        string
	Description string
	Dimensions  int
	Metric      string
}

// GetVectorizeIndex returns detail for a single Vectorize v2 index.
func (r *ResourceListClient) GetVectorizeIndex(ctx context.Context, name string) (*VectorizeIndexDetail, error) {
	body, err := r.doGet(ctx, "vectorize/v2/indexes/"+name)
	if err != nil {
		return nil, err
	}
	var resp cfSingleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing API response: %w", err)
	}
	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("API error: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("API returned success=false")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(resp.Result, &m); err != nil {
		return nil, fmt.Errorf("parsing result: %w", err)
	}
	d := &VectorizeIndexDetail{
		Name:        stringFromMap(m, "name"),
		Description: stringFromMap(m, "description"),
		Metric:      stringFromMap(m, "metric"),
	}
	if cfg, ok := m["config"].(map[string]interface{}); ok {
		if dim, ok := cfg["dimensions"].(float64); ok {
			d.Dimensions = int(dim)
		}
		if metric, ok := cfg["metric"].(string); ok && d.Metric == "" {
			d.Metric = metric
		}
	}
	// Some API versions put dimensions at top level
	if d.Dimensions == 0 {
		if dim, ok := m["dimensions"].(float64); ok {
			d.Dimensions = int(dim)
		}
	}
	return d, nil
}

// HyperdriveConfigDetail holds detail fields for a Hyperdrive configuration.
type HyperdriveConfigDetail struct {
	ID       string
	Name     string
	Database string
	Host     string
	Port     int
	Scheme   string
	User     string
}

// GetHyperdriveConfig returns detail for a single Hyperdrive configuration.
func (r *ResourceListClient) GetHyperdriveConfig(ctx context.Context, id string) (*HyperdriveConfigDetail, error) {
	body, err := r.doGet(ctx, "hyperdrive/configs/"+id)
	if err != nil {
		return nil, err
	}
	var resp cfSingleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing API response: %w", err)
	}
	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("API error: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("API returned success=false")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(resp.Result, &m); err != nil {
		return nil, fmt.Errorf("parsing result: %w", err)
	}
	d := &HyperdriveConfigDetail{
		ID:   stringFromMap(m, "id"),
		Name: stringFromMap(m, "name"),
	}
	if origin, ok := m["origin"].(map[string]interface{}); ok {
		d.Database = stringFromMap(origin, "database")
		d.Host = stringFromMap(origin, "host")
		d.Scheme = stringFromMap(origin, "scheme")
		d.User = stringFromMap(origin, "user")
		if port, ok := origin["port"].(float64); ok {
			d.Port = int(port)
		}
	}
	return d, nil
}

// SecretsStoreDetail holds detail fields for a Secrets Store store.
type SecretsStoreDetail struct {
	ID      string
	Name    string
	Secrets []SecretsStoreSecret
}

// SecretsStoreSecret represents a single secret within a store.
type SecretsStoreSecret struct {
	ID      string
	Name    string
	Scopes  string
	Comment string
}

// GetSecretsStoreStore returns detail for a single Secrets Store store,
// including the list of secrets within it.
func (r *ResourceListClient) GetSecretsStoreStore(ctx context.Context, storeID string) (*SecretsStoreDetail, error) {
	// Fetch store detail
	body, err := r.doGet(ctx, "secrets_store/stores/"+storeID)
	if err != nil {
		return nil, err
	}
	var resp cfSingleResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing API response: %w", err)
	}
	if !resp.Success {
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("API error: %s", resp.Errors[0].Message)
		}
		return nil, fmt.Errorf("API returned success=false")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(resp.Result, &m); err != nil {
		return nil, fmt.Errorf("parsing result: %w", err)
	}
	d := &SecretsStoreDetail{
		ID:   stringFromMap(m, "id"),
		Name: stringFromMap(m, "name"),
	}

	// Fetch secrets within the store
	secrets, err := r.ListSecretsStoreSecrets(ctx, storeID)
	if err == nil {
		d.Secrets = secrets
	}

	return d, nil
}

// ListSecretsStoreSecrets returns all secrets within a Secrets Store store.
func (r *ResourceListClient) ListSecretsStoreSecrets(ctx context.Context, storeID string) ([]SecretsStoreSecret, error) {
	body, err := r.doGet(ctx, fmt.Sprintf("secrets_store/stores/%s/secrets", storeID))
	if err != nil {
		return nil, err
	}
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
	var secrets []SecretsStoreSecret
	for _, raw := range resp.Result {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		s := SecretsStoreSecret{
			ID:      stringFromMap(m, "id"),
			Name:    stringFromMap(m, "name"),
			Comment: stringFromMap(m, "comment"),
		}
		// Scopes may be an array of strings
		if scopes, ok := m["scopes"].([]interface{}); ok {
			var parts []string
			for _, sc := range scopes {
				if str, ok := sc.(string); ok {
					parts = append(parts, str)
				}
			}
			s.Scopes = joinStrings(parts, ", ")
		}
		secrets = append(secrets, s)
	}
	return secrets, nil
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

// cfSingleResponse is the generic Cloudflare v4 single-object response envelope.
type cfSingleResponse struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []cfResError    `json:"errors"`
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
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
