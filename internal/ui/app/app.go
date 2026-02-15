package app

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/actions"
	"github.com/oarafat/orangeshell/internal/ui/bindings"
	uiconfig "github.com/oarafat/orangeshell/internal/ui/config"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/deployallpopup"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/envvars"
	"github.com/oarafat/orangeshell/internal/ui/header"
	"github.com/oarafat/orangeshell/internal/ui/launcher"
	"github.com/oarafat/orangeshell/internal/ui/monitoring"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/removeprojectpopup"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/setup"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	uitriggers "github.com/oarafat/orangeshell/internal/ui/triggers"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// Periodic polling removed — data is now refreshed on-demand:
// (a) when navigating to a view and cache is stale (>CacheTTL), or
// (b) immediately after a mutating action (deploy, resource creation, etc.).

// ViewState tracks the current content view.
type ViewState int

const (
	ViewWrangler      ViewState = iota // Wrangler home screen (default)
	ViewServiceList                    // Service resource list (Workers, KV, etc.)
	ViewServiceDetail                  // Resource detail (Worker detail, D1 console)
	ViewEnvVars                        // Environment variables view
	ViewTriggers                       // Cron triggers view
)

// Phase tracks the top-level application state.
type Phase int

const (
	PhaseSetup Phase = iota
	PhaseDashboard
)

type initDashboardMsg struct {
	client   *api.Client
	accounts []api.Account
}

type errMsg struct {
	err error
}

// toastExpireMsg fires after the toast display duration to clear the toast.
type toastExpireMsg struct{}

// toastTick returns a command that fires toastExpireMsg after 3 seconds.
func toastTick() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return toastExpireMsg{} })
}

// setToast sets the toast message and expiry. Call toastTick() to get the auto-clear command.
func (m *Model) setToast(msg string) {
	m.toastMsg = msg
	m.toastExpiry = time.Now().Add(3 * time.Second)
}

// isStaleAccount returns true if the given accountID is non-empty and doesn't
// match the currently active account, indicating the response is stale.
func (m Model) isStaleAccount(accountID string) bool {
	return accountID != "" && accountID != m.registry.ActiveAccountID()
}

// reloadWranglerConfig re-parses the wrangler config at configPath and updates the
// wrangler model. On success it shows successToast. On error it shows the parse error.
// Returns the reloaded config (nil on error).
func (m *Model) reloadWranglerConfig(configPath, successToast string) *wcfg.WranglerConfig {
	cfg, err := wcfg.Parse(configPath)
	if err != nil {
		m.toastMsg = fmt.Sprintf("Config reload error: %v", err)
		m.toastExpiry = time.Now().Add(3 * time.Second)
		return nil
	}
	m.wrangler.ReloadConfig(configPath, cfg)
	m.toastMsg = successToast
	m.toastExpiry = time.Now().Add(3 * time.Second)
	return cfg
}

// resolveActiveProjectConfig returns the config path and project name for the
// currently active project — either the selected project on the monorepo list,
// or the drilled-into / single-project config.
func (m Model) resolveActiveProjectConfig() (configPath, projectName string) {
	if m.wrangler.IsOnProjectList() {
		configPath = m.wrangler.SelectedProjectConfigPath()
		if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
			projectName = cfg.Name
		}
	} else if m.wrangler.HasConfig() {
		configPath = m.wrangler.ConfigPath()
		if cfg := m.wrangler.Config(); cfg != nil {
			projectName = cfg.Name
		}
	}
	return
}

// bindingIndexBuiltMsg carries a newly built binding index from the background.
type bindingIndexBuiltMsg struct {
	index     *svc.BindingIndex
	accountID string
}

// parallelTailStartedMsg signals that a single parallel tail session has connected.
type parallelTailStartedMsg struct {
	ScriptName string
	Session    *svc.TailSession
}

// parallelTailLogMsg carries log lines from a single parallel tail session.
type parallelTailLogMsg struct {
	ScriptName string
	Lines      []svc.TailLine
}

// parallelTailErrorMsg signals that a parallel tail session encountered an error.
type parallelTailErrorMsg struct {
	ScriptName string
	Err        error
}

// parallelTailSessionDoneMsg signals that a parallel tail session's channel closed.
type parallelTailSessionDoneMsg struct {
	ScriptName string
}

// Model is the root Bubble Tea model that composes all UI components.
type Model struct {
	// Submodels
	setup  setup.Model
	header header.Model
	detail detail.Model
	search search.Model

	// Launcher overlay (replaces sidebar)
	showLauncher bool
	launcher     launcher.Model

	// State
	phase        Phase
	viewState    ViewState
	activeTab    tabbar.TabID
	hoverTab     tabbar.TabID // -1 means no hover
	showSearch   bool
	showActions  bool
	actionsPopup actions.Model
	cfg          *config.Config
	client       *api.Client
	registry     *svc.Registry

	// Dimensions
	width  int
	height int

	// Error display
	err error

	// Wrangler project view
	wrangler              uiwrangler.Model
	wranglerRunner        *wcfg.Runner
	wranglerRunnerAction  string       // action string of the running wrangler command (e.g. "deploy", "versions deploy")
	wranglerVersionRunner *wcfg.Runner // separate runner for background version fetches

	// Monitoring tab model (live tail sessions)
	monitoring  monitoring.Model
	devSessions []devSession // active wrangler dev sessions (for monitoring tab dev tailing)

	// Active tail session (nil when no tail is running)
	tailSession *svc.TailSession
	tailSource  string // "wrangler", "detail", or "monitoring" — which view owns the current tail

	// Parallel tail sessions (monorepo multi-worker tailing)
	parallelTailSessions []*svc.TailSession
	parallelTailActive   bool

	// Binding popup overlay
	showBindings  bool
	bindingsPopup bindings.Model

	// Add environment popup overlay
	showEnvPopup bool
	envPopup     envpopup.Model

	// Delete resource popup overlay
	showDeletePopup  bool
	deletePopup      deletepopup.Model
	pendingDeleteReq *detail.DeleteResourceRequestMsg // stashed while binding index is being built

	// Create project popup overlay
	showProjectPopup bool
	projectPopup     projectpopup.Model

	// Remove project popup overlay
	showRemoveProjectPopup bool
	removeProjectPopup     removeprojectpopup.Model

	// Configuration tab (unified model)
	configView uiconfig.Model

	// Legacy: Environment variables view (kept for Operations tab cross-nav)
	envvarsView             envvars.Model
	envVarsFromResourceList bool // true when env vars view was opened from the Resources launcher

	// Legacy: Cron triggers view (kept for Operations tab cross-nav)
	triggersView             uitriggers.Model
	triggersFromResourceList bool // true when triggers view was opened from the Resources launcher

	// Deploy all popup overlay
	showDeployAllPopup bool
	deployAllPopup     deployallpopup.Model
	deployAllRunners   []*wcfg.Runner // one per project, kept for cancellation

	// Toast notification
	toastMsg    string
	toastExpiry time.Time

	// scanDir is the directory to scan for wrangler projects (from CLI arg).
	// Empty means no auto-scan — show the empty-state menu immediately.
	scanDir string
}

// NewModel creates the root model. If config is already set up, skips to dashboard.
// scanDir is an optional directory path to scan for wrangler projects; if empty,
// no auto-scan is performed and the empty-state menu is shown immediately.
func NewModel(cfg *config.Config, scanDir string) Model {
	phase := PhaseSetup
	if cfg.IsConfigured() {
		phase = PhaseDashboard
	}

	m := Model{
		setup:      setup.New(cfg),
		header:     header.New(cfg.AuthMethod),
		detail:     detail.New(),
		search:     search.New(),
		wrangler:   uiwrangler.New(),
		monitoring: monitoring.New(),
		configView: uiconfig.New(),
		phase:      phase,
		viewState:  ViewWrangler, // wrangler is the home screen
		activeTab:  tabbar.TabOperations,
		hoverTab:   -1, // no tab hovered
		cfg:        cfg,
		registry:   svc.NewRegistry(),
		scanDir:    scanDir,
	}

	return m
}

// Init returns the initial command.
func (m Model) Init() tea.Cmd {
	if m.phase == PhaseDashboard {
		cmds := []tea.Cmd{m.initDashboardCmd(), m.wrangler.SpinnerInit()}

		if m.scanDir != "" {
			// A directory was provided on the CLI — scan it for wrangler projects.
			cmds = append(cmds, m.discoverProjectsFromDir(m.scanDir))
		} else {
			// No directory provided — skip discovery, show empty-state menu immediately.
			cmds = append(cmds, func() tea.Msg {
				return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
			})
		}

		return tea.Batch(cmds...)
	}
	return nil
}

// initDashboardCmd authenticates and fetches account info.
func (m Model) initDashboardCmd() tea.Cmd {
	return func() tea.Msg {
		authenticator, err := auth.New(m.cfg)
		if err != nil {
			return errMsg{err}
		}

		ctx := context.Background()

		// Validate credentials first — this refreshes expired OAuth tokens.
		// Must happen before creating the SDK client so it gets the fresh token.
		if err := authenticator.Validate(ctx); err != nil {
			return errMsg{err}
		}

		client, err := api.NewClient(authenticator, m.cfg)
		if err != nil {
			return errMsg{err}
		}

		accounts, err := client.ListAccounts(ctx)
		if err != nil {
			return errMsg{err}
		}

		return initDashboardMsg{
			client:   client,
			accounts: accounts,
		}
	}
}

// Update handles all messages for the application.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Delegate to domain-specific message handlers (Tier 3).
	// Each returns (Model, Cmd, handled). If handled, we're done.
	handlers := []func(*Model, tea.Msg) (Model, tea.Cmd, bool){
		(*Model).handleDeletePopupMsg,
		(*Model).handleBindingsMsg,
		(*Model).handleEnvPopupMsg,
		(*Model).handleProjectPopupMsg,
		(*Model).handleRemoveProjectMsg,
		(*Model).handleEnvVarsMsg,
		(*Model).handleTriggersMsg,
		(*Model).handleConfigViewMsg,
		(*Model).handleDeployAllMsg,
	}
	for _, h := range handlers {
		if result, cmd, handled := h(&m, msg); handled {
			return result, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.search.SetSize(msg.Width, msg.Height)
		if m.phase == PhaseSetup {
			m.setup.SetSize(msg.Width, msg.Height)
		}
		return m, nil

	case initDashboardMsg:
		m.client = msg.client
		m.phase = PhaseDashboard
		m.layout()

		// Build header account tabs from the full accounts list
		headerAccounts := make([]header.Account, len(msg.accounts))
		for i, acc := range msg.accounts {
			headerAccounts[i] = header.Account{ID: acc.ID, Name: acc.Name}
		}
		m.header.SetAccounts(headerAccounts, m.cfg.AccountID)

		// Register services for the active account
		m.registerServices(m.cfg.AccountID)

		// Start on wrangler home — no service to load initially
		m.viewState = ViewWrangler

		var cmds []tea.Cmd

		// If wrangler config was already discovered (it runs in parallel with auth),
		// trigger deployment fetching now that the client is available.
		if m.wrangler.IsMonorepo() {
			if cmd := m.fetchAllProjectDeployments(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else if cfg := m.wrangler.Config(); cfg != nil {
			if cmd := m.fetchSingleProjectDeployments(cfg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		return m, tea.Batch(cmds...)

	case errMsg:
		m.err = msg.err
		if m.phase == PhaseDashboard && m.client == nil {
			m.phase = PhaseSetup
			m.setup = setup.New(m.cfg)
			m.setup.SetSize(m.width, m.height)
		}
		return m, nil

	// Service data messages
	case detail.ResourcesLoadedMsg:
		if m.isStaleAccount(msg.AccountID) {
			return m, nil
		}
		// Cache the result regardless of which service is displayed
		if msg.Err == nil && msg.Resources != nil {
			m.registry.SetCache(msg.ServiceName, msg.Resources)
		}
		// When Workers list loads, build the binding index in the background.
		// When a non-Workers service loads and no binding index exists yet,
		// trigger a Workers fetch + index build so managed detection works.
		var indexCmd tea.Cmd
		if msg.ServiceName == "Workers" && msg.Err == nil && msg.Resources != nil {
			indexCmd = m.buildBindingIndexCmd()
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
			if indexCmd != nil {
				return m, indexCmd
			}
			return m, nil
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
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case detail.SelectServiceMsg:
		// User selected a service from the dropdown — navigate to it
		cmd := m.navigateToService(msg.ServiceName)
		return m, cmd

	case detail.LoadResourcesMsg:
		// Don't attempt to load if auth hasn't completed yet — services aren't
		// registered. The initDashboardMsg handler will trigger the load once
		// the client is ready.
		if m.client == nil {
			return m, nil
		}
		return m, m.loadServiceResources(msg.ServiceName)

	case detail.LoadDetailMsg:
		if msg.ServiceName == "Env Variables" {
			// ResourceID is the config path; look up project name
			configPath := msg.ResourceID
			projectName := ""
			for _, p := range m.wrangler.ProjectConfigs() {
				if p.ConfigPath == configPath {
					if p.Config != nil {
						projectName = p.Config.Name
					}
					break
				}
			}
			// Reset detail model back to list mode so it's not stuck
			// in "Loading details..." when we return via esc.
			m.detail.BackToList()
			m.envVarsFromResourceList = true
			return m, m.openEnvVarsView(configPath, "", projectName)
		}
		if msg.ServiceName == "Triggers" {
			configPath := msg.ResourceID
			projectName := ""
			for _, p := range m.wrangler.ProjectConfigs() {
				if p.ConfigPath == configPath {
					if p.Config != nil {
						projectName = p.Config.Name
					}
					break
				}
			}
			m.detail.BackToList()
			m.triggersFromResourceList = true
			return m, m.openTriggersView(configPath, projectName)
		}
		m.viewState = ViewServiceDetail
		return m, tea.Batch(m.loadResourceDetail(msg.ServiceName, msg.ResourceID), m.detail.SpinnerInit())

	case detail.DetailLoadedMsg:
		// Staleness check: ignore if the user has switched services or resources
		if msg.ServiceName != m.detail.Service() {
			return m, nil
		}
		// Enrich non-Workers detail with bound worker references
		if msg.Err == nil && msg.Detail != nil && msg.ServiceName != "Workers" {
			m.enrichDetailWithBoundWorkers(msg.Detail, msg.ServiceName, msg.ResourceID)
		}
		m.detail.SetDetail(msg.Detail, msg.Err)
		// D1 console initialization is deferred to interactive mode (EnterInteractiveMsg).
		// However, load the schema in preview mode so it's visible in the read-only detail view.
		if msg.Err == nil && msg.ServiceName == "D1" && msg.Detail != nil {
			m.detail.PreviewD1Schema(msg.ResourceID)
			schemaCmd := m.loadD1Schema(msg.ResourceID)
			return m, tea.Batch(schemaCmd, m.detail.SpinnerInit())
		}
		return m, nil

	case detail.EnterInteractiveMsg:
		// User entered interactive mode on a ReadWrite service — initialize interactive features.
		if msg.Mode == detail.ReadWrite && msg.ServiceName == "D1" && msg.ResourceID != "" {
			// Only init D1 console if not already active for this database
			if !m.detail.D1Active() || m.detail.D1DatabaseID() != msg.ResourceID {
				inputCmd := m.detail.InitD1Console(msg.ResourceID)
				// InitD1Console preserves schema if already loaded for this DB;
				// only re-fetch if schema is not yet available.
				cmds := []tea.Cmd{inputCmd}
				if m.detail.IsLoading() {
					cmds = append(cmds, m.loadD1Schema(msg.ResourceID), m.detail.SpinnerInit())
				}
				return m, tea.Batch(cmds...)
			}
		}
		return m, nil

	case detail.BackgroundRefreshMsg:
		if m.isStaleAccount(msg.AccountID) {
			return m, nil
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
		// Rebuild binding index when Workers list is refreshed
		if msg.ServiceName == "Workers" && msg.Err == nil && msg.Resources != nil {
			return m, m.buildBindingIndexCmd()
		}
		return m, nil

	// Tail lifecycle messages — all routed to the monitoring model
	case detail.TailStartMsg:
		if m.client == nil {
			return m, nil
		}
		m.tailSource = "monitoring"
		m.monitoring.StartSingleTail(msg.ScriptName)
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		return m, m.startTailCmd(accountID, msg.ScriptName)

	case detail.TailStartedMsg:
		m.tailSession = msg.Session
		m.monitoring.SetTailConnected()
		return m, m.waitForTailLines()

	case detail.TailLogMsg:
		if m.tailSession == nil {
			return m, nil
		}
		m.monitoring.AppendTailLines(msg.Lines)
		// Continue polling for more lines
		return m, m.waitForTailLines()

	case detail.TailErrorMsg:
		m.monitoring.SetTailError(msg.Err)
		m.tailSession = nil
		return m, nil

	case detail.TailStoppedMsg:
		m.stopTail()
		return m, nil

	// D1 SQL console messages
	case detail.D1QueryMsg:
		if m.client == nil {
			return m, nil
		}
		return m, m.executeD1Query(msg.DatabaseID, msg.SQL)

	case detail.D1QueryResultMsg:
		m.detail.SetD1QueryResult(msg.Result, msg.Err)
		// If the query changed the DB, auto-refresh the schema
		if msg.Result != nil && msg.Result.ChangedDB {
			m.detail.SetD1SchemaLoading()
			dbID := m.detail.D1DatabaseID()
			return m, tea.Batch(m.loadD1Schema(dbID), m.detail.SpinnerInit())
		}
		return m, nil

	case detail.D1SchemaLoadMsg:
		return m, m.loadD1Schema(msg.DatabaseID)

	case detail.D1SchemaLoadedMsg:
		// Staleness check: only apply if we're still on this database
		if msg.DatabaseID != m.detail.D1DatabaseID() {
			return m, nil
		}
		m.detail.SetD1Schema(msg.Tables, msg.Err)
		return m, nil

	case detail.CopyToClipboardMsg:
		_ = clipboard.WriteAll(msg.Text)
		m.toastMsg = "Copied to clipboard"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
			return toastExpireMsg{}
		})

	case toastExpireMsg:
		if time.Now().After(m.toastExpiry) {
			m.toastMsg = ""
		}
		return m, nil

	// Delete resource popup messages (from detail list view)
	case detail.DeleteResourceRequestMsg:
		if idx := m.registry.GetBindingIndex(); idx != nil {
			// Index available — show popup immediately with binding warnings
			boundWorkers := idx.Lookup(msg.ServiceName, msg.ResourceID)
			m.showDeletePopup = true
			m.deletePopup = deletepopup.New(msg.ServiceName, msg.ResourceID, msg.ResourceName, boundWorkers)
			return m, nil
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
		return m, tea.Batch(fetchCmds...)

	case bindingIndexBuiltMsg:
		if m.isStaleAccount(msg.accountID) {
			return m, nil
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
		return m, nil

	// Wrangler messages
	case uiwrangler.ConfigLoadedMsg:
		m.wrangler.SetConfig(msg.Config, msg.Path, msg.Err)
		// Refresh the monitoring worker tree in case we're on the Monitoring tab
		m.refreshMonitoringWorkerTree()
		// Trigger deployment fetching for single-project environments
		if msg.Err == nil && msg.Config != nil {
			return m, m.fetchSingleProjectDeployments(msg.Config)
		}
		return m, nil

	case uiwrangler.EmptyMenuSelectMsg:
		switch msg.Action {
		case "create_project":
			m.wrangler.ActivateDirBrowser(uiwrangler.DirBrowserModeCreate)
		case "open_project":
			m.wrangler.ActivateDirBrowser(uiwrangler.DirBrowserModeOpen)
		}
		return m, nil

	case uiwrangler.EnvDeploymentLoadedMsg:
		if m.isStaleAccount(msg.AccountID) {
			return m, nil
		}
		// Always update (even on error) so DeploymentFetched gets set and
		// the UI can show "Currently not deployed" instead of nothing.
		m.wrangler.SetEnvDeployment(msg.EnvName, msg.Deployment, msg.Subdomain)
		// Cache the deployment data in the registry for instant restore on account switch-back.
		// Cache both successful responses and errors (worker not found = "not deployed").
		if msg.ScriptName != "" {
			m.registry.SetDeploymentCache(msg.ScriptName, displayToDeploymentInfo(msg.Deployment), msg.Subdomain)
		}
		return m, nil

	case uiwrangler.ProjectsDiscoveredMsg:
		m.wrangler.SetProjects(msg.Projects, msg.RootName, msg.RootDir)
		// Refresh the monitoring worker tree
		m.refreshMonitoringWorkerTree()
		// Trigger deployment fetching for all projects
		return m, m.fetchAllProjectDeployments()

	case uiwrangler.ProjectDeploymentLoadedMsg:
		if m.isStaleAccount(msg.AccountID) {
			return m, nil
		}
		// Always update so DeploymentFetched gets set
		m.wrangler.SetProjectDeployment(msg.ProjectIndex, msg.EnvName, msg.Deployment, msg.Subdomain)
		// Cache the deployment data in the registry.
		// Cache both successful responses and errors (worker not found = "not deployed").
		if msg.ScriptName != "" {
			m.registry.SetDeploymentCache(msg.ScriptName, displayToDeploymentInfo(msg.Deployment), msg.Subdomain)
		}
		return m, nil

	case uiwrangler.TailStartMsg:
		// "t" key pressed in wrangler view — start tailing on Monitoring tab
		if m.client == nil {
			return m, nil
		}
		// Stop any existing tail
		m.stopTail()
		// Start tail via monitoring model and switch to Monitoring tab
		m.tailSource = "monitoring"
		m.monitoring.StartSingleTail(msg.ScriptName)
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		return m, m.startTailCmd(accountID, msg.ScriptName)

	case uiwrangler.TailStoppedMsg:
		// "t" key pressed while tail is active — stop it
		m.stopTail()
		return m, nil

	// Parallel tail lifecycle messages — routed to monitoring model
	case uiwrangler.ParallelTailStartMsg:
		if m.client == nil {
			return m, nil
		}
		// Stop any existing single tail and parallel tails
		m.stopTail()
		m.stopAllParallelTails()
		// Convert wrangler targets to monitoring targets
		monTargets := make([]monitoring.ParallelTailTarget, len(msg.Scripts))
		for i, t := range msg.Scripts {
			monTargets[i] = monitoring.ParallelTailTarget{ScriptName: t.ScriptName, URL: t.URL}
		}
		// Start parallel tailing on Monitoring tab
		m.monitoring.StartParallelTail(msg.EnvName, monTargets)
		m.parallelTailActive = true
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		var cmds []tea.Cmd
		for _, target := range msg.Scripts {
			cmds = append(cmds, m.startParallelTailSessionCmd(accountID, target.ScriptName))
		}
		return m, tea.Batch(cmds...)

	case uiwrangler.ParallelTailExitMsg:
		m.stopAllParallelTails()
		return m, nil

	case parallelTailStartedMsg:
		if !m.parallelTailActive {
			// Stale — parallel tail was stopped while session was connecting
			if msg.Session != nil {
				session := msg.Session
				client := m.client
				go func() {
					ctx := context.Background()
					svc.StopTail(ctx, client.CF, session)
				}()
			}
			return m, nil
		}
		m.parallelTailSessions = append(m.parallelTailSessions, msg.Session)
		m.monitoring.ParallelTailSetConnected(msg.ScriptName)
		m.monitoring.ParallelTailSetSessionID(msg.ScriptName, msg.Session.ID)
		return m, m.waitForParallelTailLines(msg.ScriptName, msg.Session)

	case parallelTailLogMsg:
		if !m.parallelTailActive {
			return m, nil
		}
		m.monitoring.ParallelTailAppendLines(msg.ScriptName, msg.Lines)
		// Find the session to continue polling
		for _, s := range m.parallelTailSessions {
			if s.ScriptName == msg.ScriptName {
				return m, m.waitForParallelTailLines(msg.ScriptName, s)
			}
		}
		return m, nil

	case parallelTailErrorMsg:
		if !m.parallelTailActive {
			return m, nil
		}
		m.monitoring.ParallelTailSetError(msg.ScriptName, msg.Err)
		return m, nil

	case parallelTailSessionDoneMsg:
		// Channel closed for one session — nothing to do, pane stays with last lines
		return m, nil

	// Monitoring model messages
	case monitoring.TailStopMsg:
		m.stopTail()
		return m, nil

	case monitoring.ParallelTailStopMsg:
		m.stopAllParallelTails()
		return m, nil

	// New monitoring dual-pane messages
	case monitoring.TailAddMsg:
		// User pressed 'a' on a worker in the tree — add to grid and start tail
		if m.isDevWorker(msg.ScriptName) {
			// Dev worker: output is already being piped. Just ensure it's in the grid.
			if ds := m.findDevSession(msg.ScriptName); ds != nil {
				m.monitoring.AddDevToGrid(ds.ScriptName, ds.DevKind)
			}
			return m, nil
		}
		if m.client == nil {
			return m, nil
		}
		m.monitoring.AddToGrid(msg.ScriptName, "")
		m.parallelTailActive = true
		accountID := m.registry.ActiveAccountID()
		return m, m.startGridTailCmd(accountID, msg.ScriptName)

	case monitoring.TailRemoveMsg:
		// User pressed 'd' on a worker in the tree — stop tail and remove from grid
		if m.isDevWorker(msg.ScriptName) {
			// Dev worker: remove from grid but don't stop the dev process.
			// Output continues flowing to CmdPane. User can re-add via 'a'.
			m.monitoring.RemoveFromGrid(msg.ScriptName)
			return m, nil
		}
		m.stopGridTail(msg.ScriptName)
		m.monitoring.RemoveFromGrid(msg.ScriptName)
		return m, nil

	case monitoring.TailToggleMsg:
		if m.isDevWorker(msg.ScriptName) {
			// Dev panes can't be toggled — they're always active while process runs
			return m, nil
		}
		if m.client == nil {
			return m, nil
		}
		if msg.Start {
			// Restart the tail for this pane
			m.parallelTailActive = true
			accountID := m.registry.ActiveAccountID()
			return m, m.startGridTailCmd(accountID, msg.ScriptName)
		}
		// Stop the tail for this pane (but keep in grid)
		m.stopGridTail(msg.ScriptName)
		m.monitoring.GridSetStopped(msg.ScriptName)
		return m, nil

	case monitoring.TailToggleAllMsg:
		if m.client == nil {
			return m, nil
		}
		if msg.Start {
			// Start tails for all stopped grid panes (skip dev panes — always active)
			m.parallelTailActive = true
			accountID := m.registry.ActiveAccountID()
			var cmds []tea.Cmd
			for _, script := range m.monitoring.AllGridPaneScripts() {
				if !m.hasGridTailSession(script) && !m.isDevWorker(script) {
					cmds = append(cmds, m.startGridTailCmd(accountID, script))
				}
			}
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}
		// Stop all grid tails (dev panes are unaffected — no API session to stop)
		m.stopAllGridTails()
		return m, nil

	case uiwrangler.LoadConfigPathMsg:
		if m.wrangler.DirBrowserActiveMode() == uiwrangler.DirBrowserModeCreate {
			// User chose a directory to create a new project in
			m.wrangler.CloseDirBrowser()
			m.showProjectPopup = true
			m.projectPopup = projectpopup.New(nil, msg.Path)
			return m, nil
		}
		// User entered a custom path — scan it for wrangler config
		m.wrangler.SetConfigLoading()
		return m, tea.Batch(m.discoverProjectsFromDir(msg.Path), m.wrangler.SpinnerInit())

	case uiwrangler.NavigateMsg:
		return m, m.navigateTo(msg.ServiceName, msg.ResourceID)

	case uiwrangler.ActionMsg:
		return m, m.startWranglerCmd(msg.Action, msg.EnvName)

	case uiwrangler.OpenURLMsg:
		return m, openURL(msg.URL)

	case uiwrangler.VersionsFetchedMsg:
		if msg.Err != nil {
			// Show error and close picker
			m.wrangler.CloseVersionPicker()
			m.err = fmt.Errorf("failed to fetch versions: %w", msg.Err)
			return m, nil
		}
		m.wrangler.SetVersions(msg.Versions)
		return m, m.wrangler.SpinnerInit()

	case uiwrangler.DeployVersionMsg:
		m.wrangler.CloseVersionPicker()
		return m, m.startWranglerCmdWithArgs("versions deploy", msg.EnvName, []string{
			fmt.Sprintf("%s@100", msg.VersionID),
			"-y",
		})

	case uiwrangler.GradualDeployMsg:
		m.wrangler.CloseVersionPicker()
		pctB := 100 - msg.PercentageA
		return m, m.startWranglerCmdWithArgs("versions deploy", msg.EnvName, []string{
			fmt.Sprintf("%s@%d", msg.VersionA, msg.PercentageA),
			fmt.Sprintf("%s@%d", msg.VersionB, pctB),
			"-y",
		})

	case uiwrangler.VersionPickerCloseMsg:
		m.wrangler.CloseVersionPicker()
		return m, nil

	case uiwrangler.DeleteBindingRequestMsg:
		m.showDeletePopup = true
		m.deletePopup = deletepopup.NewBindingDelete(msg.ConfigPath, msg.EnvName, msg.BindingName, msg.BindingType, msg.WorkerName)
		return m, nil

	case uiwrangler.ShowEnvVarsMsg:
		m.syncConfigProjects()
		m.configView.SelectProjectByPath(msg.ConfigPath)
		m.configView.SetCategory(uiconfig.CategoryEnvVars)
		m.activeTab = tabbar.TabConfiguration
		return m, nil

	case uiwrangler.ShowTriggersMsg:
		m.syncConfigProjects()
		m.configView.SelectProjectByPath(msg.ConfigPath)
		m.configView.SetCategory(uiconfig.CategoryTriggers)
		m.activeTab = tabbar.TabConfiguration
		return m, nil

	case uiwrangler.CmdOutputMsg:
		m.wrangler.AppendCmdOutput(msg.Line)

		// If this is a dev command, also pipe output to the monitoring grid
		if wcfg.IsDevAction(m.wranglerRunnerAction) && len(m.devSessions) > 0 {
			ds := &m.devSessions[0]
			tailLine := parseDevOutputLine(msg.Line)
			m.monitoring.GridAppendLines(ds.ScriptName, []svc.TailLine{tailLine})

			// Check for port announcement (e.g. "Ready on http://localhost:8787")
			if port := extractDevPort(msg.Line.Text); port != "" && ds.Port == "" {
				ds.Port = port
				m.refreshMonitoringWorkerTree()
			}
		}

		return m, waitForWranglerOutput(m.wranglerRunner)

	case uiwrangler.CmdDoneMsg:
		// Drain any remaining lines before finishing
		isDevCmd := wcfg.IsDevAction(m.wranglerRunnerAction)
		if m.wranglerRunner != nil {
			for line := range m.wranglerRunner.LinesCh() {
				m.wrangler.AppendCmdOutput(line)
				// Also pipe drained dev lines to monitoring grid
				if isDevCmd && len(m.devSessions) > 0 {
					tailLine := parseDevOutputLine(line)
					m.monitoring.GridAppendLines(m.devSessions[0].ScriptName, []svc.TailLine{tailLine})
				}
			}
		}
		m.wrangler.FinishCommand(msg.Result)
		action := m.wranglerRunnerAction
		m.wranglerRunner = nil
		m.wranglerRunnerAction = ""

		// If this was a dev action, clean up dev monitoring state
		if isDevCmd {
			m.cleanupDevSession()
		}

		// After mutating commands, immediately refresh stale data
		if isMutatingAction(action) && msg.Result.ExitCode == 0 {
			return m, m.refreshAfterMutation()
		}
		return m, nil

	// Search messages
	case search.NavigateMsg:
		m.showSearch = false
		// Navigate to the selected service and resource
		return m, m.navigateTo(msg.ServiceName, msg.ResourceID)

	case search.CloseMsg:
		m.showSearch = false
		return m, nil

	// Action popup messages
	case actions.SelectMsg:
		m.showActions = false
		return m, m.handleActionSelect(msg.Item)

	case actions.CloseMsg:
		m.showActions = false
		return m, nil

	// Launcher messages
	case launcher.LaunchServiceMsg:
		m.showLauncher = false
		if msg.ServiceName == "" {
			// Go home
			m.activeTab = tabbar.TabOperations
			m.viewState = ViewWrangler
			if cmd := m.refreshDeploymentsIfStale(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}
		if msg.ServiceName == "Env Variables" {
			m.syncConfigProjects()
			m.configView.SetCategory(uiconfig.CategoryEnvVars)
			m.activeTab = tabbar.TabConfiguration
			return m, nil
		}
		if msg.ServiceName == "Triggers" {
			m.syncConfigProjects()
			m.configView.SetCategory(uiconfig.CategoryTriggers)
			m.activeTab = tabbar.TabConfiguration
			return m, nil
		}
		return m, m.navigateToService(msg.ServiceName)

	case launcher.CloseMsg:
		m.showLauncher = false
		return m, nil

	case spinner.TickMsg:
		var cmds []tea.Cmd
		if m.detail.IsLoading() {
			cmds = append(cmds, m.detail.UpdateSpinner(msg))
		}
		if m.wrangler.IsLoading() {
			cmds = append(cmds, m.wrangler.UpdateSpinner(msg))
		}
		if m.showProjectPopup && m.projectPopup.IsCreating() {
			var cmd tea.Cmd
			m.projectPopup, cmd = m.projectPopup.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.showDeletePopup && m.deletePopup.NeedsSpinner() {
			var cmd tea.Cmd
			m.deletePopup, cmd = m.deletePopup.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.showRemoveProjectPopup && m.removeProjectPopup.NeedsSpinner() {
			var cmd tea.Cmd
			m.removeProjectPopup, cmd = m.removeProjectPopup.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.showDeployAllPopup && m.deployAllPopup.IsDeploying() {
			var cmd tea.Cmd
			m.deployAllPopup, cmd = m.deployAllPopup.Update(msg)
			cmds = append(cmds, cmd)
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil
	}

	switch m.phase {
	case PhaseSetup:
		return m.updateSetup(msg)
	case PhaseDashboard:
		return m.updateDashboard(msg)
	}

	return m, nil
}

func (m Model) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.setup, cmd = m.setup.Update(msg)

	if m.setup.Done() {
		m.cfg = m.setup.Config()
		m.header = header.New(m.cfg.AuthMethod)
		m.phase = PhaseDashboard
		m.layout()

		cmds := []tea.Cmd{m.initDashboardCmd(), m.wrangler.SpinnerInit()}
		if m.scanDir != "" {
			cmds = append(cmds, m.discoverProjectsFromDir(m.scanDir))
		} else {
			cmds = append(cmds, func() tea.Msg {
				return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
			})
		}
		return m, tea.Batch(cmds...)
	}

	return m, cmd
}

func (m Model) updateDashboard(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If version picker overlay is active, route key events there
	if m.wrangler.IsVersionPickerActive() {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			var cmd tea.Cmd
			m.wrangler, cmd = m.wrangler.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// If search overlay is active, route everything there
	if m.showSearch {
		return m.updateSearch(msg)
	}

	// If bindings popup is active, route everything there
	if m.showBindings {
		return m.updateBindings(msg)
	}

	// If deploy all popup is active, route everything there
	if m.showDeployAllPopup {
		return m.updateDeployAllPopup(msg)
	}

	// If env popup is active, route everything there
	if m.showEnvPopup {
		return m.updateEnvPopup(msg)
	}

	// If delete resource popup is active, route everything there
	if m.showDeletePopup {
		return m.updateDeletePopup(msg)
	}

	// If project popup is active, route everything there
	if m.showProjectPopup {
		return m.updateProjectPopup(msg)
	}

	// If remove project popup is active, route everything there
	if m.showRemoveProjectPopup {
		return m.updateRemoveProjectPopup(msg)
	}

	// If action popup is active, route everything there
	if m.showActions {
		return m.updateActions(msg)
	}

	// If envvars view is active (legacy, opened from Operations tab), route key events there
	if m.viewState == ViewEnvVars {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "ctrl+h", "ctrl+l", "1", "2", "3", "4":
				// Let global shortcuts and tab-switch keys fall through
			default:
				return m.updateEnvVars(msg)
			}
		} else {
			return m.updateEnvVars(msg)
		}
	}

	// If triggers view is active (legacy, opened from Operations tab), route key events there
	if m.viewState == ViewTriggers {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "ctrl+h", "ctrl+l", "1", "2", "3", "4":
				// Let global shortcuts and tab-switch keys fall through
			default:
				return m.updateTriggers(msg)
			}
		} else {
			return m.updateTriggers(msg)
		}
	}

	// If launcher overlay is active, route everything there
	if m.showLauncher {
		return m.updateLauncher(msg)
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		// Reset hover state on every mouse event — re-set below if still hovering.
		m.hoverTab = -1
		m.header.SetHoverIdx(-1)

		switch msg.Action {
		case tea.MouseActionMotion:
			// Track hover over tab bar.
			for _, t := range tabbar.All() {
				if z := zone.Get(t.ZoneID()); z != nil && z.InBounds(msg) {
					m.hoverTab = t
					break
				}
			}
			// Track hover over header account tabs.
			for i := 0; i < m.header.AccountCount(); i++ {
				if z := zone.Get(header.AccountZoneID(i)); z != nil && z.InBounds(msg) {
					m.header.SetHoverIdx(i)
					break
				}
			}
			return m, nil

		case tea.MouseActionRelease:
			if msg.Button == tea.MouseButtonLeft {
				// Check tab bar clicks.
				for _, t := range tabbar.All() {
					if z := zone.Get(t.ZoneID()); z != nil && z.InBounds(msg) {
						m.activeTab = t
						cmd := m.ensureViewStateForTab()
						return m, cmd
					}
				}
				// Check header account tab clicks.
				for i := 0; i < m.header.AccountCount(); i++ {
					if z := zone.Get(header.AccountZoneID(i)); z != nil && z.InBounds(msg) {
						if m.header.SetActiveIndex(i) {
							return m, m.switchAccount(m.header.ActiveAccountID(), m.header.ActiveAccountName())
						}
						return m, nil
					}
				}
			}
		}

		// Forward mouse events to the wrangler on Operations tab (project box clicks)
		if m.activeTab == tabbar.TabOperations && m.viewState == ViewWrangler {
			var cmd tea.Cmd
			m.wrangler, cmd = m.wrangler.Update(msg)
			return m, cmd
		}

		// Forward mouse events to the config view on Configuration tab
		if m.activeTab == tabbar.TabConfiguration && m.viewState != ViewEnvVars && m.viewState != ViewTriggers {
			var cmd tea.Cmd
			m.configView, cmd = m.configView.Update(msg)
			return m, cmd
		}

		// Forward mouse events to the detail panel when it's visible (for copy-on-click + list clicks)
		if m.activeTab == tabbar.TabResources ||
			(m.activeTab == tabbar.TabOperations && (m.viewState == ViewServiceList || m.viewState == ViewServiceDetail)) {
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		// Number keys switch top-level tabs — but only when not in a text input context
		// (dir browser folder creation, D1 SQL console, etc.).
		case "1", "2", "3", "4":
			if !m.isTextInputActive() {
				switch msg.String() {
				case "1":
					m.activeTab = tabbar.TabOperations
				case "2":
					m.activeTab = tabbar.TabMonitoring
				case "3":
					m.activeTab = tabbar.TabResources
				case "4":
					m.activeTab = tabbar.TabConfiguration
				}
				cmd := m.ensureViewStateForTab()
				return m, cmd
			}

		case "q":
			// Monitoring tab: quit only when idle (no active tail).
			if m.activeTab == tabbar.TabMonitoring {
				if !m.monitoring.IsActive() {
					return m, tea.Quit
				}
				// If tail is active, q is not quit — fall through.
				break
			}
			// Configuration tab: quit unless in legacy ViewEnvVars/ViewTriggers.
			// The config model's own Update handles q → tea.Quit for its categories.
			if m.activeTab == tabbar.TabConfiguration && m.viewState != ViewEnvVars && m.viewState != ViewTriggers {
				// Let it fall through to the config model's Update below
				break
			}
			if m.activeTab == tabbar.TabConfiguration {
				return m, tea.Quit
			}
			// Resources tab: quit unless D1 console is interactively focused
			if m.activeTab == tabbar.TabResources {
				if m.detail.D1Active() && m.detail.Interacting() && m.detail.Focus() == detail.FocusDetail {
					break // let it fall through to detail's Update
				}
				return m, tea.Quit
			}
			// Operations tab: only quit when not in a text-input context
			if m.viewState == ViewWrangler && !m.wrangler.IsDirBrowserActive() && !m.wrangler.CmdRunning() {
				return m, tea.Quit
			}
			if m.viewState == ViewServiceList {
				return m, tea.Quit
			}
			// In detail view, only quit if D1 console is not interactively focused
			if m.viewState == ViewServiceDetail && !(m.detail.D1Active() && m.detail.Interacting()) {
				return m, tea.Quit
			}
		case "ctrl+h":
			// Go to wrangler home screen from anywhere
			m.stopTail()
			m.stopAllParallelTails()
			m.monitoring.Clear()
			m.detail.ClearD1()
			m.envVarsFromResourceList = false
			m.triggersFromResourceList = false
			m.activeTab = tabbar.TabOperations
			m.viewState = ViewWrangler
			// Refresh deployment data if stale
			if cmd := m.refreshDeploymentsIfStale(); cmd != nil {
				return m, cmd
			}
			return m, nil
		case "ctrl+l":
			// Open the service launcher overlay
			projectName := ""
			if m.wrangler.IsMonorepo() {
				projectName = m.wrangler.RootName()
			} else if m.wrangler.HasConfig() {
				projectName = m.wrangler.Config().Name
			}
			m.launcher = launcher.New(projectName)
			m.showLauncher = true
			return m, nil
		case "ctrl+k":
			m.showSearch = true
			m.search.SetItems(m.registry.AllSearchItems())
			m.search.Reset()
			cmds := m.fetchStaleForSearch()
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
		case "ctrl+n":
			if m.activeTab == tabbar.TabOperations && m.viewState == ViewWrangler && !m.wrangler.IsDirBrowserActive() {
				if m.wrangler.IsOnProjectList() {
					// Monorepo view: create resources only
					m.showBindings = true
					m.bindingsPopup = bindings.NewMonorepo()
					return m, nil
				} else if m.wrangler.HasConfig() {
					// Navigate to Configuration tab → Bindings
					m.syncConfigProjects()
					configPath := m.wrangler.ConfigPath()
					m.configView.SelectProjectByPath(configPath)
					m.configView.SetCategory(uiconfig.CategoryBindings)
					m.activeTab = tabbar.TabConfiguration
					return m, nil
				}
			}
		case "ctrl+p":
			if m.activeTab == tabbar.TabOperations && m.viewState == ViewWrangler && !m.wrangler.IsDirBrowserActive() {
				m.showActions = true
				if m.wrangler.IsOnProjectList() {
					m.actionsPopup = m.buildMonorepoActionsPopup()
				} else {
					m.actionsPopup = m.buildWranglerActionsPopup()
				}
				return m, nil
			}
			if m.viewState == ViewServiceDetail && m.detail.InDetailView() {
				m.showActions = true
				m.actionsPopup = m.buildActionsPopup()
				return m, nil
			}
		case "d":
			// Delete focused environment shortcut (project-level only)
			if m.activeTab == tabbar.TabOperations && m.viewState == ViewWrangler && !m.wrangler.IsOnProjectList() &&
				!m.wrangler.IsDirBrowserActive() && !m.wrangler.CmdRunning() &&
				!m.wrangler.InsideBox() &&
				m.wrangler.HasConfig() {
				envName := m.wrangler.FocusedEnvName()
				if envName != "" && envName != "default" {
					configPath := m.wrangler.ConfigPath()
					workerName := m.wrangler.Config().Name
					m.showEnvPopup = true
					m.envPopup = envpopup.NewDeleteConfirm(configPath, workerName, envName)
					return m, nil
				}
			}
		case "]":
			if m.header.NextAccount() {
				return m, m.switchAccount(m.header.ActiveAccountID(), m.header.ActiveAccountName())
			}
			return m, nil
		case "[":
			if m.header.PrevAccount() {
				return m, m.switchAccount(m.header.ActiveAccountID(), m.header.ActiveAccountName())
			}
			return m, nil
		case "esc":
			// Esc on the Monitoring tab — dual-pane navigation
			if m.activeTab == tabbar.TabMonitoring {
				if m.monitoring.Focus() == monitoring.FocusRight {
					// Right pane focused → switch to left pane
					m.monitoring.SetFocusLeft()
					return m, nil
				}
				// Left pane focused → go back to Operations tab
				m.activeTab = tabbar.TabOperations
				m.viewState = ViewWrangler
				return m, nil
			}

			// Esc on Resources tab — let the detail model handle it entirely:
			// dropdown open → close, detail focus → list focus, list focus → no-op.
			if m.activeTab == tabbar.TabResources {
				// Fall through to the routing section below
				break
			}

			// Esc on Configuration tab — let the config model handle it
			// (dropdown close, mode cancel, etc.)
			if m.activeTab == tabbar.TabConfiguration && m.viewState != ViewEnvVars && m.viewState != ViewTriggers {
				// Fall through to the config model's Update below
				break
			}

			// Esc at the app level handles view-state navigation back
			switch m.viewState {
			case ViewServiceList:
				// Fallback: go home
				m.activeTab = tabbar.TabOperations
				m.viewState = ViewWrangler
				if cmd := m.refreshDeploymentsIfStale(); cmd != nil {
					return m, cmd
				}
				return m, nil
			case ViewServiceDetail:
				// Let detail handle Esc internally (detail→list transition)
				// We detect the transition after detail processes the message
			case ViewEnvVars:
				// Envvars view handles its own Esc (clear filter first, then close)
			case ViewTriggers:
				// Triggers view handles its own Esc
			case ViewWrangler:
				// Wrangler handles its own Esc (dir browser, cmd pane, etc.)
			}
		}
	}

	// Route to the active view based on current tab.
	var cmd tea.Cmd
	switch m.activeTab {
	case tabbar.TabOperations:
		if m.viewState == ViewWrangler {
			m.wrangler, cmd = m.wrangler.Update(msg)
		}
	case tabbar.TabMonitoring:
		m.monitoring, cmd = m.monitoring.Update(msg)
	case tabbar.TabResources:
		switch m.viewState {
		case ViewServiceList, ViewServiceDetail:
			m.detail, cmd = m.detail.Update(msg)
			// Sync app viewState with the detail model's internal state
			if m.detail.InDetailView() {
				m.viewState = ViewServiceDetail
			} else {
				m.viewState = ViewServiceList
			}
		}
	case tabbar.TabConfiguration:
		// Route to the unified config model (unless in legacy ViewEnvVars/ViewTriggers)
		if m.viewState != ViewEnvVars && m.viewState != ViewTriggers {
			m.configView, cmd = m.configView.Update(msg)
		}
	}
	return m, cmd
}

func (m Model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	return m, cmd
}

// updateLauncher forwards messages to the launcher overlay.
func (m Model) updateLauncher(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.launcher, cmd = m.launcher.Update(msg)
	return m, cmd
}

// isTextInputActive returns true when the user is in a text input context
// (dir browser folder creation, D1 SQL console, wrangler command running, etc.)
// where number keys should be typed rather than switching tabs.
func (m Model) isTextInputActive() bool {
	if m.viewState == ViewWrangler && m.wrangler.IsDirBrowserActive() {
		return true
	}
	if m.viewState == ViewWrangler && m.wrangler.CmdRunning() && !wcfg.IsDevAction(m.wranglerRunnerAction) {
		return true
	}
	// D1 console only blocks tab switching when in interactive mode
	if m.detail.D1Active() && m.detail.Interacting() && m.detail.Focus() == detail.FocusDetail {
		return true
	}
	// Config view text inputs (env var add/edit, triggers custom, env name add)
	if m.activeTab == tabbar.TabConfiguration && m.configView.IsTextInputActive() {
		return true
	}
	return false
}

// ensureViewStateForTab resets the viewState to the tab's root view when switching
// tabs, if the current viewState doesn't belong to the target tab.
// This prevents a blank screen when switching from e.g. Resources (ViewServiceList)
// to Operations (which doesn't render ViewServiceList).
func (m *Model) ensureViewStateForTab() tea.Cmd {
	switch m.activeTab {
	case tabbar.TabOperations:
		// Only ViewWrangler is the natural root for Operations.
		// Any other viewState (service list/detail, env vars, triggers) belongs
		// to another tab context — reset to Wrangler home.
		if m.viewState != ViewWrangler {
			m.viewState = ViewWrangler
		}
	case tabbar.TabResources:
		// Service list/detail are valid; anything else shows placeholder.
		if m.detail.Service() == "" {
			// No service selected — auto-open the dropdown
			m.detail.OpenDropdown()
			m.viewState = ViewServiceList
			m.detail.SetFocused(true)
		} else if m.viewState != ViewServiceList && m.viewState != ViewServiceDetail {
			// Returning from another tab — restore the correct view state
			if m.detail.InDetailView() {
				m.viewState = ViewServiceDetail
			} else {
				m.viewState = ViewServiceList
			}
			m.detail.SetFocused(true)
		}
		// Ensure the binding index is available for managed/bound detection.
		// Trigger a Workers fetch + index build if not yet done.
		if m.registry.GetBindingIndex() == nil && m.client != nil {
			if m.registry.GetCache("Workers") == nil {
				return m.loadServiceResources("Workers")
			}
			return m.buildBindingIndexCmd()
		}
	case tabbar.TabConfiguration:
		// Ensure the config view has up-to-date projects
		m.syncConfigProjects()
		// Reset viewState if it was from another tab (e.g. ViewServiceDetail)
		if m.viewState == ViewServiceDetail || m.viewState == ViewWrangler || m.viewState == ViewServiceList {
			m.viewState = ViewWrangler // config tab manages its own state internally
		}
	case tabbar.TabMonitoring:
		// Rebuild the worker tree from wrangler data so the left pane is up to date.
		m.refreshMonitoringWorkerTree()
	}
	return nil
}

// refreshMonitoringWorkerTree builds the worker tree from wrangler data and
// passes it to the monitoring model. Called when switching to the Monitoring tab
// or when wrangler config changes.
func (m *Model) refreshMonitoringWorkerTree() {
	workers := m.wrangler.WorkerList()
	if len(workers) == 0 && len(m.devSessions) == 0 {
		m.monitoring.SetWorkerTree(nil)
		return
	}

	var tree []monitoring.WorkerTreeEntry

	// Group workers by project name, preserving order from WorkerList()
	type projectGroup struct {
		name    string
		workers []uiwrangler.WorkerInfo
	}
	var groups []projectGroup
	groupIdx := make(map[string]int)

	for _, w := range workers {
		if idx, ok := groupIdx[w.ProjectName]; ok {
			groups[idx].workers = append(groups[idx].workers, w)
		} else {
			groupIdx[w.ProjectName] = len(groups)
			groups = append(groups, projectGroup{
				name:    w.ProjectName,
				workers: []uiwrangler.WorkerInfo{w},
			})
		}
	}

	for _, g := range groups {
		// Project header
		tree = append(tree, monitoring.WorkerTreeEntry{
			ProjectName: g.name,
			IsHeader:    true,
		})
		// Workers under this project
		for _, w := range g.workers {
			tree = append(tree, monitoring.WorkerTreeEntry{
				ProjectName: w.ProjectName,
				ScriptName:  w.ScriptName,
				EnvName:     w.EnvName,
			})
		}
	}

	// Append dev session entries below a separator
	if len(m.devSessions) > 0 {
		tree = append(tree, monitoring.WorkerTreeEntry{
			ProjectName: "Dev Mode Sessions",
			IsHeader:    true,
			IsDev:       true,
		})
		for _, ds := range m.devSessions {
			tree = append(tree, monitoring.WorkerTreeEntry{
				ProjectName: ds.ProjectName,
				ScriptName:  ds.ScriptName,
				EnvName:     ds.EnvName,
				IsDev:       true,
				DevKind:     ds.DevKind,
				DevPort:     ds.Port,
			})
		}
	}

	m.monitoring.SetWorkerTree(tree)
}

// layout recalculates component sizes based on terminal dimensions.
// Full-width layout: header(1) + tab bar(3) + content + help(1).
func (m *Model) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	headerHeight := 1
	helpHeight := 1
	contentHeight := m.height - headerHeight - tabBarHeight - helpHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	contentWidth := m.width

	m.header.SetWidth(m.width)
	m.detail.SetSize(contentWidth, contentHeight)
	m.wrangler.SetSize(contentWidth, contentHeight)
	m.monitoring.SetSize(contentWidth, contentHeight)
	m.configView.SetSize(contentWidth, contentHeight)
	// Detail content starts after: header(1) + tab bar(3) + top border(1) = 5 rows from top of terminal
	m.detail.SetYOffset(headerHeight + tabBarHeight + 1)
}

// openURL opens a URL in the user's default browser.
func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "linux":
			cmd = exec.Command("xdg-open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			return nil
		}
		_ = cmd.Start()
		return nil
	}
}
