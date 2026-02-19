package app

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/actions"
	uiai "github.com/oarafat/orangeshell/internal/ui/ai"
	"github.com/oarafat/orangeshell/internal/ui/bindings"
	"github.com/oarafat/orangeshell/internal/ui/buildstokenpopup"
	uiconfig "github.com/oarafat/orangeshell/internal/ui/config"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/deployallpopup"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/header"
	"github.com/oarafat/orangeshell/internal/ui/launcher"
	"github.com/oarafat/orangeshell/internal/ui/monitoring"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/removeprojectpopup"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/setup"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
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

// SetProgramMsg carries the *tea.Program reference so the model can use p.Send()
// from background goroutines (e.g., provisioning progress callbacks).
type SetProgramMsg struct {
	Program *tea.Program
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

// devCronTriggerDoneMsg carries the result of triggering a cron handler on a dev worker.
type devCronTriggerDoneMsg struct {
	ScriptName string
	Err        error
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
	devRunners            map[string]*devRunner // keyed by "projectName:envName" — long-lived dev servers
	cmdRunners            map[string]*cmdRunner // keyed by "projectName:envName" — short-lived commands (deploy, delete, etc.)
	wranglerVersionRunner *wcfg.Runner          // separate runner for background version fetches
	vhVersionRunner       *wcfg.Runner          // version history: wrangler versions list (Resources tab)
	vhDeploymentRunner    *wcfg.Runner          // version history: wrangler deployments list (Resources tab)

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

	// Builds API token popup overlay
	showBuildsTokenPopup bool
	buildsTokenPopup     buildstokenpopup.Model
	buildsTokenDeclined  bool // suppresses repeated prompts within a session

	// Configuration tab (unified model)
	configView uiconfig.Model

	// AI tab model
	aiTab uiai.Model

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

	// program holds the *tea.Program reference for background goroutine → UI
	// communication (e.g., AI provisioning progress callbacks).
	program *tea.Program
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
		aiTab:      uiai.New(),
		phase:      phase,
		viewState:  ViewWrangler, // wrangler is the home screen
		activeTab:  tabbar.TabOperations,
		hoverTab:   -1, // no tab hovered
		cfg:        cfg,
		registry:   svc.NewRegistry(),
		scanDir:    scanDir,
		devRunners: make(map[string]*devRunner),
		cmdRunners: make(map[string]*cmdRunner),
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

// getBuildsClient creates the Workers Builds API client.
// Prefers the dedicated BuildsAPIToken (which has Workers CI Read scope),
// falling back to the primary auth credentials.
func (m *Model) getBuildsClient() *api.BuildsClient {
	accountID := m.registry.ActiveAccountID()

	// 1. Prefer dedicated builds token (always has the right scope)
	if m.cfg.BuildsAPIToken != "" {
		return api.NewBuildsClient(accountID, "", "", m.cfg.BuildsAPIToken)
	}

	// 2. Fall back to primary credentials
	var authEmail, authKey, authToken string
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		authEmail = m.cfg.Email
		authKey = m.cfg.APIKey
	case config.AuthMethodAPIToken:
		authToken = m.cfg.APIToken
	case config.AuthMethodOAuth:
		authToken = m.cfg.OAuthAccessToken
	}

	return api.NewBuildsClient(accountID, authEmail, authKey, authToken)
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
		(*Model).handleBuildsTokenPopupMsg,
		(*Model).handleDetailMsg,
		(*Model).handleWranglerMsg,
		(*Model).handleMonitoringMsg,
		(*Model).handleAIMsg,
		(*Model).handleOverlayMsg,
	}
	for _, h := range handlers {
		if result, cmd, handled := h(&m, msg); handled {
			return result, cmd
		}
	}

	switch msg := msg.(type) {
	case SetProgramMsg:
		m.program = msg.Program
		return m, nil

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

		// Load AI settings from config
		m.aiTab.LoadConfig(m.cfg)

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

	case toastExpireMsg:
		if time.Now().After(m.toastExpiry) {
			m.toastMsg = ""
		}
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

	// If builds token popup is active, route everything there
	if m.showBuildsTokenPopup {
		return m.updateBuildsTokenPopup(msg)
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
		if m.activeTab == tabbar.TabConfiguration {
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
		case "1", "2", "3", "4", "5":
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
				case "5":
					m.activeTab = tabbar.TabAI
				}
				cmd := m.ensureViewStateForTab()
				return m, cmd
			}

		case "q":
			// AI tab: only quit from settings or context pane (not chat — q is a typeable char)
			if m.activeTab == tabbar.TabAI {
				if m.aiTab.CurrentMode() == uiai.ModeSettings || m.aiTab.Focus() == uiai.FocusContext {
					return m, tea.Quit
				}
				break // fall through to AI tab Update to type 'q' in chat
			}
			// Monitoring tab: quit only when idle (no active tail).
			if m.activeTab == tabbar.TabMonitoring {
				if !m.monitoring.IsActive() {
					return m, tea.Quit
				}
				// If tail is active, q is not quit — fall through.
				break
			}
			// Configuration tab: the config model's own Update handles q → tea.Quit.
			if m.activeTab == tabbar.TabConfiguration {
				break
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
			if m.activeTab == tabbar.TabConfiguration {
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
			// Refresh active CmdPane after navigation (env box change, project drill-in/out)
			m.refreshActiveCmdPane()
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
		m.configView, cmd = m.configView.Update(msg)
	case tabbar.TabAI:
		// Refresh context sources before routing (keeps line counts up to date)
		if _, isKey := msg.(tea.KeyMsg); isKey {
			m.refreshAIContextSources()
		}
		m.aiTab, cmd = m.aiTab.Update(msg)
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
	if m.viewState == ViewWrangler && m.wrangler.CmdRunning() {
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
	// AI tab chat input is active when the chat pane is focused
	if m.activeTab == tabbar.TabAI && m.aiTab.IsTextInputActive() {
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
		// Refresh CmdPane + dev badges for the currently focused project/env
		m.refreshActiveCmdPane()
		m.syncDevBadges()
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
	case tabbar.TabAI:
		// Refresh context sources from monitoring grid panes
		m.refreshAIContextSources()
		// Refresh file sources from wrangler project directories
		m.refreshAIFileSources()
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
	m.aiTab.SetSize(contentWidth, contentHeight)
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
