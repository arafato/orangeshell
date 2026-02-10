package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/r2"
)

// R2Service implements the Service interface for Cloudflare R2 object storage.
type R2Service struct {
	client    *cloudflare.Client
	accountID string

	// Cache for search and detail lookups
	mu        sync.Mutex
	cached    []Resource
	cachedRaw map[string]r2.Bucket // keyed by bucket name
	cacheTime time.Time
}

// NewR2Service creates an R2 service.
func NewR2Service(client *cloudflare.Client, accountID string) *R2Service {
	return &R2Service{
		client:    client,
		accountID: accountID,
	}
}

func (s *R2Service) Name() string { return "R2" }

// List fetches all R2 buckets from the Cloudflare API.
func (s *R2Service) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// R2 has no auto-paging; fetch with a high per-page limit.
	resp, err := s.client.R2.Buckets.List(ctx, r2.BucketListParams{
		AccountID: cloudflare.F(s.accountID),
		PerPage:   cloudflare.F(1000.0),
		Order:     cloudflare.F(r2.BucketListParamsOrderName),
		Direction: cloudflare.F(r2.BucketListParamsDirectionAsc),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list R2 buckets: %w", err)
	}

	var resources []Resource
	rawMap := make(map[string]r2.Bucket)
	for _, b := range resp.Buckets {
		summary := formatBucketSummary(b)
		resources = append(resources, Resource{
			ID:          b.Name,
			Name:        b.Name,
			ServiceType: "R2",
			Summary:     summary,
		})
		rawMap[b.Name] = b
	}

	// Update cache
	s.mu.Lock()
	s.cached = resources
	s.cachedRaw = rawMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return resources, nil
}

// Get fetches detail for a single R2 bucket.
func (s *R2Service) Get(id string) (*ResourceDetail, error) {
	// Try cache first
	s.mu.Lock()
	raw, hasCached := s.cachedRaw[id]
	s.mu.Unlock()

	if !hasCached {
		// Fetch from API
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		bucket, err := s.client.R2.Buckets.Get(ctx, id, r2.BucketGetParams{
			AccountID: cloudflare.F(s.accountID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get R2 bucket %s: %w", id, err)
		}
		raw = *bucket
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          raw.Name,
			Name:        raw.Name,
			ServiceType: "R2",
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Bucket Name",
		Value: raw.Name,
	})

	if raw.CreationDate != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Created",
			Value: raw.CreationDate,
		})
	}

	if string(raw.Location) != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Location",
			Value: formatLocation(raw.Location),
		})
	}

	if string(raw.Jurisdiction) != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Jurisdiction",
			Value: string(raw.Jurisdiction),
		})
	}

	if string(raw.StorageClass) != "" {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Storage Class",
			Value: string(raw.StorageClass),
		})
	}

	return detail, nil
}

// SearchItems returns the cached list of R2 buckets for fuzzy search.
func (s *R2Service) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}

func formatBucketSummary(b r2.Bucket) string {
	parts := []string{}

	if b.CreationDate != "" {
		// Parse and show as relative time if possible
		if t, err := time.Parse(time.RFC3339, b.CreationDate); err == nil {
			parts = append(parts, fmt.Sprintf("created %s", timeAgo(t)))
		} else {
			parts = append(parts, fmt.Sprintf("created %s", b.CreationDate))
		}
	}

	if string(b.Location) != "" {
		parts = append(parts, formatLocation(b.Location))
	}

	if string(b.StorageClass) != "" {
		parts = append(parts, string(b.StorageClass))
	}

	return joinParts(parts)
}

func formatLocation(loc r2.BucketLocation) string {
	switch loc {
	case r2.BucketLocationApac:
		return "Asia Pacific"
	case r2.BucketLocationEeur:
		return "Eastern Europe"
	case r2.BucketLocationEnam:
		return "Eastern North America"
	case r2.BucketLocationWeur:
		return "Western Europe"
	case r2.BucketLocationWnam:
		return "Western North America"
	case r2.BucketLocationOc:
		return "Oceania"
	default:
		return string(loc)
	}
}

func joinParts(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += " | "
		}
		result += p
	}
	return result
}
