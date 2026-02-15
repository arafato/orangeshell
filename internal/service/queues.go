package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/option"
	"github.com/cloudflare/cloudflare-go/v6/queues"
)

// QueueService implements the Service interface for Cloudflare Queues.
type QueueService struct {
	client    *cloudflare.Client
	accountID string

	// Cache for search and resource lookups
	mu        sync.Mutex
	cached    []Resource
	cachedRaw map[string]queues.Queue // keyed by queue ID
	cacheTime time.Time
}

// NewQueueService creates a Queue service.
func NewQueueService(client *cloudflare.Client, accountID string) *QueueService {
	return &QueueService{
		client:    client,
		accountID: accountID,
	}
}

func (s *QueueService) Name() string { return "Queues" }

// List fetches all queues from the Cloudflare API.
func (s *QueueService) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := s.client.Queues.ListAutoPaging(ctx, queues.QueueListParams{
		AccountID: cloudflare.F(s.accountID),
	})

	var resources []Resource
	rawMap := make(map[string]queues.Queue)
	for pager.Next() {
		q := pager.Current()

		summary := fmt.Sprintf("producers: %.0f  consumers: %.0f",
			q.ProducersTotalCount, q.ConsumersTotalCount)
		resources = append(resources, Resource{
			ID:          q.QueueID,
			Name:        q.QueueName,
			ServiceType: "Queues",
			Summary:     summary,
		})
		rawMap[q.QueueID] = q
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list queues: %w", err)
	}

	s.mu.Lock()
	s.cached = resources
	s.cachedRaw = rawMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return resources, nil
}

// Get fetches detail for a single queue. The id parameter may be a queue UUID
// or a queue name (wrangler bindings store the name). If it's a name, this method
// resolves it to the UUID via the cache or by listing queues.
func (s *QueueService) Get(id string) (*ResourceDetail, error) {
	// Try to resolve the id — it might be a name rather than a UUID.
	id = s.resolveQueueID(id)

	s.mu.Lock()
	raw, hasCached := s.cachedRaw[id]
	s.mu.Unlock()

	if !hasCached {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		q, err := s.client.Queues.Get(ctx, id, queues.QueueGetParams{
			AccountID: cloudflare.F(s.accountID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get queue %s: %w", id, err)
		}
		raw = *q
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          raw.QueueID,
			Name:        raw.QueueName,
			ServiceType: "Queues",
		},
	}

	detail.Fields = append(detail.Fields, DetailField{
		Label: "Queue ID",
		Value: raw.QueueID,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Queue Name",
		Value: raw.QueueName,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Producers",
		Value: fmt.Sprintf("%.0f", raw.ProducersTotalCount),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Consumers",
		Value: fmt.Sprintf("%.0f", raw.ConsumersTotalCount),
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Created",
		Value: raw.CreatedOn,
	})
	detail.Fields = append(detail.Fields, DetailField{
		Label: "Modified",
		Value: raw.ModifiedOn,
	})

	return detail, nil
}

// Delete removes a queue by ID.
func (s *QueueService) Delete(ctx context.Context, id string) error {
	_, err := s.client.Queues.Delete(ctx, id, queues.QueueDeleteParams{
		AccountID: cloudflare.F(s.accountID),
	}, option.WithMaxRetries(0))
	if err != nil {
		return fmt.Errorf("failed to delete queue %s: %w", id, err)
	}
	s.mu.Lock()
	delete(s.cachedRaw, id)
	s.mu.Unlock()
	return nil
}

// resolveQueueID resolves a queue identifier that might be a name (from wrangler
// bindings) to the actual queue UUID. Checks the cache first; if no match, fetches
// the queue list to perform the resolution.
func (s *QueueService) resolveQueueID(id string) string {
	s.mu.Lock()
	// Check if it's already a valid queue ID in the cache
	if _, ok := s.cachedRaw[id]; ok {
		s.mu.Unlock()
		return id
	}
	// Check if it matches a queue name in the cache
	for _, r := range s.cached {
		if r.Name == id {
			s.mu.Unlock()
			return r.ID
		}
	}
	s.mu.Unlock()

	// Cache miss — list queues to resolve the name
	resources, err := s.List()
	if err != nil {
		return id // best-effort: return as-is
	}
	for _, r := range resources {
		if r.Name == id || r.ID == id {
			return r.ID
		}
	}
	return id
}

// SearchItems returns the cached list of queues for fuzzy search.
func (s *QueueService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}
