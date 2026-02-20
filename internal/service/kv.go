package service

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/kv"
	"github.com/cloudflare/cloudflare-go/v6/option"
)

// KVService implements the Service interface for Cloudflare Workers KV.
type KVService struct {
	client    *cloudflare.Client
	accountID string

	// Cache for search and detail lookups
	mu        sync.Mutex
	cached    []Resource
	cachedRaw map[string]kv.Namespace // keyed by namespace ID
	cacheTime time.Time
}

// NewKVService creates a KV service.
func NewKVService(client *cloudflare.Client, accountID string) *KVService {
	return &KVService{
		client:    client,
		accountID: accountID,
	}
}

func (s *KVService) Name() string { return "KV" }

// List fetches all KV namespaces from the Cloudflare API.
func (s *KVService) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := s.client.KV.Namespaces.ListAutoPaging(ctx, kv.NamespaceListParams{
		AccountID: cloudflare.F(s.accountID),
	})

	var resources []Resource
	rawMap := make(map[string]kv.Namespace)
	for pager.Next() {
		ns := pager.Current()

		resources = append(resources, Resource{
			ID:          ns.ID,
			Name:        ns.Title,
			ServiceType: "KV",
			Summary:     fmt.Sprintf("id: %s", truncateID(ns.ID)),
		})
		rawMap[ns.ID] = ns
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list KV namespaces: %w", err)
	}

	// Update cache
	s.mu.Lock()
	s.cached = resources
	s.cachedRaw = rawMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return resources, nil
}

// Get fetches detail for a single KV namespace.
func (s *KVService) Get(id string) (*ResourceDetail, error) {
	// Try to get from cache first
	s.mu.Lock()
	raw, hasCached := s.cachedRaw[id]
	s.mu.Unlock()

	if !hasCached {
		// Fetch from API
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		ns, err := s.client.KV.Namespaces.Get(ctx, id, kv.NamespaceGetParams{
			AccountID: cloudflare.F(s.accountID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get KV namespace %s: %w", id, err)
		}
		raw = *ns
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          raw.ID,
			Name:        raw.Title,
			ServiceType: "KV",
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Namespace ID",
		Value: raw.ID,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Title",
		Value: raw.Title,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "URL Encoding",
		Value: fmt.Sprintf("%v", raw.SupportsURLEncoding),
	})

	return detail, nil
}

// Delete removes a KV namespace by ID.
func (s *KVService) Delete(ctx context.Context, id string) error {
	_, err := s.client.KV.Namespaces.Delete(ctx, id, kv.NamespaceDeleteParams{
		AccountID: cloudflare.F(s.accountID),
	}, option.WithMaxRetries(0))
	if err != nil {
		return fmt.Errorf("failed to delete KV namespace %s: %w", id, err)
	}
	s.mu.Lock()
	delete(s.cachedRaw, id)
	s.mu.Unlock()
	return nil
}

// SearchItems returns the cached list of KV namespaces for fuzzy search.
func (s *KVService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}

// truncateID shows the first 8 characters of an ID followed by "...".
func truncateID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "..."
}

// --- KV Key-Level Operations (Data Explorer) ---

// KVKeyEntry represents a single key-value pair in a KV namespace.
type KVKeyEntry struct {
	Name       string    // Key name
	Value      string    // Value (UTF-8 text or placeholder for binary)
	Expiration time.Time // Zero if no expiration
	IsBinary   bool      // True if value is non-UTF-8 binary
	ValueSize  int       // Size in bytes of the raw value
}

// ListKeysWithValues lists keys in a namespace (optionally filtered by prefix)
// and fetches the value for each key. Returns up to `limit` entries.
// Strategy: list keys first (1 API call), then fetch each value individually.
func (s *KVService) ListKeysWithValues(namespaceID, prefix string, limit int) ([]KVKeyEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if limit <= 0 {
		limit = 20
	}

	// Step 1: List keys with optional prefix filter
	params := kv.NamespaceKeyListParams{
		AccountID: cloudflare.F(s.accountID),
		Limit:     cloudflare.F(float64(limit)),
	}
	if prefix != "" {
		params.Prefix = cloudflare.F(prefix)
	}

	page, err := s.client.KV.Namespaces.Keys.List(ctx, namespaceID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list KV keys: %w", err)
	}

	keys := page.Result
	if len(keys) > limit {
		keys = keys[:limit]
	}

	if len(keys) == 0 {
		return nil, nil
	}

	// Step 2: Fetch values for each key (sequential to avoid rate limiting)
	entries := make([]KVKeyEntry, 0, len(keys))
	for _, k := range keys {
		entry := KVKeyEntry{
			Name: k.Name,
		}

		// Parse expiration if present
		if k.Expiration > 0 {
			entry.Expiration = time.Unix(int64(k.Expiration), 0)
		}

		// Fetch value
		value, size, isBinary, err := s.getValue(ctx, namespaceID, k.Name)
		if err != nil {
			// On error, show the error as the value rather than failing the whole list
			entry.Value = fmt.Sprintf("(error: %s)", err)
		} else {
			entry.Value = value
			entry.ValueSize = size
			entry.IsBinary = isBinary
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// getValue fetches a single KV value. Returns the value as a string,
// the raw byte size, whether it's binary, and any error.
func (s *KVService) getValue(ctx context.Context, namespaceID, keyName string) (string, int, bool, error) {
	// URL-encode the key name for safe path inclusion
	encodedKey := url.PathEscape(keyName)

	resp, err := s.client.KV.Namespaces.Values.Get(ctx, namespaceID, encodedKey, kv.NamespaceValueGetParams{
		AccountID: cloudflare.F(s.accountID),
	})
	if err != nil {
		return "", 0, false, err
	}
	defer resp.Body.Close()

	// Read up to 10KB to avoid loading huge values into memory
	const maxValueSize = 10 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxValueSize+1))
	if err != nil {
		return "", 0, false, fmt.Errorf("failed to read value: %w", err)
	}

	size := len(body)
	truncated := false
	if size > maxValueSize {
		body = body[:maxValueSize]
		truncated = true
	}

	// Check if the content is valid UTF-8 text
	if !utf8.Valid(body) {
		return fmt.Sprintf("(binary, %s)", formatBytes(size)), size, true, nil
	}

	value := string(body)
	if truncated {
		value += "…"
	}

	return value, size, false, nil
}

// formatBytes formats a byte count into a human-readable string.
func formatBytes(b int) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
