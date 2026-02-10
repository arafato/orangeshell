package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/workers"
)

// WorkersService implements the Service interface for Cloudflare Workers.
type WorkersService struct {
	client    *cloudflare.Client
	accountID string

	// Cache for search and detail lookups
	mu        sync.Mutex
	cached    []Resource
	cachedRaw map[string]workers.ScriptListResponse // keyed by script ID
	cacheTime time.Time
}

// NewWorkersService creates a Workers service.
func NewWorkersService(client *cloudflare.Client, accountID string) *WorkersService {
	return &WorkersService{
		client:    client,
		accountID: accountID,
	}
}

func (s *WorkersService) Name() string { return "Workers" }

// List fetches all worker scripts from the Cloudflare API.
func (s *WorkersService) List() ([]Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pager := s.client.Workers.Scripts.ListAutoPaging(ctx, workers.ScriptListParams{
		AccountID: cloudflare.F(s.accountID),
	})

	var resources []Resource
	rawMap := make(map[string]workers.ScriptListResponse)
	for pager.Next() {
		w := pager.Current()

		summary := formatWorkerSummary(w)
		resources = append(resources, Resource{
			ID:          w.ID,
			Name:        w.ID,
			ServiceType: "Workers",
			ModifiedAt:  w.ModifiedOn,
			Summary:     summary,
		})
		rawMap[w.ID] = w
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("failed to list workers: %w", err)
	}

	// Update cache
	s.mu.Lock()
	s.cached = resources
	s.cachedRaw = rawMap
	s.cacheTime = time.Now()
	s.mu.Unlock()

	return resources, nil
}

// getSettings fetches worker settings with panic recovery.
// The cloudflare-go/v6 SDK has a bug in its union deserializer for the Placement
// field that panics on certain API responses. We recover from it gracefully.
func (s *WorkersService) getSettings(ctx context.Context, id string) (settings *workers.ScriptScriptAndVersionSettingGetResponse, err error) {
	defer func() {
		if r := recover(); r != nil {
			settings = nil
			err = fmt.Errorf("SDK panic deserializing settings for %s (cloudflare-go/v6 bug): %v", id, r)
		}
	}()

	settings, err = s.client.Workers.Scripts.ScriptAndVersionSettings.Get(ctx, id,
		workers.ScriptScriptAndVersionSettingGetParams{
			AccountID: cloudflare.F(s.accountID),
		})
	return
}

// Get fetches detailed info for a single worker by script name.
func (s *WorkersService) Get(id string) (*ResourceDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get settings (bindings, compatibility, etc.) — may fail due to SDK bug
	settings, settingsErr := s.getSettings(ctx, id)

	// Use cached list entry for metadata (routes, handlers, etc.) to avoid re-listing
	var listEntry *workers.ScriptListResponse
	s.mu.Lock()
	if raw, ok := s.cachedRaw[id]; ok {
		listEntry = &raw
	}
	s.mu.Unlock()

	// If both the settings call and cache miss, we have nothing to show
	if settingsErr != nil && listEntry == nil {
		return nil, fmt.Errorf("failed to get worker details for %s: %w", id, settingsErr)
	}

	detail := &ResourceDetail{
		Resource: Resource{
			ID:          id,
			Name:        id,
			ServiceType: "Workers",
		},
	}

	// Build detail fields from list entry metadata
	if listEntry != nil {
		detail.ModifiedAt = listEntry.ModifiedOn

		detail.Fields = append(detail.Fields, DetailField{
			Label: "Created",
			Value: listEntry.CreatedOn.Format(time.RFC3339),
		})
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Modified",
			Value: listEntry.ModifiedOn.Format(time.RFC3339),
		})
		if listEntry.LastDeployedFrom != "" {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Deployed From",
				Value: listEntry.LastDeployedFrom,
			})
		}

		// Routes
		if len(listEntry.Routes) > 0 {
			var patterns []string
			for _, r := range listEntry.Routes {
				patterns = append(patterns, r.Pattern)
			}
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Routes",
				Value: strings.Join(patterns, "\n         "),
			})
		}

		// Handlers
		if len(listEntry.Handlers) > 0 {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Handlers",
				Value: strings.Join(listEntry.Handlers, ", "),
			})
		}

		if string(listEntry.UsageModel) != "" {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Usage Model",
				Value: string(listEntry.UsageModel),
			})
		}

		detail.Fields = append(detail.Fields, DetailField{
			Label: "Logpush",
			Value: fmt.Sprintf("%v", listEntry.Logpush),
		})
	}

	// Build detail fields from settings (may be nil if SDK panicked)
	if settings != nil {
		if settings.CompatibilityDate != "" {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Compat Date",
				Value: settings.CompatibilityDate,
			})
		}
		if len(settings.CompatibilityFlags) > 0 {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Compat Flags",
				Value: strings.Join(settings.CompatibilityFlags, ", "),
			})
		}

		// Bindings — access safely since Placement union may be partially initialized
		if len(settings.Bindings) > 0 {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Bindings",
				Value: formatBindings(settings.Bindings),
			})
		}

		// Placement — guard against zero-value from partial deserialization
		if settings.Placement.Mode.Mode != "" {
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Placement",
				Value: string(settings.Placement.Mode.Mode),
			})
		}

		// Tail consumers
		if len(settings.TailConsumers) > 0 {
			var consumers []string
			for _, tc := range settings.TailConsumers {
				consumers = append(consumers, tc.Service)
			}
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Tail Consumers",
				Value: strings.Join(consumers, ", "),
			})
		}
	}

	// Note if settings were unavailable
	if settingsErr != nil {
		detail.Fields = append(detail.Fields, DetailField{
			Label: "Note",
			Value: "Some settings unavailable (SDK deserialization issue)",
		})
	}

	return detail, nil
}

// SearchItems returns the cached list of workers for fuzzy search.
func (s *WorkersService) SearchItems() []Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cached
}

func formatWorkerSummary(w workers.ScriptListResponse) string {
	parts := []string{}

	ago := timeAgo(w.ModifiedOn)
	parts = append(parts, fmt.Sprintf("modified %s", ago))

	if w.LastDeployedFrom != "" {
		parts = append(parts, fmt.Sprintf("via %s", w.LastDeployedFrom))
	}

	if len(w.Routes) > 0 {
		parts = append(parts, fmt.Sprintf("%d route(s)", len(w.Routes)))
	}

	return strings.Join(parts, " | ")
}

func formatBindings(bindings []workers.ScriptScriptAndVersionSettingGetResponseBinding) string {
	if len(bindings) == 0 {
		return "(none)"
	}

	var lines []string
	for _, b := range bindings {
		bindingType := string(b.Type)
		detail := ""
		switch bindingType {
		case "kv_namespace":
			detail = fmt.Sprintf("ns:%s", b.NamespaceID)
		case "d1":
			detail = fmt.Sprintf("db:%s", b.ID)
		case "r2_bucket":
			detail = b.BucketName
		case "durable_object_namespace":
			detail = b.ClassName
		case "service":
			detail = b.Service
		case "queue":
			detail = b.QueueName
		case "hyperdrive":
			detail = b.ID
		case "ai":
			detail = "Workers AI"
		case "secret_text", "plain_text":
			detail = "(value hidden)"
		default:
			detail = bindingType
		}
		lines = append(lines, fmt.Sprintf("  %s [%s] %s", b.Name, bindingType, detail))
	}
	return "\n" + strings.Join(lines, "\n")
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh ago", h)
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	default:
		months := int(d.Hours() / 24 / 30)
		return fmt.Sprintf("%dmo ago", months)
	}
}
