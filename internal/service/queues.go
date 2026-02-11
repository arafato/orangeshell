package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
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

// Get fetches detail for a single queue.
func (s *QueueService) Get(id string) (*ResourceDetail, error) {
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

// SearchItems returns the cached list of queues for fuzzy search.
func (s *QueueService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}
