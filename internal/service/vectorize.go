package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/oarafat/orangeshell/internal/api"
)

// VectorizeService implements the Service and Deleter interfaces for Cloudflare Vectorize.
type VectorizeService struct {
	rlc *api.ResourceListClient

	mu     sync.Mutex
	cached []Resource
}

// NewVectorizeService creates a Vectorize service backed by raw HTTP.
func NewVectorizeService(rlc *api.ResourceListClient) *VectorizeService {
	return &VectorizeService{rlc: rlc}
}

func (s *VectorizeService) Name() string { return "Vectorize" }

// List fetches all Vectorize indexes from the Cloudflare API.
func (s *VectorizeService) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := s.rlc.ListVectorizeIndexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Vectorize indexes: %w", err)
	}

	var resources []Resource
	for _, item := range items {
		resources = append(resources, Resource{
			ID:          item.Name, // Vectorize uses name as the identifier
			Name:        item.Name,
			ServiceType: "Vectorize",
			Summary:     fmt.Sprintf("index: %s", item.Name),
		})
	}

	s.mu.Lock()
	s.cached = resources
	s.mu.Unlock()

	return resources, nil
}

// Get fetches detail for a single Vectorize index.
func (s *VectorizeService) Get(id string) (*ResourceDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d, err := s.rlc.GetVectorizeIndex(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get Vectorize index %s: %w", id, err)
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          d.Name,
			Name:        d.Name,
			ServiceType: "Vectorize",
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Index Name",
		Value: d.Name,
	})
	if d.Description != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Description",
			Value: d.Description,
		})
	}
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Dimensions",
		Value: fmt.Sprintf("%d", d.Dimensions),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Distance Metric",
		Value: d.Metric,
	})

	return detail, nil
}

// Delete removes a Vectorize index by name.
func (s *VectorizeService) Delete(ctx context.Context, id string) error {
	if err := s.rlc.DoDelete(ctx, "vectorize/v2/indexes/"+id); err != nil {
		return fmt.Errorf("failed to delete Vectorize index %s: %w", id, err)
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

// SearchItems returns the cached list of Vectorize indexes for fuzzy search.
func (s *VectorizeService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}
