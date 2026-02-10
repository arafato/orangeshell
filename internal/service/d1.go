package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/d1"
)

// D1Service implements the Service interface for Cloudflare D1 SQL databases.
type D1Service struct {
	client    *cloudflare.Client
	accountID string

	// Cache for search and detail lookups
	mu        sync.Mutex
	cached    []Resource
	cachedRaw map[string]d1.DatabaseListResponse // keyed by database UUID
	cacheTime time.Time
}

// NewD1Service creates a D1 service.
func NewD1Service(client *cloudflare.Client, accountID string) *D1Service {
	return &D1Service{
		client:    client,
		accountID: accountID,
	}
}

func (s *D1Service) Name() string { return "D1" }

// List fetches all D1 databases from the Cloudflare API.
func (s *D1Service) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := s.client.D1.Database.ListAutoPaging(ctx, d1.DatabaseListParams{
		AccountID: cloudflare.F(s.accountID),
	})

	var resources []Resource
	rawMap := make(map[string]d1.DatabaseListResponse)
	for pager.Next() {
		db := pager.Current()

		summary := formatD1Summary(db)
		resources = append(resources, Resource{
			ID:          db.UUID,
			Name:        db.Name,
			ServiceType: "D1",
			ModifiedAt:  db.CreatedAt,
			Summary:     summary,
		})
		rawMap[db.UUID] = db
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list D1 databases: %w", err)
	}

	// Update cache
	s.mu.Lock()
	s.cached = resources
	s.cachedRaw = rawMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return resources, nil
}

// Get fetches full details for a single D1 database by UUID.
func (s *D1Service) Get(id string) (*ResourceDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := s.client.D1.Database.Get(ctx, id, d1.DatabaseGetParams{
		AccountID: cloudflare.F(s.accountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get D1 database %s: %w", id, err)
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          db.UUID,
			Name:        db.Name,
			ServiceType: "D1",
			ModifiedAt:  db.CreatedAt,
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Database ID",
		Value: db.UUID,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Name",
		Value: db.Name,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Created",
		Value: db.CreatedAt.Format(time.RFC3339),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Version",
		Value: db.Version,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "File Size",
		Value: formatFileSize(db.FileSize),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Tables",
		Value: fmt.Sprintf("%.0f", db.NumTables),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Read Replication",
		Value: string(db.ReadReplication.Mode),
	})

	return detail, nil
}

// SearchItems returns the cached list of D1 databases for fuzzy search.
func (s *D1Service) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}

func formatD1Summary(db d1.DatabaseListResponse) string {
	parts := []string{}

	if !db.CreatedAt.IsZero() {
		parts = append(parts, fmt.Sprintf("created %s", timeAgo(db.CreatedAt)))
	}

	if db.Version != "" {
		parts = append(parts, fmt.Sprintf("v%s", db.Version))
	}

	return joinParts(parts)
}

// formatFileSize converts bytes to a human-readable string.
func formatFileSize(bytes float64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%.0f B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", bytes/1024)
	case bytes < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", bytes/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GB", bytes/(1024*1024*1024))
	}
}
