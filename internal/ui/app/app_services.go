package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
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
	// Stop any active tail/D1/KV/Queue session when switching services
	m.stopTail()
	m.detail.ClearD1()
	m.detail.ClearKV()
	m.detail.ClearQueue()
	m.detail.ClearQueueCache()

	m.activeTab = tabbar.TabResources
	m.viewState = ViewServiceList
	m.detail.SetFocused(true)

	entry := m.registry.GetCache(name)
	if entry != nil {
		if !m.registry.IsCacheStale(name) {
			// Cache is fresh — show it without a background refresh
			previewCmd := m.detail.SetServiceFresh(name, entry.Resources)
			m.updateManagedResources()
			if previewCmd != nil {
				return tea.Batch(previewCmd, m.detail.SpinnerInit())
			}
			return nil
		}
		// Cache is stale — show it and trigger a background refresh
		refreshCmd, previewCmd := m.detail.SetServiceWithCache(name, entry.Resources)
		m.updateManagedResources()
		cmds := []tea.Cmd{refreshCmd}
		if previewCmd != nil {
			cmds = append(cmds, previewCmd)
		}
		if m.detail.IsLoading() {
			cmds = append(cmds, m.detail.SpinnerInit())
		}
		return tea.Batch(cmds...)
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

// buildAccessIndexCmd returns a command that fetches Access Applications and custom
// domains, then builds a reverse index mapping Workers to their Access policies.
// This runs in the background after Workers are listed, alongside the binding index.
func (m Model) buildAccessIndexCmd() tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}
	return func() tea.Msg {
		idx := workersSvc.BuildAccessIndex()
		return accessIndexBuiltMsg{
			index:     idx,
			accountID: accountID,
		}
	}
}

// buildBuildsIndexCmd returns a command that checks which Workers in the current
// project(s) have CI/CD triggers via the Workers Builds API. The result is a
// BuildsIndex stored in the registry, used to display ⟳ badges on the Operations tab.
func (m Model) buildBuildsIndexCmd() tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	client := m.getBuildsClient()
	if client == nil {
		return nil
	}

	// Collect the unique worker script names from wrangler configs.
	workers := m.wrangler.WorkerList()
	if len(workers) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var scriptNames []string
	for _, w := range workers {
		if !seen[w.ScriptName] {
			seen[w.ScriptName] = true
			scriptNames = append(scriptNames, w.ScriptName)
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		idx := svc.NewBuildsIndex()
		for _, name := range scriptNames {
			// Resolve the script tag — the canonical identifier used by the
			// Builds API (both dashboard and our flow create triggers with it).
			tag, tagErr := client.GetScriptTag(ctx, name)
			if tagErr != nil {
				continue
			}
			triggers, err := client.GetWorkerTriggers(ctx, tag)
			if err != nil {
				continue
			}
			if len(triggers) > 0 {
				idx.SetConnected(name)
			}
		}
		return buildsIndexBuiltMsg{index: idx, accountID: accountID}
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
	// Configure Access API auth — OAuth tokens lack the required scope,
	// so we use fallback credentials when available.
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		// Global API Key has all permissions
		workersSvc.SetAccessAuth(m.cfg.Email, m.cfg.APIKey, "")
	case config.AuthMethodAPIToken:
		// API Token might have Access scope — try it (silent fallback on 403)
		workersSvc.SetAccessAuth("", "", m.cfg.APIToken)
	case config.AuthMethodOAuth:
		// OAuth can't access the Access API — use fallback credentials.
		// Do NOT use API Key from env vars here — it may be scoped to a
		// different account and would cause 403 errors on account switch.
		if ft := m.cfg.FallbackTokenFor(accountID); ft != "" {
			workersSvc.SetAccessAuth("", "", ft)
		}
		// else: no access credentials → Access badges silently disabled
	}
	m.registry.Register(workersSvc)

	kvSvc := svc.NewKVService(m.client.CF, accountID)
	m.registry.Register(kvSvc)

	r2Svc := svc.NewR2Service(m.client.CF, accountID)
	m.registry.Register(r2Svc)

	d1Svc := svc.NewD1Service(m.client.CF, accountID)
	m.registry.Register(d1Svc)

	queuesSvc := svc.NewQueueService(m.client.CF, accountID)
	m.registry.Register(queuesSvc)

	// Register services backed by raw HTTP (ResourceListClient)
	rlc := m.newResourceListClient()
	if rlc != nil {
		vectorizeSvc := svc.NewVectorizeService(rlc)
		m.registry.Register(vectorizeSvc)

		hyperdriveSvc := svc.NewHyperdriveService(rlc)
		m.registry.Register(hyperdriveSvc)

	}

	// Populate the Resources tab service dropdown
	m.detail.SetServices([]detail.ServiceEntry{
		{Name: "Workers", Integrated: true, Mode: detail.ReadOnly},
		{Name: "KV", Integrated: true, Mode: detail.ReadWrite},
		{Name: "R2", Integrated: true, Mode: detail.ReadOnly},
		{Name: "D1", Integrated: true, Mode: detail.ReadWrite},
		{Name: "Queues", Integrated: true, Mode: detail.ReadWrite},
		{Name: "Vectorize", Integrated: true, Mode: detail.ReadOnly},
		{Name: "Hyperdrive", Integrated: true, Mode: detail.ReadOnly},
		{Name: "Pages", Integrated: false, Mode: detail.ReadOnly},
	})
}

// switchAccount handles switching to a different account. Re-registers services with the
// new accountID. If currently viewing a service, reloads it with the new account's data.
func (m *Model) switchAccount(accountID, accountName string) tea.Cmd {
	// Stop any active tail session and wrangler command
	m.stopTail()
	m.stopAllParallelTails()
	m.cleanupAllDevSessions()
	// Stop all command runners
	for key := range m.cmdRunners {
		m.stopCmdRunner(key)
	}
	m.monitoring.Clear()
	m.detail.ClearD1()
	m.detail.ClearKV()
	m.detail.ClearQueue()
	m.detail.ClearQueueCache()
	m.wrangler.ClearVersionCache()
	m.wrangler.CloseVersionPicker()

	m.cfg.AccountID = accountID
	m.registerServices(accountID)

	// Update restricted badge — provisioning may be needed for the new account
	restricted := m.cfg.AuthMethod == config.AuthMethodOAuth && !m.cfg.HasFallbackAuthFor(accountID)
	m.header.SetRestricted(restricted)

	// Show restricted-mode toast once per account
	var toastCmd tea.Cmd
	if restricted && !m.restrictedToastShown[accountID] {
		m.restrictedToastShown[accountID] = true
		m.setToast("Restricted mode — some features need a fallback token")
		toastCmd = toastTick()
	}

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

	// Auto-provision fallback token for the new account if needed
	var provisionCmd tea.Cmd
	if m.needsFallbackTokenProvisioning() {
		provisionCmd = m.provisionFallbackTokenCmd()
	}

	// If we're viewing a service, reload it with the new account
	if m.viewState == ViewServiceList || m.viewState == ViewServiceDetail {
		serviceName := m.detail.Service()
		m.detail.ResetService()
		m.viewState = ViewServiceList // drop back to list on account switch

		entry := m.registry.GetCache(serviceName)
		var previewCmd tea.Cmd
		if entry != nil {
			_, previewCmd = m.detail.SetServiceWithCache(serviceName, entry.Resources)
		} else {
			m.detail.SetService(serviceName)
		}

		loadCmd := tea.Cmd(func() tea.Msg {
			return detail.LoadResourcesMsg{ServiceName: serviceName}
		})
		cmds := []tea.Cmd{loadCmd, m.detail.SpinnerInit(), deployCmd}
		if previewCmd != nil {
			cmds = append(cmds, previewCmd)
		}
		if provisionCmd != nil {
			cmds = append(cmds, provisionCmd)
		}
		if toastCmd != nil {
			cmds = append(cmds, toastCmd)
		}
		return tea.Batch(cmds...)
	}

	cmds := []tea.Cmd{deployCmd}
	if provisionCmd != nil {
		cmds = append(cmds, provisionCmd)
	}
	if toastCmd != nil {
		cmds = append(cmds, toastCmd)
	}
	return tea.Batch(cmds...)
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
		// Some binding types (e.g. Queues) store a resource Name rather than a UUID
		// as their ResourceID. Resolve the name to the real ID using the cache.
		resourceID = m.resolveResourceID(entry.Resources, resourceID)
		loadCmd, _ = m.detail.SetServiceWithCache(serviceName, entry.Resources)
	} else {
		loadCmd = m.detail.SetService(serviceName)
	}

	// Switch detail panel directly to detail view and load the specific resource
	// (overrides auto-preview — we want this specific resource, not the first one)
	m.detail.NavigateToDetail(resourceID)
	detailCmd := m.loadResourceDetail(serviceName, resourceID)

	return tea.Batch(loadCmd, detailCmd)
}

// resolveResourceID checks whether resourceID matches a resource by ID. If not,
// it tries to match by Name (some bindings like Queues store the name, not the UUID).
// Returns the resolved UUID if found, or the original string otherwise.
func (m Model) resolveResourceID(resources []svc.Resource, resourceID string) string {
	// First check if it already matches an ID directly
	for _, r := range resources {
		if r.ID == resourceID {
			return resourceID
		}
	}
	// Fall back to matching by Name
	for _, r := range resources {
		if r.Name == resourceID {
			return r.ID
		}
	}
	return resourceID
}

// --- KV Data Explorer helpers ---

// loadKVKeys returns a command that fetches keys (with values) from a KV namespace.
func (m Model) loadKVKeys(namespaceID, prefix string) tea.Cmd {
	kvSvc := m.getKVService()
	if kvSvc == nil {
		return func() tea.Msg {
			return detail.KVKeysLoadedMsg{NamespaceID: namespaceID, Err: fmt.Errorf("KV service not available")}
		}
	}
	return func() tea.Msg {
		keys, err := kvSvc.ListKeysWithValues(namespaceID, prefix, 20)
		return detail.KVKeysLoadedMsg{NamespaceID: namespaceID, Keys: keys, Err: err}
	}
}

// getKVService retrieves the KVService from the registry (type-asserted).
func (m Model) getKVService() *svc.KVService {
	s := m.registry.Get("KV")
	if s == nil {
		return nil
	}
	if kvs, ok := s.(*svc.KVService); ok {
		return kvs
	}
	return nil
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

// --- Queue Message Inspector helpers ---

// pullQueueMessages returns a command that pulls a message snapshot from a queue.
func (m Model) pullQueueMessages(queueID string, batchSize int) tea.Cmd {
	qSvc := m.getQueueService()
	if qSvc == nil {
		return func() tea.Msg {
			return detail.QueuePullResultMsg{QueueID: queueID, Err: fmt.Errorf("Queue service not available")}
		}
	}
	return func() tea.Msg {
		result, err := qSvc.PullMessages(queueID, batchSize)
		return detail.QueuePullResultMsg{QueueID: queueID, Result: result, Err: err}
	}
}

// loadQueueConsumers returns a command that loads consumers for a queue.
func (m Model) loadQueueConsumers(queueID string) tea.Cmd {
	qSvc := m.getQueueService()
	if qSvc == nil {
		return func() tea.Msg {
			return detail.QueueConsumersLoadedMsg{QueueID: queueID, Err: fmt.Errorf("Queue service not available")}
		}
	}
	return func() tea.Msg {
		consumers, err := qSvc.ListConsumers(queueID)
		return detail.QueueConsumersLoadedMsg{QueueID: queueID, Consumers: consumers, Err: err}
	}
}

// pushQueueMessage returns a command that pushes a message to a queue.
func (m Model) pushQueueMessage(queueID, body string) tea.Cmd {
	qSvc := m.getQueueService()
	if qSvc == nil {
		return func() tea.Msg {
			return detail.QueuePushResultMsg{Err: fmt.Errorf("Queue service not available")}
		}
	}
	return func() tea.Msg {
		err := qSvc.PushMessage(queueID, body)
		return detail.QueuePushResultMsg{Body: body, Err: err}
	}
}

// getQueueService retrieves the QueueService from the registry (type-asserted).
func (m Model) getQueueService() *svc.QueueService {
	s := m.registry.Get("Queues")
	if s == nil {
		return nil
	}
	if qs, ok := s.(*svc.QueueService); ok {
		return qs
	}
	return nil
}

// enableHTTPPull returns a Cmd that adds an HTTP pull consumer to a queue.
func (m Model) enableHTTPPull(queueID string) tea.Cmd {
	qSvc := m.getQueueService()
	if qSvc == nil {
		return func() tea.Msg {
			return detail.QueueHTTPPullEnabledMsg{QueueID: queueID, Err: fmt.Errorf("queue service not available")}
		}
	}
	return func() tea.Msg {
		err := qSvc.EnableHTTPPull(queueID)
		return detail.QueueHTTPPullEnabledMsg{QueueID: queueID, Err: err}
	}
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

// handleFallbackTokenMsg handles the result of auto-provisioning a scoped API token.
// On success: saves the token to config, re-wires WorkersService access auth,
// updates the header restricted badge, and triggers an access index rebuild.
// On failure: silently ignores (restricted mode continues).
func (m *Model) handleFallbackTokenMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	ftMsg, ok := msg.(fallbackTokenProvisionedMsg)
	if !ok {
		return *m, nil, false
	}

	if m.isStaleAccount(ftMsg.accountID) {
		return *m, nil, true
	}

	if ftMsg.err != nil {
		// Silent failure — restricted mode continues, no user-facing error
		return *m, nil, true
	}

	// Save the provisioned token to config (per-account)
	m.cfg.SetFallbackToken(ftMsg.accountID, ftMsg.token)
	if ftMsg.tokenID != "" {
		m.cfg.SetFallbackTokenID(ftMsg.accountID, ftMsg.tokenID)
	}
	_ = m.cfg.Save() // best-effort persist

	// Re-wire WorkersService access auth with the new token
	if ws := m.getWorkersService(); ws != nil {
		ws.SetAccessAuth("", "", ftMsg.token)
	}

	// Update header restricted badge (no longer restricted)
	m.header.SetRestricted(false)

	// Trigger access and builds index rebuild now that we have credentials
	var cmds []tea.Cmd
	if cmd := m.buildAccessIndexCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.buildBuildsIndexCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) > 0 {
		return *m, tea.Batch(cmds...), true
	}
	return *m, nil, true
}

// needsFallbackTokenProvisioning returns true when the app should attempt
// to auto-provision a scoped API token. The fallback token is a Bearer token
// with specific scopes (Access Read, Workers CI Read/Write, Analytics Read).
// It is needed for APIs that don't accept Global API Key auth (e.g. Workers
// Builds) and for OAuth which lacks these scopes entirely.
// Conditions:
//   - No fallback token exists for the active account
//   - Global API Key + Email are available (needed to create the scoped token)
func (m Model) needsFallbackTokenProvisioning() bool {
	activeAccountID := m.registry.ActiveAccountID()
	hasFT := m.cfg.FallbackTokenFor(activeAccountID) != ""
	hasKey := m.cfg.APIKey != "" && m.cfg.Email != ""
	needs := !hasFT && hasKey
	return needs
}

// provisionFallbackTokenCmd creates a background command that auto-provisions a
// scoped API token with Access Apps Read, Workers CI Read/Write, and Analytics
// Read permissions.
func (m Model) provisionFallbackTokenCmd() tea.Cmd {
	email := m.cfg.Email
	apiKey := m.cfg.APIKey
	accountID := m.registry.ActiveAccountID()
	return func() tea.Msg {
		result, err := api.CreateScopedToken(context.Background(), email, apiKey, accountID)
		return fallbackTokenProvisionedMsg{
			token:     result.Value,
			tokenID:   result.ID,
			accountID: accountID,
			err:       err,
		}
	}
}

// --- Local emulator helpers ---

// executeLocalD1Query returns a command that runs a SQL query against a local D1 database
// via the wrangler CLI (npx wrangler d1 execute --local).
func (m Model) executeLocalD1Query(lr wcfg.LocalResource, sql string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		localResult, err := wcfg.ExecuteLocalD1Query(ctx, lr, sql)
		if err != nil {
			return detail.LocalD1QueryResultMsg{Err: err}
		}

		// Convert wrangler.LocalD1QueryResult → service.D1QueryResult
		var output string
		if len(localResult.Columns) > 0 {
			output = svc.FormatASCIITable(localResult.Columns, localResult.Rows)
		} else if localResult.ChangedDB {
			output = "Query OK"
		} else {
			output = "(empty result)"
		}

		return detail.LocalD1QueryResultMsg{
			Result: &svc.D1QueryResult{
				Output:    output,
				Meta:      localResult.Meta,
				ChangedDB: localResult.ChangedDB,
			},
		}
	}
}

// loadLocalD1Schema returns a command that introspects a local D1 database schema
// via the wrangler CLI (sqlite_master + PRAGMA table_info + PRAGMA foreign_key_list).
func (m Model) loadLocalD1Schema(lr wcfg.LocalResource, databaseID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		localTables, err := wcfg.QueryLocalD1Schema(ctx, lr)
		if err != nil {
			return detail.D1SchemaLoadedMsg{DatabaseID: databaseID, Err: err}
		}

		// Convert wrangler.LocalSchemaTable → service.SchemaTable
		tables := make([]svc.SchemaTable, 0, len(localTables))
		for _, lt := range localTables {
			st := svc.SchemaTable{Name: lt.Name}
			for _, lc := range lt.Columns {
				st.Columns = append(st.Columns, svc.SchemaColumn{
					Name:    lc.Name,
					Type:    lc.Type,
					NotNull: lc.NotNull,
					PK:      lc.PK,
				})
			}
			for _, lf := range lt.FKs {
				st.FKs = append(st.FKs, svc.SchemaFK{
					FromCol: lf.FromCol,
					ToTable: lf.ToTable,
					ToCol:   lf.ToCol,
				})
			}
			tables = append(tables, st)
		}

		return detail.D1SchemaLoadedMsg{DatabaseID: databaseID, Tables: tables}
	}
}

// loadLocalKVKeys returns a command that fetches keys (with values) from a local KV namespace
// via the wrangler CLI (npx wrangler kv key list/get --local).
func (m Model) loadLocalKVKeys(lr wcfg.LocalResource, prefix string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		entries, err := wcfg.ListLocalKVKeysWithValues(ctx, lr, prefix, 20)
		if err != nil {
			return detail.LocalKVKeysLoadedMsg{
				BindingName: lr.BindingName,
				Err:         err,
			}
		}

		// Convert wrangler.LocalKVKeyValueEntry → service.KVKeyEntry
		kvKeys := make([]svc.KVKeyEntry, 0, len(entries))
		for _, e := range entries {
			kvKeys = append(kvKeys, svc.KVKeyEntry{
				Name:       e.Name,
				Value:      e.Value,
				Expiration: e.Expiration,
				ValueSize:  e.ValueSize,
				IsBinary:   false, // local CLI returns text
			})
		}

		return detail.LocalKVKeysLoadedMsg{
			BindingName: lr.BindingName,
			Keys:        kvKeys,
		}
	}
}
