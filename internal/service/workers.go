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

// safeSettingsResponse is a hand-rolled struct for the worker settings endpoint.
// We use this instead of the SDK's generated type because the SDK has a bug in its
// union deserializer for the Placement field that panics on certain API responses.
// By using client.Get() with this struct, we bypass the buggy deserialization.
type safeSettingsResponse struct {
	Result safeSettings `json:"result"`
}

type safeSettings struct {
	Bindings           []safeBinding      `json:"bindings"`
	CompatibilityDate  string             `json:"compatibility_date"`
	CompatibilityFlags []string           `json:"compatibility_flags"`
	TailConsumers      []safeTailConsumer `json:"tail_consumers"`
}

type safeBinding struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	NamespaceID  string `json:"namespace_id,omitempty"`
	ID           string `json:"id,omitempty"`
	BucketName   string `json:"bucket_name,omitempty"`
	ClassName    string `json:"class_name,omitempty"`
	Service      string `json:"service,omitempty"`
	QueueName    string `json:"queue_name,omitempty"`
	Dataset      string `json:"dataset,omitempty"`
	IndexName    string `json:"index_name,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
	Pipeline     string `json:"pipeline,omitempty"`
}

type safeTailConsumer struct {
	Service string `json:"service"`
}

// getSettings fetches worker settings using a safe struct to avoid SDK deserialization panics.
func (s *WorkersService) getSettings(ctx context.Context, id string) (*safeSettings, error) {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/settings", s.accountID, id)
	var resp safeSettingsResponse
	err := s.client.Get(ctx, path, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("failed to get settings for %s: %w", id, err)
	}
	return &resp.Result, nil
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

	// Build detail fields from settings (may be nil if the API call failed)
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

		// Bindings — parse into structured data and also format for display
		if len(settings.Bindings) > 0 {
			detail.Bindings = parseBindings(settings.Bindings)
			detail.Fields = append(detail.Fields, DetailField{
				Label: "Bindings",
				Value: formatBindingsFromInfo(detail.Bindings),
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
			Value: fmt.Sprintf("Settings unavailable: %s", settingsErr),
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

// parseBindings converts safe binding structs into structured BindingInfo with navigation targets.
func parseBindings(bindings []safeBinding) []BindingInfo {
	var result []BindingInfo
	for _, b := range bindings {
		bi := BindingInfo{
			Name: b.Name,
			Type: b.Type,
		}

		switch bi.Type {
		case "kv_namespace":
			bi.TypeDisplay = "KV Namespace"
			bi.Detail = fmt.Sprintf("ns:%s", b.NamespaceID)
			bi.NavService = "KV"
			bi.NavResource = b.NamespaceID
		case "d1":
			bi.TypeDisplay = "D1 Database"
			bi.Detail = fmt.Sprintf("db:%s", b.ID)
			bi.NavService = "D1"
			bi.NavResource = b.ID
		case "r2_bucket":
			bi.TypeDisplay = "R2 Bucket"
			bi.Detail = b.BucketName
			bi.NavService = "R2"
			bi.NavResource = b.BucketName
		case "service":
			bi.TypeDisplay = "Service Binding"
			bi.Detail = b.Service
			bi.NavService = "Workers"
			bi.NavResource = b.Service
		case "durable_object_namespace":
			bi.TypeDisplay = "Durable Object"
			bi.Detail = b.ClassName
		case "queue":
			bi.TypeDisplay = "Queue"
			bi.Detail = b.QueueName
		case "hyperdrive":
			bi.TypeDisplay = "Hyperdrive"
			bi.Detail = b.ID
		case "ai":
			bi.TypeDisplay = "Workers AI"
			bi.Detail = "Workers AI"
		case "analytics_engine":
			bi.TypeDisplay = "Analytics Engine"
			bi.Detail = b.Dataset
		case "vectorize":
			bi.TypeDisplay = "Vectorize"
			bi.Detail = b.IndexName
		case "secret_text":
			bi.TypeDisplay = "Secret"
			bi.Detail = "(value hidden)"
		case "plain_text":
			bi.TypeDisplay = "Plain Text"
			bi.Detail = "(value hidden)"
		case "workflow":
			bi.TypeDisplay = "Workflow"
			bi.Detail = b.WorkflowName
		default:
			bi.TypeDisplay = bi.Type
			bi.Detail = bi.Type
		}

		result = append(result, bi)
	}
	return result
}

// formatBindingsFromInfo formats structured BindingInfo into a multi-line display string.
func formatBindingsFromInfo(bindings []BindingInfo) string {
	if len(bindings) == 0 {
		return "(none)"
	}
	var lines []string
	for _, b := range bindings {
		lines = append(lines, fmt.Sprintf("  %s [%s] %s", b.Name, b.Type, b.Detail))
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
