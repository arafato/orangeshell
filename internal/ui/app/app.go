package app

import (
	"context"
	"fmt"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/actions"
	"github.com/oarafat/orangeshell/internal/ui/bindings"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/deployallpopup"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/envvars"
	"github.com/oarafat/orangeshell/internal/ui/header"
	"github.com/oarafat/orangeshell/internal/ui/launcher"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/removeprojectpopup"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/setup"
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

	// Active tail session (nil when no tail is running)
	tailSession *svc.TailSession
	tailSource  string // "wrangler" or "detail" — which view owns the current tail

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

	// Environment variables view
	envvarsView             envvars.Model
	envVarsFromResourceList bool // true when env vars view was opened from the Resources launcher

	// Cron triggers view
	triggersView             uitriggers.Model
	triggersFromResourceList bool // true when triggers view was opened from the Resources launcher

	// Deploy all popup overlay
	showDeployAllPopup bool
	deployAllPopup     deployallpopup.Model
	deployAllRunners   []*wcfg.Runner // one per project, kept for cancellation

	// Toast notification
	toastMsg    string
	toastExpiry time.Time
}

// NewModel creates the root model. If config is already set up, skips to dashboard.
func NewModel(cfg *config.Config) Model {
	phase := PhaseSetup
	if cfg.IsConfigured() {
		phase = PhaseDashboard
	}

	m := Model{
		setup:     setup.New(cfg),
		header:    header.New(cfg.AuthMethod),
		detail:    detail.New(),
		search:    search.New(),
		wrangler:  uiwrangler.New(),
		phase:     phase,
		viewState: ViewWrangler, // wrangler is the home screen
		cfg:       cfg,
		registry:  svc.NewRegistry(),
	}

	return m
}

// Init returns the initial command.
func (m Model) Init() tea.Cmd {
	if m.phase == PhaseDashboard {
		// Discover wrangler configs immediately (pure filesystem I/O) in parallel with auth.
		// This eliminates the "Loading wrangler configuration..." spinner delay that
		// previously waited for API auth to complete before starting discovery.
		cmds := []tea.Cmd{m.initDashboardCmd(), m.discoverProjectsCmd(), m.wrangler.SpinnerInit()}
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
		// When Workers list loads, build the binding index in the background
		var indexCmd tea.Cmd
		if msg.ServiceName == "Workers" && msg.Err == nil && msg.Resources != nil {
			indexCmd = m.buildBindingIndexCmd()
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
		m.detail.SetResources(msg.Resources, msg.Err, msg.NotIntegrated)
		// Update search items after loading
		if msg.Err == nil {
			m.search.SetItems(m.registry.AllSearchItems())
		}
		if indexCmd != nil {
			return m, indexCmd
		}
		return m, nil

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
		// If this is a D1 detail, initialize the SQL console and load schema async
		if msg.Err == nil && msg.ServiceName == "D1" && msg.Detail != nil {
			inputCmd := m.detail.InitD1Console(msg.ResourceID)
			schemaCmd := m.loadD1Schema(msg.ResourceID)
			return m, tea.Batch(inputCmd, schemaCmd, m.detail.SpinnerInit())
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

	// Tail lifecycle messages
	case detail.TailStartMsg:
		if m.client == nil {
			return m, nil
		}
		m.tailSource = "detail"
		m.detail.SetTailStarting()
		accountID := m.registry.ActiveAccountID()
		return m, m.startTailCmd(accountID, msg.ScriptName)

	case detail.TailStartedMsg:
		m.tailSession = msg.Session
		if m.tailSource == "wrangler" {
			m.wrangler.TailConnected()
		} else {
			m.detail.SetTailStarted()
		}
		return m, m.waitForTailLines()

	case detail.TailLogMsg:
		if m.tailSession == nil {
			return m, nil
		}
		if m.tailSource == "wrangler" {
			m.wrangler.AppendTailLines(msg.Lines)
		} else {
			m.detail.AppendTailLines(msg.Lines)
		}
		// Continue polling for more lines
		return m, m.waitForTailLines()

	case detail.TailErrorMsg:
		if m.tailSource == "wrangler" {
			m.wrangler.SetTailError(msg.Err)
		} else {
			m.detail.SetTailError(msg.Err)
		}
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
		// "t" key pressed in wrangler view — start tailing
		if m.client == nil {
			return m, nil
		}
		// Stop any existing tail
		m.stopTail()
		m.detail.ClearTail()
		// Start tail in wrangler view
		m.tailSource = "wrangler"
		m.wrangler.StartTail(msg.ScriptName)
		accountID := m.registry.ActiveAccountID()
		return m, tea.Batch(m.startTailCmd(accountID, msg.ScriptName), m.wrangler.SpinnerInit())

	case uiwrangler.TailStoppedMsg:
		// "t" key pressed while tail is active — stop it
		m.stopTail()
		m.detail.ClearTail()
		return m, nil

	// Parallel tail lifecycle messages
	case uiwrangler.ParallelTailStartMsg:
		if m.client == nil {
			return m, nil
		}
		// Stop any existing single tail and parallel tails
		m.stopTail()
		m.detail.ClearTail()
		m.stopAllParallelTails()
		// Start parallel tailing
		m.wrangler.StartParallelTail(msg.EnvName, msg.Scripts)
		m.parallelTailActive = true
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
		m.wrangler.ParallelTailSetConnected(msg.ScriptName)
		m.wrangler.ParallelTailSetSessionID(msg.ScriptName, msg.Session.ID)
		return m, m.waitForParallelTailLines(msg.ScriptName, msg.Session)

	case parallelTailLogMsg:
		if !m.parallelTailActive {
			return m, nil
		}
		m.wrangler.ParallelTailAppendLines(msg.ScriptName, msg.Lines)
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
		m.wrangler.ParallelTailSetError(msg.ScriptName, msg.Err)
		return m, nil

	case parallelTailSessionDoneMsg:
		// Channel closed for one session — nothing to do, pane stays with last lines
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
		m.envVarsFromResourceList = false
		return m, m.openEnvVarsView(msg.ConfigPath, msg.EnvName, msg.ProjectName)

	case uiwrangler.ShowTriggersMsg:
		m.triggersFromResourceList = false
		return m, m.openTriggersView(msg.ConfigPath, msg.ProjectName)

	case uiwrangler.CmdOutputMsg:
		m.wrangler.AppendCmdOutput(msg.Line)
		return m, waitForWranglerOutput(m.wranglerRunner)

	case uiwrangler.CmdDoneMsg:
		// Drain any remaining lines before finishing
		if m.wranglerRunner != nil {
			for line := range m.wranglerRunner.LinesCh() {
				m.wrangler.AppendCmdOutput(line)
			}
		}
		m.wrangler.FinishCommand(msg.Result)
		action := m.wranglerRunnerAction
		m.wranglerRunner = nil
		m.wranglerRunnerAction = ""

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
			m.viewState = ViewWrangler
			if cmd := m.refreshDeploymentsIfStale(); cmd != nil {
				return m, cmd
			}
			return m, nil
		}
		if msg.ServiceName == "Env Variables" {
			return m, m.navigateToEnvVarsList()
		}
		if msg.ServiceName == "Triggers" {
			return m, m.navigateToTriggersList()
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
		return m, m.initDashboardCmd()
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

	// If envvars view is active, route key events there (but let global shortcuts through)
	if m.viewState == ViewEnvVars {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "ctrl+h":
				// Let ctrl+h fall through to the global handler below
			case "ctrl+l":
				// Let ctrl+l fall through to the global handler below
			default:
				return m.updateEnvVars(msg)
			}
		} else {
			return m.updateEnvVars(msg)
		}
	}

	// If triggers view is active, route key events there (but let global shortcuts through)
	if m.viewState == ViewTriggers {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.String() {
			case "ctrl+h":
				// Let ctrl+h fall through to the global handler below
			case "ctrl+l":
				// Let ctrl+l fall through to the global handler below
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
		// Forward mouse events to the detail panel regardless of view (for copy-on-click)
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			// Only quit when not in a text-input context
			if m.viewState == ViewWrangler && !m.wrangler.IsDirBrowserActive() && !m.wrangler.CmdRunning() {
				return m, tea.Quit
			}
			if m.viewState == ViewServiceList {
				return m, tea.Quit
			}
			// In detail view, only quit if D1 console is not focused
			if m.viewState == ViewServiceDetail && !m.detail.D1Active() {
				return m, tea.Quit
			}
		case "ctrl+h":
			// Go to wrangler home screen from anywhere
			m.stopTail()
			m.detail.ClearTail()
			m.detail.ClearD1()
			m.envVarsFromResourceList = false
			m.triggersFromResourceList = false
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
			if m.viewState == ViewWrangler && !m.wrangler.IsDirBrowserActive() {
				if m.wrangler.IsOnProjectList() {
					// Monorepo view: create resources only
					m.showBindings = true
					m.bindingsPopup = bindings.NewMonorepo()
					return m, nil
				} else if m.wrangler.HasConfig() {
					// Project view: create or assign existing resources
					configPath := m.wrangler.ConfigPath()
					envName := m.wrangler.FocusedEnvName()
					workerName := m.wrangler.FocusedWorkerName()
					m.showBindings = true
					m.bindingsPopup = bindings.NewProject(configPath, envName, workerName)
					return m, nil
				}
			}
		case "ctrl+p":
			if m.viewState == ViewWrangler && !m.wrangler.IsDirBrowserActive() {
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
			if m.viewState == ViewWrangler && !m.wrangler.IsOnProjectList() &&
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
			// Esc at the app level handles view-state navigation back
			switch m.viewState {
			case ViewServiceList:
				// Service list → go home
				m.viewState = ViewWrangler
				// Refresh deployment data if stale
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

	// Route to the active view
	var cmd tea.Cmd
	switch m.viewState {
	case ViewWrangler:
		m.wrangler, cmd = m.wrangler.Update(msg)
	case ViewServiceList, ViewServiceDetail:
		wasDetail := m.detail.InDetailView()
		m.detail, cmd = m.detail.Update(msg)
		// Detect detail→list transition (detail handled Esc internally)
		if wasDetail && !m.detail.InDetailView() {
			m.viewState = ViewServiceList
		}
	case ViewEnvVars:
		m.envvarsView, cmd = m.envvarsView.Update(msg)
	case ViewTriggers:
		m.triggersView, cmd = m.triggersView.Update(msg)
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

// layout recalculates component sizes based on terminal dimensions.
// Full-width layout: header(1) + content + help(1).
func (m *Model) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	headerHeight := 1
	helpHeight := 1
	contentHeight := m.height - headerHeight - helpHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	contentWidth := m.width

	m.header.SetWidth(m.width)
	m.detail.SetSize(contentWidth, contentHeight)
	m.wrangler.SetSize(contentWidth, contentHeight)
	// Detail content starts after: header(1) + top border(1) = 2 rows from top of terminal
	m.detail.SetYOffset(headerHeight + 1)
}
