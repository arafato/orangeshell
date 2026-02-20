package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oarafat/orangeshell/internal/api"
)

// SecretsStoreService implements the Service and Deleter interfaces for Cloudflare Secrets Store.
// Stores are the top-level resource; secrets within a store are shown in the detail view.
type SecretsStoreService struct {
	rlc *api.ResourceListClient

	mu     sync.Mutex
	cached []Resource
}

// NewSecretsStoreService creates a Secrets Store service backed by raw HTTP.
func NewSecretsStoreService(rlc *api.ResourceListClient) *SecretsStoreService {
	return &SecretsStoreService{rlc: rlc}
}

func (s *SecretsStoreService) Name() string { return "Secrets Store" }

// List fetches all Secrets Store stores from the Cloudflare API.
func (s *SecretsStoreService) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := s.rlc.ListSecretsStoreStores(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Secrets Store stores: %w", err)
	}

	var resources []Resource
	for _, item := range items {
		resources = append(resources, Resource{
			ID:          item.ID,
			Name:        item.Name,
			ServiceType: "Secrets Store",
			Summary:     fmt.Sprintf("id: %s", truncateID(item.ID)),
		})
	}

	s.mu.Lock()
	s.cached = resources
	s.mu.Unlock()

	return resources, nil
}

// Get fetches detail for a single Secrets Store store, including its secrets.
func (s *SecretsStoreService) Get(id string) (*ResourceDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d, err := s.rlc.GetSecretsStoreStore(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get Secrets Store %s: %w", id, err)
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          d.ID,
			Name:        d.Name,
			ServiceType: "Secrets Store",
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Store ID",
		Value: d.ID,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Store Name",
		Value: d.Name,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Secrets",
		Value: fmt.Sprintf("%d secret(s)", len(d.Secrets)),
	})

	// Render secrets table as ExtraContent (same pattern as D1 showing tables)
	if len(d.Secrets) > 0 {
		detail.ExtraContent = renderSecretsTable(d.Secrets)
	}

	return detail, nil
}

// renderSecretsTable builds a simple text table of secrets for the detail view.
func renderSecretsTable(secrets []api.SecretsStoreSecret) string {
	var b strings.Builder
	b.WriteString("  Secrets:\n")
	b.WriteString("  ────────────────────────────────────────\n")

	// Header
	b.WriteString(fmt.Sprintf("  %-24s %-12s %s\n", "Name", "Scopes", "Comment"))
	b.WriteString("  ────────────────────────────────────────\n")

	for _, s := range secrets {
		name := s.Name
		if len(name) > 24 {
			name = name[:21] + "..."
		}
		scopes := s.Scopes
		if len(scopes) > 12 {
			scopes = scopes[:9] + "..."
		}
		comment := s.Comment
		if len(comment) > 30 {
			comment = comment[:27] + "..."
		}
		b.WriteString(fmt.Sprintf("  %-24s %-12s %s\n", name, scopes, comment))
	}

	return b.String()
}

// Delete removes a Secrets Store store by ID.
func (s *SecretsStoreService) Delete(ctx context.Context, id string) error {
	if err := s.rlc.DoDelete(ctx, "secrets_store/stores/"+id); err != nil {
		return fmt.Errorf("failed to delete Secrets Store %s: %w", id, err)
	}
	// Evict from cache
	s.mu.Lock()
	for i, r := range s.cached {
		if r.ID == id {
			s.cached = append(s.cached[:i], s.cached[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

// SearchItems returns the cached list of Secrets Store stores for fuzzy search.
func (s *SecretsStoreService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}
