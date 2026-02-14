package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
)

// fetchStaleForSearch triggers background refreshes for any stale service caches so search
// can show a loading indicator. Returns the commands to run.
func (m *Model) fetchStaleForSearch() []tea.Cmd {
	var cmds []tea.Cmd
	for _, name := range m.registry.RegisteredNames() {
		if m.registry.IsCacheStale(name) {
			cmds = append(cmds, m.backgroundRefresh(name))
		}
	}
	m.search.SetFetching(len(cmds))
	return cmds
}

// navigateToService switches to a service list view, using cached data if available.
// If the cache is fresh (<CacheTTL), it is shown without a background refresh.
// If the cache is stale, it is shown immediately and a background refresh is triggered.
// If there is no cache, a loading spinner is shown while data is fetched.
func (m *Model) navigateToService(name string) tea.Cmd {
	// Stop any active tail/D1 session when switching services
	m.stopTail()
	m.detail.ClearD1()

	m.activeTab = tabbar.TabResources
	m.viewState = ViewServiceList
	m.detail.SetFocused(true)

	entry := m.registry.GetCache(name)
	if entry != nil {
		if !m.registry.IsCacheStale(name) {
			// Cache is fresh — show it without a background refresh
			m.detail.SetServiceFresh(name, entry.Resources)
			m.updateManagedResources()
			return nil
		}
		// Cache is stale — show it and trigger a background refresh
		cmd := m.detail.SetServiceWithCache(name, entry.Resources)
		m.updateManagedResources()
		if m.detail.IsLoading() {
			return tea.Batch(cmd, m.detail.SpinnerInit())
		}
		return cmd
	}
	// No cache at all — show loading spinner
	m.detail.SetService(name)
	return tea.Batch(
		tea.Cmd(func() tea.Msg { return detail.LoadResourcesMsg{ServiceName: name} }),
		m.detail.SpinnerInit(),
	)
}

// buildBindingIndexCmd returns a command that fetches settings for all Workers and builds
// a reverse binding index. This runs in the background after Workers are listed.
func (m Model) buildBindingIndexCmd() tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}
	return func() tea.Msg {
		idx := workersSvc.BuildBindingIndex()
		return bindingIndexBuiltMsg{
			index:     idx,
			accountID: accountID,
		}
	}
}

// getWorkersService retrieves the WorkersService from the registry (type-asserted).
func (m Model) getWorkersService() *svc.WorkersService {
	s := m.registry.Get("Workers")
	if s == nil {
		return nil
	}
	if ws, ok := s.(*svc.WorkersService); ok {
		return ws
	}
	return nil
}

// backgroundRefresh creates a command that fetches resources for a service in the background.
// Returns a BackgroundRefreshMsg instead of ResourcesLoadedMsg to avoid interfering with
// the normal load flow. Captures the current accountID so stale responses can be discarded.
func (m Model) backgroundRefresh(serviceName string) tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	return func() tea.Msg {
		s := m.registry.Get(serviceName)
		if s == nil {
			return detail.BackgroundRefreshMsg{
				ServiceName: serviceName,
				AccountID:   accountID,
				Resources:   nil,
				Err:         nil,
			}
		}

		resources, err := s.List()
		return detail.BackgroundRefreshMsg{
			ServiceName: serviceName,
			AccountID:   accountID,
			Resources:   resources,
			Err:         err,
		}
	}
}

// loadServiceResources creates a command that fetches resources from a registered service.
// Captures the current accountID so stale responses can be discarded.
func (m Model) loadServiceResources(serviceName string) tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	return func() tea.Msg {
		s := m.registry.Get(serviceName)
		if s == nil {
			return detail.ResourcesLoadedMsg{
				ServiceName:   serviceName,
				AccountID:     accountID,
				Resources:     nil,
				Err:           nil,
				NotIntegrated: true,
			}
		}

		resources, err := s.List()
		return detail.ResourcesLoadedMsg{
			ServiceName: serviceName,
			AccountID:   accountID,
			Resources:   resources,
			Err:         err,
		}
	}
}

// loadResourceDetail creates a command that fetches detail for a single resource.
func (m Model) loadResourceDetail(serviceName, resourceID string) tea.Cmd {
	return func() tea.Msg {
		s := m.registry.Get(serviceName)
		if s == nil {
			return detail.DetailLoadedMsg{
				ServiceName: serviceName,
				ResourceID:  resourceID,
				Detail:      nil,
				Err:         fmt.Errorf("service %s not available", serviceName),
			}
		}

		d, err := s.Get(resourceID)
		return detail.DetailLoadedMsg{
			ServiceName: serviceName,
			ResourceID:  resourceID,
			Detail:      d,
			Err:         err,
		}
	}
}

// registerServices creates and registers all service implementations for the given accountID.
// Clears any existing services first, then sets the registry's active account for caching.
// Also populates the Resources tab dropdown with the available services.
func (m *Model) registerServices(accountID string) {
	m.registry.ClearServices()
	m.registry.SetAccountID(accountID)

	workersSvc := svc.NewWorkersService(m.client.CF, accountID)
	m.registry.Register(workersSvc)

	kvSvc := svc.NewKVService(m.client.CF, accountID)
	m.registry.Register(kvSvc)

	r2Svc := svc.NewR2Service(m.client.CF, accountID)
	m.registry.Register(r2Svc)

	d1Svc := svc.NewD1Service(m.client.CF, accountID)
	m.registry.Register(d1Svc)

	queuesSvc := svc.NewQueueService(m.client.CF, accountID)
	m.registry.Register(queuesSvc)

	// Populate the Resources tab service dropdown
	m.detail.SetServices([]detail.ServiceEntry{
		{Name: "Workers", Integrated: true},
		{Name: "KV", Integrated: true},
		{Name: "R2", Integrated: true},
		{Name: "D1", Integrated: true},
		{Name: "Queues", Integrated: true},
		{Name: "Pages", Integrated: false},
		{Name: "Hyperdrive", Integrated: false},
	})
}

// switchAccount handles switching to a different account. Re-registers services with the
// new accountID. If currently viewing a service, reloads it with the new account's data.
func (m *Model) switchAccount(accountID, accountName string) tea.Cmd {
	// Stop any active tail session and wrangler command
	m.stopTail()
	m.stopAllParallelTails()
	m.monitoring.Clear()
	m.detail.ClearD1()
	m.stopWranglerRunner()
	m.wrangler.ClearVersionCache()
	m.wrangler.CloseVersionPicker()

	m.cfg.AccountID = accountID
	m.registerServices(accountID)

	// Update search items with whatever is cached for this account
	m.search.SetItems(m.registry.AllSearchItems())

	// Clear stale deployment data, restore from cache if available, then refresh in background
	m.wrangler.ClearDeployments()
	m.restoreDeploymentsFromCache()
	var deployCmd tea.Cmd
	if m.wrangler.IsMonorepo() {
		deployCmd = m.fetchAllProjectDeployments(true)
	} else if cfg := m.wrangler.Config(); cfg != nil {
		deployCmd = m.fetchSingleProjectDeployments(cfg, true)
	}

	// If we're viewing a service, reload it with the new account
	if m.viewState == ViewServiceList || m.viewState == ViewServiceDetail {
		serviceName := m.detail.Service()
		m.detail.ResetService()
		m.viewState = ViewServiceList // drop back to list on account switch

		entry := m.registry.GetCache(serviceName)
		if entry != nil {
			m.detail.SetServiceWithCache(serviceName, entry.Resources)
		} else {
			m.detail.SetService(serviceName)
		}

		loadCmd := tea.Cmd(func() tea.Msg {
			return detail.LoadResourcesMsg{ServiceName: serviceName}
		})
		return tea.Batch(loadCmd, m.detail.SpinnerInit(), deployCmd)
	}

	return deployCmd
}

// navigateTo navigates directly to a specific resource's detail view.
func (m *Model) navigateTo(serviceName, resourceID string) tea.Cmd {
	m.activeTab = tabbar.TabResources
	m.viewState = ViewServiceDetail
	m.detail.SetFocused(true)

	// Set the service on the detail panel (loads the list in background), using cache if available
	var loadCmd tea.Cmd
	entry := m.registry.GetCache(serviceName)
	if entry != nil {
		loadCmd = m.detail.SetServiceWithCache(serviceName, entry.Resources)
	} else {
		loadCmd = m.detail.SetService(serviceName)
	}

	// Switch detail panel directly to detail view and load the specific resource
	m.detail.NavigateToDetail(resourceID)
	detailCmd := m.loadResourceDetail(serviceName, resourceID)

	return tea.Batch(loadCmd, detailCmd)
}

// --- D1 SQL console helpers ---

// executeD1Query returns a command that runs a SQL query against a D1 database.
func (m Model) executeD1Query(databaseID, sql string) tea.Cmd {
	d1Svc := m.getD1Service()
	if d1Svc == nil {
		return func() tea.Msg {
			return detail.D1QueryResultMsg{Err: fmt.Errorf("D1 service not available")}
		}
	}
	return func() tea.Msg {
		result, err := d1Svc.ExecuteQuery(databaseID, sql)
		return detail.D1QueryResultMsg{Result: result, Err: err}
	}
}

// loadD1Schema returns a command that loads the schema for a D1 database.
func (m Model) loadD1Schema(databaseID string) tea.Cmd {
	d1Svc := m.getD1Service()
	if d1Svc == nil {
		return func() tea.Msg {
			return detail.D1SchemaLoadedMsg{DatabaseID: databaseID, Err: fmt.Errorf("D1 service not available")}
		}
	}
	return func() tea.Msg {
		tables, err := d1Svc.QuerySchema(databaseID)
		return detail.D1SchemaLoadedMsg{DatabaseID: databaseID, Tables: tables, Err: err}
	}
}

// getD1Service retrieves the D1Service from the registry (type-asserted).
func (m Model) getD1Service() *svc.D1Service {
	s := m.registry.Get("D1")
	if s == nil {
		return nil
	}
	if d1s, ok := s.(*svc.D1Service); ok {
		return d1s
	}
	return nil
}

// updateManagedResources computes which resources in the current service are
// wrangler-managed (bound to a Worker via the binding index) and updates the
// detail model's managed set. This affects the white/dim color coding in the list.
func (m *Model) updateManagedResources() {
	serviceName := m.detail.Service()
	if serviceName == "" {
		m.detail.SetManagedResources(nil)
		return
	}

	// For Workers: "managed" means the worker appears in a wrangler config.
	// Cross-reference the API list with wrangler's WorkerList().
	if serviceName == "Workers" {
		wranglerWorkers := m.wrangler.WorkerList()
		if len(wranglerWorkers) == 0 {
			m.detail.SetManagedResources(nil)
			return
		}
		wranglerNames := make(map[string]bool, len(wranglerWorkers))
		for _, w := range wranglerWorkers {
			wranglerNames[w.ScriptName] = true
		}
		managed := make(map[string]bool)
		entry := m.registry.GetCache(serviceName)
		if entry != nil {
			for _, r := range entry.Resources {
				if wranglerNames[r.ID] {
					managed[r.ID] = true
				}
			}
		}
		m.detail.SetManagedResources(managed)
		return
	}

	// For other services: "managed" means bound to a Worker via the binding index.
	idx := m.registry.GetBindingIndex()
	if idx == nil {
		m.detail.SetManagedResources(nil)
		return
	}

	entry := m.registry.GetCache(serviceName)
	if entry == nil {
		m.detail.SetManagedResources(nil)
		return
	}

	managed := make(map[string]bool)
	for _, r := range entry.Resources {
		// Check by resource ID first, then by Name (some services like Queues
		// use a UUID as ID but the binding index stores the human-readable name)
		if bound := idx.Lookup(serviceName, r.ID); len(bound) > 0 {
			managed[r.ID] = true
		} else if r.Name != r.ID {
			if bound := idx.Lookup(serviceName, r.Name); len(bound) > 0 {
				managed[r.ID] = true
			}
		}
	}
	m.detail.SetManagedResources(managed)
}

// enrichDetailWithBoundWorkers appends a "Worker(s)" field to a resource detail
// if any Workers reference this resource via bindings. Also populates the detail's
// Bindings field with reverse references so the action popup can navigate to them.
func (m Model) enrichDetailWithBoundWorkers(detail *svc.ResourceDetail, serviceName, resourceID string) {
	idx := m.registry.GetBindingIndex()
	if idx == nil {
		return
	}
	bound := idx.Lookup(serviceName, resourceID)
	if len(bound) == 0 {
		return
	}

	// Build display value: comma-separated worker names
	var names []string
	for _, bw := range bound {
		names = append(names, fmt.Sprintf("%s (as %s)", bw.ScriptName, bw.BindingName))
	}
	detail.Fields = append(detail.Fields, svc.DetailField{
		Label: "Worker(s)",
		Value: strings.Join(names, ", "),
	})

	// Store as reverse bindings so the action popup can navigate to them
	for _, bw := range bound {
		detail.Bindings = append(detail.Bindings, svc.BindingInfo{
			Name:        bw.ScriptName,
			Type:        "worker_ref",
			TypeDisplay: fmt.Sprintf("bound as %s", bw.BindingName),
			NavService:  "Workers",
			NavResource: bw.ScriptName,
		})
	}
}
