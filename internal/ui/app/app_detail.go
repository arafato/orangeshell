package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
)

// accessIndexBuiltMsg is sent when the background Access index build completes.
type accessIndexBuiltMsg struct {
	index     *svc.AccessIndex
	accountID string
}

// handleDetailMsg handles all detail/resource panel messages.
// Returns (model, cmd, handled).
func (m *Model) handleDetailMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	// --- Service data messages ---

	case detail.ResourcesLoadedMsg:
		if m.isStaleAccount(msg.AccountID) {
			return *m, nil, true
		}
		// Cache the result regardless of which service is displayed
		if msg.Err == nil && msg.Resources != nil {
			m.registry.SetCache(msg.ServiceName, msg.Resources)
		}
		// When Workers list loads, build the binding index and access index in the background.
		// When a non-Workers service loads and no binding index exists yet,
		// trigger a Workers fetch + index build so managed detection works.
		var indexCmd tea.Cmd
		var accessCmd tea.Cmd
		if msg.ServiceName == "Workers" && msg.Err == nil && msg.Resources != nil {
			indexCmd = m.buildBindingIndexCmd()
			accessCmd = m.buildAccessIndexCmd()
		} else if msg.ServiceName != "Workers" && msg.Err == nil && m.registry.GetBindingIndex() == nil {
			// Binding index not yet available — kick off Workers fetch + build
			if m.registry.GetCache("Workers") == nil {
				indexCmd = m.loadServiceResources("Workers")
			} else {
				indexCmd = m.buildBindingIndexCmd()
			}
		}
		// Staleness check: ignore if the user has already switched services
		if msg.ServiceName != m.detail.Service() {
			// Still update search items even if not the active service
			if msg.Err == nil {
				m.search.SetItems(m.registry.AllSearchItems())
			}
			var bgCmds []tea.Cmd
			if indexCmd != nil {
				bgCmds = append(bgCmds, indexCmd)
			}
			if accessCmd != nil {
				bgCmds = append(bgCmds, accessCmd)
			}
			if len(bgCmds) > 0 {
				return *m, tea.Batch(bgCmds...), true
			}
			return *m, nil, true
		}
		previewCmd := m.detail.SetResources(msg.Resources, msg.Err, msg.NotIntegrated)
		// Update managed resource highlighting
		m.updateManagedResources()
		// Sync viewState: auto-preview switches detail model to viewDetail
		if m.detail.InDetailView() {
			m.viewState = ViewServiceDetail
		}
		// Update search items after loading
		if msg.Err == nil {
			m.search.SetItems(m.registry.AllSearchItems())
		}
		var cmds []tea.Cmd
		if previewCmd != nil {
			cmds = append(cmds, previewCmd, m.detail.SpinnerInit())
		}
		if indexCmd != nil {
			cmds = append(cmds, indexCmd)
		}
		if accessCmd != nil {
			cmds = append(cmds, accessCmd)
		}
		if len(cmds) > 0 {
			return *m, tea.Batch(cmds...), true
		}
		return *m, nil, true

	case detail.SelectServiceMsg:
		// User selected a service from the dropdown — navigate to it
		cmd := m.navigateToService(msg.ServiceName)
		return *m, cmd, true

	case detail.LoadResourcesMsg:
		// Don't attempt to load if auth hasn't completed yet — services aren't
		// registered. The initDashboardMsg handler will trigger the load once
		// the client is ready.
		if m.client == nil {
			return *m, nil, true
		}
		return *m, m.loadServiceResources(msg.ServiceName), true

	case detail.LoadDetailMsg:
		if msg.ServiceName == "Env Variables" {
			m.detail.BackToList()
			return *m, m.navigateToEnvVars(msg.ResourceID), true
		}
		if msg.ServiceName == "Triggers" {
			m.detail.BackToList()
			return *m, m.navigateToTriggers(msg.ResourceID), true
		}
		m.viewState = ViewServiceDetail
		return *m, tea.Batch(m.loadResourceDetail(msg.ServiceName, msg.ResourceID), m.detail.SpinnerInit()), true

	case detail.DetailLoadedMsg:
		// Staleness check: ignore if the user has switched services or resources
		if msg.ServiceName != m.detail.Service() {
			return *m, nil, true
		}
		// Enrich non-Workers detail with bound worker references
		if msg.Err == nil && msg.Detail != nil && msg.ServiceName != "Workers" {
			m.enrichDetailWithBoundWorkers(msg.Detail, msg.ServiceName, msg.ResourceID)
		}
		// Enrich Workers detail with Access protection info
		if msg.Err == nil && msg.Detail != nil && msg.ServiceName == "Workers" {
			m.enrichDetailWithAccessInfo(msg.Detail, msg.ResourceID)
		}
		m.detail.SetDetail(msg.Detail, msg.Err)
		// KV explorer initialization is deferred to interactive mode (EnterInteractiveMsg).
		// No preview data is fetched for KV — just show the "press enter" hint.

		// D1 console initialization is deferred to interactive mode (EnterInteractiveMsg).
		// However, load the schema in preview mode so it's visible in the read-only detail view.
		if msg.Err == nil && msg.ServiceName == "D1" && msg.Detail != nil {
			m.detail.PreviewD1Schema(msg.ResourceID)
			schemaCmd := m.loadD1Schema(msg.ResourceID)
			return *m, tea.Batch(schemaCmd, m.detail.SpinnerInit()), true
		}
		// Workers: trigger version history fetch if needed
		if scriptName := m.detail.NeedsVersionHistory(); scriptName != "" {
			m.detail.StartVersionHistoryLoad(scriptName)
			return *m, tea.Batch(m.fetchVersionHistory(scriptName), m.detail.SpinnerInit()), true
		}
		return *m, nil, true

	case detail.BackgroundRefreshMsg:
		if m.isStaleAccount(msg.AccountID) {
			return *m, nil, true
		}
		// Cache the refreshed result
		if msg.Err == nil && msg.Resources != nil {
			m.registry.SetCache(msg.ServiceName, msg.Resources)
		}
		// Update the detail panel if it's showing this service (in list mode)
		if msg.Err == nil && msg.ServiceName == m.detail.Service() {
			m.detail.RefreshResources(msg.Resources)
			m.updateManagedResources()
		}
		// Always update search items and decrement fetching counter
		m.search.SetItems(m.registry.AllSearchItems())
		if m.showSearch {
			m.search.DecrementFetching()
		}
		// Rebuild binding and access indexes when Workers list is refreshed
		if msg.ServiceName == "Workers" && msg.Err == nil && msg.Resources != nil {
			return *m, tea.Batch(m.buildBindingIndexCmd(), m.buildAccessIndexCmd()), true
		}
		return *m, nil, true

	// --- Version history messages ---

	case detail.VersionHistoryLoadedMsg:
		m.detail.SetVersionHistory(msg.ScriptName, msg.Entries, msg.Err)
		// Always try to enrich with builds API data — Workers Builds uses
		// `wrangler deploy` internally so the version source may say "wrangler"
		// even for CI-deployed versions.
		if msg.Err == nil && len(msg.Entries) > 0 {
			return *m, m.fetchBuildsForVersionHistory(msg.ScriptName), true
		}
		return *m, nil, true

	case detail.BuildsEnrichedMsg:
		m.detail.SetBuildsEnriched(msg.ScriptName, msg.Entries)
		return *m, nil, true

	case detail.BuildsAuthFailedMsg:
		// Builds API returned 401/403 — silently ignore.
		// The user sees a "(restricted)" badge in the header if fallback auth is not configured.
		return *m, nil, true

	case detail.FetchBuildLogMsg:
		return *m, tea.Batch(m.fetchBuildLog(msg.BuildUUID, msg.Entry), m.detail.SpinnerInit()), true

	case detail.BuildLogLoadedMsg:
		m.detail.SetBuildLog(msg.BuildUUID, msg.Lines, msg.Err, msg.Entry)
		return *m, nil, true

	// --- Interactive mode messages ---

	case detail.EnterInteractiveMsg:
		// User entered interactive mode on a ReadWrite service — initialize interactive features.
		if msg.Mode == detail.ReadWrite && msg.ServiceName == "D1" && msg.ResourceID != "" {
			if msg.IsLocal && msg.LocalResource != nil {
				// Local D1: init console and load schema from local emulator
				if !m.detail.D1Active() || m.detail.D1DatabaseID() != msg.ResourceID {
					inputCmd := m.detail.InitD1Console(msg.ResourceID)
					lr := *msg.LocalResource
					schemaCmd := m.loadLocalD1Schema(lr, msg.ResourceID)
					return *m, tea.Batch(inputCmd, schemaCmd, m.detail.SpinnerInit()), true
				}
			} else {
				// Remote D1: init console with schema fetch
				if !m.detail.D1Active() || m.detail.D1DatabaseID() != msg.ResourceID {
					inputCmd := m.detail.InitD1Console(msg.ResourceID)
					cmds := []tea.Cmd{inputCmd}
					if m.detail.IsLoading() {
						cmds = append(cmds, m.loadD1Schema(msg.ResourceID), m.detail.SpinnerInit())
					}
					return *m, tea.Batch(cmds...), true
				}
			}
		}
		if msg.Mode == detail.ReadWrite && msg.ServiceName == "KV" && msg.ResourceID != "" {
			if msg.IsLocal && msg.LocalResource != nil {
				// Local KV: init explorer and auto-load keys via CLI
				if !m.detail.KVActive() || m.detail.KVNamespaceID() != msg.ResourceID {
					inputCmd := m.detail.InitKVExplorer(msg.ResourceID)
					lr := *msg.LocalResource
					loadCmd := m.loadLocalKVKeys(lr, "")
					return *m, tea.Batch(inputCmd, loadCmd, m.detail.SpinnerInit()), true
				}
			} else {
				// Remote KV: init explorer and auto-load keys via API
				if !m.detail.KVActive() || m.detail.KVNamespaceID() != msg.ResourceID {
					inputCmd := m.detail.InitKVExplorer(msg.ResourceID)
					loadCmd := m.loadKVKeys(msg.ResourceID, "")
					return *m, tea.Batch(inputCmd, loadCmd, m.detail.SpinnerInit()), true
				}
			}
		}
		return *m, nil, true

	// --- D1 SQL console messages ---

	case detail.D1QueryMsg:
		if m.client == nil {
			return *m, nil, true
		}
		return *m, m.executeD1Query(msg.DatabaseID, msg.SQL), true

	case detail.D1QueryResultMsg:
		m.detail.SetD1QueryResult(msg.Result, msg.Err)
		// If the query changed the DB, auto-refresh the schema
		if msg.Result != nil && msg.Result.ChangedDB {
			m.detail.SetD1SchemaLoading()
			dbID := m.detail.D1DatabaseID()
			return *m, tea.Batch(m.loadD1Schema(dbID), m.detail.SpinnerInit()), true
		}
		return *m, nil, true

	case detail.D1SchemaLoadMsg:
		return *m, m.loadD1Schema(msg.DatabaseID), true

	case detail.D1SchemaLoadedMsg:
		// Staleness check: only apply if we're still on this database
		if msg.DatabaseID != m.detail.D1DatabaseID() {
			return *m, nil, true
		}
		m.detail.SetD1Schema(msg.Tables, msg.Err)
		return *m, nil, true

	// --- KV Data Explorer messages ---

	case detail.KVKeysLoadMsg:
		if m.client == nil {
			return *m, nil, true
		}
		return *m, tea.Batch(m.loadKVKeys(msg.NamespaceID, msg.Prefix), m.detail.SpinnerInit()), true

	case detail.KVKeysLoadedMsg:
		// Staleness check: only apply if we're still on this namespace
		if msg.NamespaceID != m.detail.KVNamespaceID() {
			return *m, nil, true
		}
		m.detail.SetKVKeys(msg.Keys, msg.Err)
		return *m, nil, true

	// --- Local emulator messages ---

	case detail.LocalResourcesUpdatedMsg:
		m.detail.SetLocalResources(msg.Resources)
		return *m, nil, true

	case detail.LocalD1QueryMsg:
		return *m, m.executeLocalD1Query(msg.LocalResource, msg.SQL), true

	case detail.LocalD1QueryResultMsg:
		m.detail.SetD1QueryResult(msg.Result, msg.Err)
		// If the query changed the DB, auto-refresh the local schema
		if msg.Result != nil && msg.Result.ChangedDB {
			if lr := m.detail.ActiveLocalResource(); lr != nil {
				m.detail.SetD1SchemaLoading()
				dbID := m.detail.D1DatabaseID()
				return *m, tea.Batch(m.loadLocalD1Schema(*lr, dbID), m.detail.SpinnerInit()), true
			}
		}
		return *m, nil, true

	case detail.LocalKVKeysLoadMsg:
		return *m, tea.Batch(m.loadLocalKVKeys(msg.LocalResource, msg.Prefix), m.detail.SpinnerInit()), true

	case detail.LocalKVKeysLoadedMsg:
		// Staleness check: only apply if we're still on the KV explorer
		if !m.detail.KVActive() {
			return *m, nil, true
		}
		m.detail.SetKVKeys(msg.Keys, msg.Err)
		return *m, nil, true

	// --- Tail lifecycle messages (single-tail from detail/Resources tab) ---

	case detail.TailStartMsg:
		if m.client == nil {
			return *m, nil, true
		}
		m.tailSource = "monitoring"
		m.monitoring.StartSingleTail(msg.ScriptName)
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		return *m, m.startTailCmd(accountID, msg.ScriptName), true

	case detail.TailStartedMsg:
		m.tailSession = msg.Session
		m.monitoring.SetTailConnected()
		return *m, m.waitForTailLines(), true

	case detail.TailLogMsg:
		if m.tailSession == nil {
			return *m, nil, true
		}
		m.monitoring.AppendTailLines(msg.Lines)
		// Continue polling for more lines
		return *m, m.waitForTailLines(), true

	case detail.TailErrorMsg:
		m.monitoring.SetTailError(msg.Err)
		m.tailSession = nil
		return *m, nil, true

	case detail.TailStoppedMsg:
		m.stopTail()
		return *m, nil, true

	// --- Clipboard ---

	case detail.CopyToClipboardMsg:
		_ = clipboard.WriteAll(msg.Text)
		m.toastMsg = "Copied to clipboard"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return *m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
			return toastExpireMsg{}
		}), true

	// --- Delete resource request ---

	case detail.DeleteResourceRequestMsg:
		if idx := m.registry.GetBindingIndex(); idx != nil {
			// Index available — show popup immediately with binding warnings
			boundWorkers := idx.Lookup(msg.ServiceName, msg.ResourceID)
			m.showDeletePopup = true
			m.deletePopup = deletepopup.New(msg.ServiceName, msg.ResourceID, msg.ResourceName, boundWorkers)
			return *m, nil, true
		}
		// Index not yet built — show popup immediately in loading state (spinner)
		// while we fetch Workers and build the index in the background.
		req := msg // copy for stashing
		m.pendingDeleteReq = &req
		m.showDeletePopup = true
		m.deletePopup = deletepopup.NewLoading(msg.ServiceName, msg.ResourceID, msg.ResourceName)
		// Kick off Workers fetch (if needed) + index build
		fetchCmds := []tea.Cmd{m.deletePopup.SpinnerTick()}
		if m.registry.GetCache("Workers") == nil {
			fetchCmds = append(fetchCmds, m.loadServiceResources("Workers"))
		} else {
			// Workers cached but index not built yet — just build the index
			if cmd := m.buildBindingIndexCmd(); cmd != nil {
				fetchCmds = append(fetchCmds, cmd)
			}
		}
		return *m, tea.Batch(fetchCmds...), true

	// --- Binding index built ---

	case bindingIndexBuiltMsg:
		if m.isStaleAccount(msg.accountID) {
			return *m, nil, true
		}
		m.registry.SetBindingIndex(msg.index)
		// Update managed resource highlighting now that the index is available
		m.updateManagedResources()
		// If there's a pending delete request, transition the popup from loading → confirm.
		if m.pendingDeleteReq != nil {
			req := m.pendingDeleteReq
			m.pendingDeleteReq = nil
			if m.showDeletePopup && m.deletePopup.IsLoading() {
				boundWorkers := msg.index.Lookup(req.ServiceName, req.ResourceID)
				m.deletePopup.SetBindingWarnings(boundWorkers)
			}
		}
		return *m, nil, true

	// --- Access index built ---

	case accessIndexBuiltMsg:
		if m.isStaleAccount(msg.accountID) {
			return *m, nil, true
		}
		m.registry.SetAccessIndex(msg.index)
		// Update the detail view's access-protected set for list view badges
		m.detail.SetAccessProtected(msg.index.ProtectedWorkerIDs())
		// Sync Operations tab badges
		m.syncAccessBadges()
		return *m, nil, true
	}

	return *m, nil, false
}

// enrichDetailWithAccessInfo appends Access protection detail fields to a Worker's
// resource detail if any Access Applications protect it.
func (m Model) enrichDetailWithAccessInfo(detail *svc.ResourceDetail, scriptName string) {
	idx := m.registry.GetAccessIndex()
	if idx == nil {
		return
	}
	infos := idx.Lookup(scriptName)
	if len(infos) == 0 {
		return
	}

	for _, info := range infos {
		var parts []string

		// App name
		parts = append(parts, fmt.Sprintf("\xf0\x9f\x94\x92 %s", info.AppName)) // lock emoji

		// Policy summary
		if len(info.Policies) > 0 {
			var policyParts []string
			for _, p := range info.Policies {
				policyParts = append(policyParts, fmt.Sprintf("%s: %s", p.Decision, p.Name))
			}
			parts = append(parts, fmt.Sprintf("Policies: %s", strings.Join(policyParts, ", ")))
		}

		// IdPs
		if len(info.AllowedIdPs) > 0 {
			parts = append(parts, fmt.Sprintf("IdPs: %s", strings.Join(info.AllowedIdPs, ", ")))
		}

		// Session duration
		if info.SessionDuration != "" {
			parts = append(parts, fmt.Sprintf("Session: %s", info.SessionDuration))
		}

		// Protected domain
		if info.Domain != "" {
			parts = append(parts, fmt.Sprintf("Domain: %s", info.Domain))
		}

		detail.Fields = append(detail.Fields, svc.DetailField{
			Label: "Access",
			Value: strings.Join(parts, "\n                   "),
		})
	}
}

// syncAccessBadges updates the access badge data on all env boxes and project boxes
// using the current Access index. Called after the access index is built.
func (m *Model) syncAccessBadges() {
	idx := m.registry.GetAccessIndex()
	if idx == nil {
		return
	}

	// Update env boxes (for drilled-in project view)
	for i := range m.wrangler.EnvBoxCount() {
		eb := m.wrangler.EnvBoxAt(i)
		if eb == nil {
			continue
		}
		eb.AccessProtected = idx.IsProtected(eb.WorkerName)
	}

	// Update project boxes (for monorepo project list)
	m.wrangler.UpdateAccessBadges(func(workerName string) bool {
		return idx.IsProtected(workerName)
	})
}
