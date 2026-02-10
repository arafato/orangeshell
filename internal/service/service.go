package service

import "time"

// Resource represents a single instance of a Cloudflare service (e.g. one Worker, one KV namespace).
// Used for listing and search results.
type Resource struct {
	ID          string    // Primary identifier (e.g. worker script name)
	Name        string    // Human-readable name
	ServiceType string    // Service category (e.g. "Workers", "KV")
	ModifiedAt  time.Time // Last modified timestamp
	Summary     string    // Short one-line summary for list views
}

// ResourceDetail holds the full detail for a single resource, rendered in the detail drill-down view.
type ResourceDetail struct {
	Resource
	Fields       []DetailField // Ordered list of key-value fields to display
	ExtraContent string        // Optional multi-line content rendered below fields (e.g. schema diagram)
	Bindings     []BindingInfo // Structured binding data (Workers only, for navigation)
}

// DetailField is a single labeled value in a detail view.
type DetailField struct {
	Label string
	Value string
}

// BindingInfo describes a single resource binding on a Worker.
type BindingInfo struct {
	Name        string // JS variable name (e.g. "MY_KV")
	Type        string // Raw binding type (e.g. "kv_namespace")
	TypeDisplay string // Human-readable type (e.g. "KV Namespace")
	Detail      string // Type-specific detail (e.g. "ns:abc123")
	NavService  string // Target service name for navigation (empty if non-navigable)
	NavResource string // Target resource ID for navigation
}

// BoundWorker represents a Worker that references a resource via a binding.
type BoundWorker struct {
	ScriptName  string // Worker script name (= its Resource.ID)
	BindingName string // JS variable name used in the worker code (e.g. "MY_KV")
}

// BindingIndex is a reverse lookup from resources to the Workers that bind to them.
// Key format: "ServiceName:ResourceID" (e.g. "KV:abc-123-uuid").
type BindingIndex struct {
	index map[string][]BoundWorker
}

// NewBindingIndex creates an empty binding index.
func NewBindingIndex() *BindingIndex {
	return &BindingIndex{index: make(map[string][]BoundWorker)}
}

// Add records that a Worker binds to a resource.
func (bi *BindingIndex) Add(serviceName, resourceID, scriptName, bindingName string) {
	key := serviceName + ":" + resourceID
	bi.index[key] = append(bi.index[key], BoundWorker{
		ScriptName:  scriptName,
		BindingName: bindingName,
	})
}

// Lookup returns all Workers that bind to the given resource, or nil.
func (bi *BindingIndex) Lookup(serviceName, resourceID string) []BoundWorker {
	if bi == nil {
		return nil
	}
	return bi.index[serviceName+":"+resourceID]
}

// Service defines the interface that every Cloudflare service integration must implement.
type Service interface {
	// Name returns the display name of the service (e.g. "Workers").
	Name() string

	// List fetches all resources for this service in the account.
	// Returns a slice of Resource items for the list view.
	List() ([]Resource, error)

	// Get fetches full details for a single resource by its ID.
	Get(id string) (*ResourceDetail, error)

	// SearchItems returns all resources that could match in a fuzzy search.
	// This is typically the same data as List, but preloaded or cached.
	SearchItems() []Resource
}

// CacheEntry holds cached resource list data for a single service.
type CacheEntry struct {
	Resources []Resource
	FetchedAt time.Time
}

// Registry holds all registered service implementations, keyed by sidebar name,
// and an in-memory session cache of resource lists per service per account.
// Caches are retained across account switches so switching back is instant.
type Registry struct {
	services  map[string]Service
	order     []string // insertion order for sidebar display
	accountID string   // currently active account

	// Per-account caches: accountID → serviceName → CacheEntry
	accountCaches map[string]map[string]*CacheEntry

	// Per-account binding indexes: accountID → BindingIndex
	bindingIndexes map[string]*BindingIndex
}

// NewRegistry creates an empty service registry.
func NewRegistry() *Registry {
	return &Registry{
		services:       make(map[string]Service),
		accountCaches:  make(map[string]map[string]*CacheEntry),
		bindingIndexes: make(map[string]*BindingIndex),
	}
}

// Register adds a service to the registry.
func (r *Registry) Register(svc Service) {
	name := svc.Name()
	if _, exists := r.services[name]; !exists {
		r.order = append(r.order, name)
	}
	r.services[name] = svc
}

// Get returns a service by name, or nil if not registered.
func (r *Registry) Get(name string) Service {
	return r.services[name]
}

// ActiveAccountID returns the currently active account ID.
func (r *Registry) ActiveAccountID() string {
	return r.accountID
}

// Names returns all registered service names in order.
func (r *Registry) Names() []string {
	return r.order
}

// SetAccountID sets the active account for cache operations.
func (r *Registry) SetAccountID(accountID string) {
	r.accountID = accountID
	// Ensure a cache map exists for this account
	if _, ok := r.accountCaches[accountID]; !ok {
		r.accountCaches[accountID] = make(map[string]*CacheEntry)
	}
}

// cache returns the cache map for the active account.
func (r *Registry) cache() map[string]*CacheEntry {
	c, ok := r.accountCaches[r.accountID]
	if !ok {
		c = make(map[string]*CacheEntry)
		r.accountCaches[r.accountID] = c
	}
	return c
}

// GetCache returns the cached resources for a service in the active account, or nil if not cached.
func (r *Registry) GetCache(serviceName string) *CacheEntry {
	return r.cache()[serviceName]
}

// SetCache stores resources in the session cache for a service in the active account.
func (r *Registry) SetCache(serviceName string, resources []Resource) {
	r.cache()[serviceName] = &CacheEntry{
		Resources: resources,
		FetchedAt: time.Now(),
	}
}

// ClearServices removes all registered services but keeps cached data.
// Used when switching accounts — services will be re-registered with the new accountID.
func (r *Registry) ClearServices() {
	r.services = make(map[string]Service)
	r.order = nil
}

// RegisteredNames returns all registered (integrated) service names.
func (r *Registry) RegisteredNames() []string {
	var names []string
	for _, name := range r.order {
		names = append(names, name)
	}
	return names
}

// AllSearchItems returns all searchable resources across all services.
// Uses the session cache so search results are available immediately.
func (r *Registry) AllSearchItems() []Resource {
	var all []Resource
	c := r.cache()
	for _, name := range r.order {
		if entry := c[name]; entry != nil {
			all = append(all, entry.Resources...)
		} else {
			// Fall back to service's own cache (e.g. WorkersService.cached)
			svc := r.services[name]
			all = append(all, svc.SearchItems()...)
		}
	}
	return all
}

// GetBindingIndex returns the binding index for the active account, or nil.
func (r *Registry) GetBindingIndex() *BindingIndex {
	return r.bindingIndexes[r.accountID]
}

// SetBindingIndex stores a binding index for the active account.
func (r *Registry) SetBindingIndex(idx *BindingIndex) {
	r.bindingIndexes[r.accountID] = idx
}

// HasCacheForAccount returns whether any cached data exists for the given account.
func (r *Registry) HasCacheForAccount(accountID string) bool {
	c, ok := r.accountCaches[accountID]
	return ok && len(c) > 0
}
