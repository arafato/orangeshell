package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/oarafat/orangeshell/internal/api"
)

// HyperdriveService implements the Service and Deleter interfaces for Cloudflare Hyperdrive.
type HyperdriveService struct {
	rlc *api.ResourceListClient

	mu     sync.Mutex
	cached []Resource
}

// NewHyperdriveService creates a Hyperdrive service backed by raw HTTP.
func NewHyperdriveService(rlc *api.ResourceListClient) *HyperdriveService {
	return &HyperdriveService{rlc: rlc}
}

func (s *HyperdriveService) Name() string { return "Hyperdrive" }

// List fetches all Hyperdrive configurations from the Cloudflare API.
func (s *HyperdriveService) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := s.rlc.ListHyperdriveConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Hyperdrive configs: %w", err)
	}

	var resources []Resource
	for _, item := range items {
		resources = append(resources, Resource{
			ID:          item.ID,
			Name:        item.Name,
			ServiceType: "Hyperdrive",
			Summary:     fmt.Sprintf("id: %s", truncateID(item.ID)),
		})
	}

	s.mu.Lock()
	s.cached = resources
	s.mu.Unlock()

	return resources, nil
}

// Get fetches detail for a single Hyperdrive configuration.
func (s *HyperdriveService) Get(id string) (*ResourceDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d, err := s.rlc.GetHyperdriveConfig(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get Hyperdrive config %s: %w", id, err)
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          d.ID,
			Name:        d.Name,
			ServiceType: "Hyperdrive",
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Config ID",
		Value: d.ID,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Name",
		Value: d.Name,
	})
	if d.Scheme != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Protocol",
			Value: d.Scheme,
		})
	}
	if d.Host != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Host",
			Value: d.Host,
		})
	}
	if d.Port > 0 {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Port",
			Value: fmt.Sprintf("%d", d.Port),
		})
	}
	if d.Database != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Database",
			Value: d.Database,
		})
	}
	if d.User != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "User",
			Value: d.User,
		})
	}

	return detail, nil
}

// Delete removes a Hyperdrive configuration by ID.
func (s *HyperdriveService) Delete(ctx context.Context, id string) error {
	if err := s.rlc.DoDelete(ctx, "hyperdrive/configs/"+id); err != nil {
		return fmt.Errorf("failed to delete Hyperdrive config %s: %w", id, err)
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

// SearchItems returns the cached list of Hyperdrive configs for fuzzy search.
func (s *HyperdriveService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}
