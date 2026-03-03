package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/option"
	"github.com/cloudflare/cloudflare-go/v6/queues"
)

// ErrHTTPPullNotEnabled indicates that the queue does not have an HTTP pull
// consumer enabled, which is required to pull/peek messages.
var ErrHTTPPullNotEnabled = errors.New("queue does not have HTTP pull enabled")

// ErrQueueHasConsumer indicates that the queue already has a consumer attached,
// preventing the addition of an HTTP pull consumer.
var ErrQueueHasConsumer = errors.New("queue already has a consumer")

// QueueMessage represents a single message pulled from a queue.
type QueueMessage struct {
	ID          string
	Body        string
	Attempts    int
	TimestampMs int64
	LeaseID     string
	Metadata    map[string]string
}

// QueuePullResult holds the result of pulling messages from a queue.
type QueuePullResult struct {
	Messages     []QueueMessage
	BacklogCount int
}

// QueueConsumer represents a consumer attached to a queue.
type QueueConsumer struct {
	Type           string // "worker" or "http_pull"
	Name           string // script name or "(pull)"
	BatchSize      int
	MaxRetries     int
	MaxConcurrency int
	RetryDelay     int    // seconds
	DLQ            string // dead letter queue name (empty if none)
}

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

		prodStr := formatProducers(q.Producers, q.ProducersTotalCount)
		summary := fmt.Sprintf("producers: %s  consumers: %.0f",
			prodStr, q.ConsumersTotalCount)
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
		Value: formatProducers(raw.Producers, raw.ProducersTotalCount),
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

// formatProducers returns a human-readable string for a queue's producers.
// If names are available, returns them comma-separated (e.g. "my-worker, photos (R2)").
// Falls back to a numeric count if the producers slice is empty.
func formatProducers(producers []queues.QueueProducer, count float64) string {
	if len(producers) == 0 {
		return fmt.Sprintf("%.0f", count)
	}
	var names []string
	for _, p := range producers {
		switch u := p.AsUnion().(type) {
		case queues.QueueProducersMqWorkerProducer:
			if u.Script != "" {
				names = append(names, u.Script)
			}
		case queues.QueueProducersMqR2Producer:
			if u.BucketName != "" {
				names = append(names, u.BucketName+" (R2)")
			}
		default:
			// Unknown producer type — use the outer struct fields as fallback.
			if p.Script != "" {
				names = append(names, p.Script)
			} else if p.BucketName != "" {
				names = append(names, p.BucketName+" (R2)")
			}
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf("%.0f", count)
	}
	return strings.Join(names, ", ")
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

// PullMessages pulls a snapshot of messages from a queue with a short
// visibility timeout. Messages automatically return to the queue after the
// timeout expires. We do NOT call the Ack/Retry endpoint because retrying
// increments the delivery attempt counter — once max_retries is reached the
// message is permanently deleted. Instead we rely solely on the visibility
// timeout to release messages back.
//
// Note: if the user refreshes within the timeout window, previously pulled
// messages may still be leased and won't appear. This is inherent to the
// queue pull model — there is no true "peek" API.
func (s *QueueService) PullMessages(queueID string, batchSize int) (*QueuePullResult, error) {
	queueID = s.resolveQueueID(queueID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := s.client.Queues.Messages.Pull(ctx, queueID, queues.MessagePullParams{
		AccountID:           cloudflare.F(s.accountID),
		BatchSize:           cloudflare.F(float64(batchSize)),
		VisibilityTimeoutMs: cloudflare.F(float64(1000)), // 1-second lease — shortest practical timeout
	})
	if err != nil {
		// Detect the specific 405 error when http_pull is not enabled.
		// The SDK returns an internal apierror.Error with StatusCode 405,
		// but since it's in an internal package we match on the error string.
		errStr := err.Error()
		if strings.Contains(errStr, "405") && strings.Contains(errStr, "pull") {
			return nil, fmt.Errorf("failed to pull messages from queue %s: %w", queueID, ErrHTTPPullNotEnabled)
		}
		return nil, fmt.Errorf("failed to pull messages from queue %s: %w", queueID, err)
	}

	result := &QueuePullResult{
		BacklogCount: int(resp.MessageBacklogCount),
	}
	for _, m := range resp.Messages {
		meta := make(map[string]string)
		if m.Metadata != nil {
			if metaMap, ok := m.Metadata.(map[string]interface{}); ok {
				for k, v := range metaMap {
					meta[k] = fmt.Sprintf("%v", v)
				}
			}
		}
		result.Messages = append(result.Messages, QueueMessage{
			ID:          m.ID,
			Body:        m.Body,
			Attempts:    int(m.Attempts),
			TimestampMs: int64(m.TimestampMs),
			LeaseID:     m.LeaseID,
			Metadata:    meta,
		})
	}
	return result, nil
}

// ListConsumers fetches all consumers attached to a queue.
func (s *QueueService) ListConsumers(queueID string) ([]QueueConsumer, error) {
	queueID = s.resolveQueueID(queueID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := s.client.Queues.Consumers.ListAutoPaging(ctx, queueID, queues.ConsumerListParams{
		AccountID: cloudflare.F(s.accountID),
	})

	var consumers []QueueConsumer
	for pager.Next() {
		c := pager.Current()
		qc := QueueConsumer{
			Type: string(c.Type),
			Name: c.Script,
		}
		if qc.Name == "" {
			qc.Name = "(pull)"
		}

		// Extract settings from the union type
		switch u := c.AsUnion().(type) {
		case queues.ConsumerMqWorkerConsumer:
			qc.BatchSize = int(u.Settings.BatchSize)
			qc.MaxRetries = int(u.Settings.MaxRetries)
			qc.MaxConcurrency = int(u.Settings.MaxConcurrency)
			qc.RetryDelay = int(u.Settings.RetryDelay)
		case queues.ConsumerMqHTTPConsumer:
			qc.BatchSize = int(u.Settings.BatchSize)
			qc.MaxRetries = int(u.Settings.MaxRetries)
			qc.RetryDelay = int(u.Settings.RetryDelay)
		}

		consumers = append(consumers, qc)
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list consumers for queue %s: %w", queueID, err)
	}

	return consumers, nil
}

// PushMessage pushes a single message to a queue. The body is sent as-is;
// if it's valid JSON it uses the JSON content type, otherwise plain text.
func (s *QueueService) PushMessage(queueID string, body string) error {
	queueID = s.resolveQueueID(queueID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to determine if the body is valid JSON
	if json.Valid([]byte(body)) {
		var parsed interface{}
		if err := json.Unmarshal([]byte(body), &parsed); err == nil {
			_, err = s.client.Queues.Messages.Push(ctx, queueID, queues.MessagePushParams{
				AccountID: cloudflare.F(s.accountID),
				Body: queues.MessagePushParamsBodyMqQueueMessageJson{
					Body:        cloudflare.F(parsed),
					ContentType: cloudflare.F(queues.MessagePushParamsBodyMqQueueMessageJsonContentTypeJson),
				},
			})
			if err != nil {
				return fmt.Errorf("failed to push message to queue %s: %w", queueID, err)
			}
			return nil
		}
	}

	// Fall back to text content type
	_, err := s.client.Queues.Messages.Push(ctx, queueID, queues.MessagePushParams{
		AccountID: cloudflare.F(s.accountID),
		Body: queues.MessagePushParamsBodyMqQueueMessageText{
			Body:        cloudflare.F(body),
			ContentType: cloudflare.F(queues.MessagePushParamsBodyMqQueueMessageTextContentTypeText),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to push message to queue %s: %w", queueID, err)
	}
	return nil
}

// EnableHTTPPull adds an HTTP pull consumer to a queue, which is required
// before messages can be pulled/peeked. If an http_pull consumer already exists
// (or any other consumer conflict occurs), the error is returned as-is.
func (s *QueueService) EnableHTTPPull(queueID string) error {
	queueID = s.resolveQueueID(queueID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := s.client.Queues.Consumers.New(ctx, queueID, queues.ConsumerNewParams{
		AccountID: cloudflare.F(s.accountID),
		Body: queues.ConsumerNewParamsBodyMqHTTPConsumer{
			Type: cloudflare.F(queues.ConsumerNewParamsBodyMqHTTPConsumerTypeHTTPPull),
		},
	})
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "already has a consumer") {
			return fmt.Errorf("%w: %w", ErrQueueHasConsumer, err)
		}
		return fmt.Errorf("failed to enable HTTP pull on queue %s: %w", queueID, err)
	}
	return nil
}

// SearchItems returns the cached list of queues for fuzzy search.
func (s *QueueService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}
