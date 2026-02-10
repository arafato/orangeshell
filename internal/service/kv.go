package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/kv"
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
