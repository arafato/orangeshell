package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"path/filepath"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/actions"
	"github.com/oarafat/orangeshell/internal/ui/bindings"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/header"
	"github.com/oarafat/orangeshell/internal/ui/launcher"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/setup"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
	"github.com/oarafat/orangeshell/version"
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

// toastExpireMsg fires after the toast display duration to clear the toast.
type toastExpireMsg struct{}

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

	// Create project popup overlay
	showProjectPopup bool
	projectPopup     projectpopup.Model

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
		// Staleness check: discard responses from a different account
		if msg.AccountID != "" && msg.AccountID != m.registry.ActiveAccountID() {
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
		// Staleness check: discard responses from a different account
		if msg.AccountID != "" && msg.AccountID != m.registry.ActiveAccountID() {
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

	case bindingIndexBuiltMsg:
		// Staleness check: discard if account changed
		if msg.accountID != m.registry.ActiveAccountID() {
			return m, nil
		}
		m.registry.SetBindingIndex(msg.index)
		return m, nil

	// Wrangler messages
	case uiwrangler.ConfigLoadedMsg:
		m.wrangler.SetConfig(msg.Config, msg.Path, msg.Err)
		// Trigger deployment fetching for single-project environments
		if msg.Err == nil && msg.Config != nil {
			return m, m.fetchSingleProjectDeployments(msg.Config)
		}
		return m, nil

	case uiwrangler.EnvDeploymentLoadedMsg:
		// Discard stale responses from a previous account
		if msg.AccountID != "" && msg.AccountID != m.registry.ActiveAccountID() {
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
		// Discard stale responses from a previous account
		if msg.AccountID != "" && msg.AccountID != m.registry.ActiveAccountID() {
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

	// Binding popup messages
	case bindings.CloseMsg:
		m.showBindings = false
		return m, nil

	case bindings.ListResourcesMsg:
		return m, m.listResourcesCmd(msg.ResourceType)

	case bindings.ResourcesLoadedMsg:
		m.bindingsPopup, _ = m.bindingsPopup.Update(msg)
		return m, nil

	case bindings.CreateResourceMsg:
		return m, m.createResourceCmd(msg.ResourceType, msg.Name)

	case bindings.CreateResourceDoneMsg:
		m.bindingsPopup, _ = m.bindingsPopup.Update(msg)
		return m, nil

	case bindings.WriteBindingMsg:
		return m, m.writeBindingCmd(msg.ConfigPath, msg.EnvName, msg.Binding)

	case bindings.WriteBindingDoneMsg:
		var cmd tea.Cmd
		m.bindingsPopup, cmd = m.bindingsPopup.Update(msg)
		return m, cmd

	case bindings.DoneMsg:
		m.showBindings = false
		// Re-parse the config to refresh bindings display
		if msg.ConfigPath != "" {
			cfg, err := wcfg.Parse(msg.ConfigPath)
			if err != nil {
				m.toastMsg = fmt.Sprintf("Config reload error: %v", err)
				m.toastExpiry = time.Now().Add(3 * time.Second)
			} else {
				m.wrangler.ReloadConfig(msg.ConfigPath, cfg)
				m.toastMsg = "Binding added"
				m.toastExpiry = time.Now().Add(3 * time.Second)
			}
		}
		// If a resource was created, refresh the corresponding service cache
		if msg.ResourceType != "" {
			if svcName := resourceTypeToServiceName(msg.ResourceType); svcName != "" {
				return m, m.backgroundRefresh(svcName)
			}
		}
		return m, nil

	// Add environment popup messages
	case envpopup.CloseMsg:
		m.showEnvPopup = false
		return m, nil

	case envpopup.CreateEnvMsg:
		return m, m.createEnvCmd(msg.ConfigPath, msg.EnvName)

	case envpopup.CreateEnvDoneMsg:
		var cmd tea.Cmd
		m.envPopup, cmd = m.envPopup.Update(msg)
		return m, cmd

	case envpopup.DeleteEnvMsg:
		return m, m.deleteEnvCmd(msg.ConfigPath, msg.EnvName)

	case envpopup.DeleteEnvDoneMsg:
		var cmd tea.Cmd
		m.envPopup, cmd = m.envPopup.Update(msg)
		return m, cmd

	case envpopup.DoneMsg:
		m.showEnvPopup = false
		// Re-parse the config to refresh the environment display
		if msg.ConfigPath != "" {
			cfg, err := wcfg.Parse(msg.ConfigPath)
			if err != nil {
				m.toastMsg = fmt.Sprintf("Config reload error: %v", err)
				m.toastExpiry = time.Now().Add(3 * time.Second)
			} else {
				m.wrangler.ReloadConfig(msg.ConfigPath, cfg)
				if m.envPopup.IsDeleteMode() {
					m.toastMsg = "Environment deleted"
				} else {
					m.toastMsg = "Environment added"
				}
				m.toastExpiry = time.Now().Add(3 * time.Second)
			}
		}
		return m, nil

	// Create project popup messages
	case projectpopup.CloseMsg:
		m.showProjectPopup = false
		return m, nil

	case projectpopup.CreateProjectMsg:
		return m, m.createProjectCmd(msg.Name, msg.Lang, msg.Dir)

	case projectpopup.CreateProjectDoneMsg:
		var cmd tea.Cmd
		m.projectPopup, cmd = m.projectPopup.Update(msg)
		return m, cmd

	case projectpopup.DoneMsg:
		m.showProjectPopup = false
		m.toastMsg = "Project created"
		m.toastExpiry = time.Now().Add(3 * time.Second)
		// Rescan the monorepo root dir to pick up the new project
		rootDir := m.wrangler.RootDir()
		if rootDir != "" {
			return m, m.discoverProjectsFromDir(rootDir)
		}
		return m, m.discoverProjectsCmd()

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

	// If env popup is active, route everything there
	if m.showEnvPopup {
		return m.updateEnvPopup(msg)
	}

	// If project popup is active, route everything there
	if m.showProjectPopup {
		return m.updateProjectPopup(msg)
	}

	// If action popup is active, route everything there
	if m.showActions {
		return m.updateActions(msg)
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

// fetchStaleForSearch triggers background fetches for all registered services
// that have no cache or stale cache. Sets the search fetching counter so the UI
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

// stopWranglerRunner cancels any running wrangler command.
func (m *Model) stopWranglerRunner() {
	if m.wranglerRunner != nil {
		m.wranglerRunner.Stop()
		m.wranglerRunner = nil
	}
}

// navigateToService switches to a service list view, using cached data if available.
// If the cache is fresh (<CacheTTL), it is shown without a background refresh.
// If the cache is stale, it is shown immediately and a background refresh is triggered.
// If there is no cache, a loading spinner is shown while data is fetched.
func (m *Model) navigateToService(name string) tea.Cmd {
	// Stop any active tail/D1 session when switching services
	m.stopTail()
	m.detail.ClearTail()
	m.detail.ClearD1()

	m.viewState = ViewServiceList
	m.detail.SetFocused(true)

	entry := m.registry.GetCache(name)
	if entry != nil {
		if !m.registry.IsCacheStale(name) {
			// Cache is fresh — show it without a background refresh
			m.detail.SetServiceFresh(name, entry.Resources)
			return nil
		}
		// Cache is stale — show it and trigger a background refresh
		cmd := m.detail.SetServiceWithCache(name, entry.Resources)
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
}

// switchAccount handles switching to a different account. Re-registers services with the
// new accountID. If currently viewing a service, reloads it with the new account's data.
func (m *Model) switchAccount(accountID, accountName string) tea.Cmd {
	// Stop any active tail session and wrangler command
	m.stopTail()
	m.stopAllParallelTails()
	m.detail.ClearTail()
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
		deployCmd = m.fetchAllProjectDeploymentsForced()
	} else if cfg := m.wrangler.Config(); cfg != nil {
		deployCmd = m.fetchSingleProjectDeploymentsForced(cfg)
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

// --- Tail lifecycle helpers ---

// startTailCmd returns a command that creates a tail session via the API.
func (m Model) startTailCmd(accountID, scriptName string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx := context.Background()
		session, err := svc.StartTail(ctx, client.CF, accountID, scriptName)
		if err != nil {
			return detail.TailErrorMsg{Err: err}
		}
		return detail.TailStartedMsg{Session: session}
	}
}

// waitForTailLines returns a command that blocks on the tail session's channel
// and returns a TailLogMsg when new lines arrive.
func (m Model) waitForTailLines() tea.Cmd {
	session := m.tailSession
	if session == nil {
		return nil
	}
	return func() tea.Msg {
		lines, ok := <-session.LinesChan()
		if !ok {
			// Channel closed — tail ended
			return detail.TailStoppedMsg{}
		}
		return detail.TailLogMsg{Lines: lines}
	}
}

// stopTail closes the active tail session and cleans up both views.
func (m *Model) stopTail() {
	if m.tailSession == nil {
		return
	}
	// Stop in a background goroutine to avoid blocking the UI
	session := m.tailSession
	client := m.client
	m.tailSession = nil

	// Clean up the view that owns this tail
	if m.tailSource == "wrangler" {
		m.wrangler.StopTailPane()
	} else {
		m.detail.SetTailStopped()
	}
	m.tailSource = ""

	go func() {
		ctx := context.Background()
		svc.StopTail(ctx, client.CF, session)
	}()
}

// --- Parallel tail lifecycle helpers ---

// startParallelTailSessionCmd returns a command that creates a single parallel tail session.
func (m Model) startParallelTailSessionCmd(accountID, scriptName string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx := context.Background()
		session, err := svc.StartTail(ctx, client.CF, accountID, scriptName)
		if err != nil {
			return parallelTailErrorMsg{ScriptName: scriptName, Err: err}
		}
		return parallelTailStartedMsg{ScriptName: scriptName, Session: session}
	}
}

// waitForParallelTailLines returns a command that blocks on a single parallel tail
// session's channel and returns a parallelTailLogMsg when new lines arrive.
func (m Model) waitForParallelTailLines(scriptName string, session *svc.TailSession) tea.Cmd {
	if session == nil {
		return nil
	}
	return func() tea.Msg {
		lines, ok := <-session.LinesChan()
		if !ok {
			// Channel closed — session ended
			return parallelTailSessionDoneMsg{ScriptName: scriptName}
		}
		return parallelTailLogMsg{ScriptName: scriptName, Lines: lines}
	}
}

// stopAllParallelTails closes all parallel tail sessions and cleans up state.
func (m *Model) stopAllParallelTails() {
	if !m.parallelTailActive {
		return
	}
	sessions := m.parallelTailSessions
	client := m.client
	m.parallelTailSessions = nil
	m.parallelTailActive = false
	m.wrangler.StopParallelTail()

	// Stop all sessions in the background to avoid blocking the UI
	if len(sessions) > 0 {
		go func() {
			ctx := context.Background()
			for _, s := range sessions {
				svc.StopTail(ctx, client.CF, s)
			}
		}()
	}
}

// --- Wrangler config helpers ---

// discoverProjectsCmd returns a command that discovers wrangler projects in the CWD tree.
// If 0 found: sends ConfigLoadedMsg{nil, "", nil} (no config)
// If 1 found: sends ConfigLoadedMsg with parsed config (backward compatible)
// If 2+ found: sends ProjectsDiscoveredMsg for monorepo mode
func (m Model) discoverProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		projects := wcfg.DiscoverProjects(".")
		cwd, _ := filepath.Abs(".")
		rootName := filepath.Base(cwd)

		switch len(projects) {
		case 0:
			return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
		case 1:
			// Single project — backward compatible: parse and return ConfigLoadedMsg
			cfg, err := wcfg.Parse(projects[0].ConfigPath)
			return uiwrangler.ConfigLoadedMsg{Config: cfg, Path: projects[0].ConfigPath, Err: err}
		default:
			// Monorepo — return all projects for the project list view
			return uiwrangler.ProjectsDiscoveredMsg{Projects: projects, RootName: rootName, RootDir: cwd}
		}
	}
}

// discoverProjectsFromDir returns a command that discovers wrangler projects starting
// from a user-specified directory (via the directory browser). Uses the same 0/1/2+
// branching as discoverProjectsCmd so monorepo directories are handled correctly.
func (m Model) discoverProjectsFromDir(dir string) tea.Cmd {
	return func() tea.Msg {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			absDir = dir
		}
		rootName := filepath.Base(absDir)

		projects := wcfg.DiscoverProjects(absDir)

		switch len(projects) {
		case 0:
			return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
		case 1:
			cfg, err := wcfg.Parse(projects[0].ConfigPath)
			return uiwrangler.ConfigLoadedMsg{Config: cfg, Path: projects[0].ConfigPath, Err: err}
		default:
			return uiwrangler.ProjectsDiscoveredMsg{Projects: projects, RootName: rootName, RootDir: absDir}
		}
	}
}

// fetchAllProjectDeployments returns a batch command that fetches deployment data
// for every project+environment combination in the monorepo whose deployment cache
// is stale or missing. Fresh entries are skipped to avoid unnecessary API calls.
// Results arrive as ProjectDeploymentLoadedMsg.
func (m Model) fetchAllProjectDeployments() tea.Cmd {
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}

	accountID := m.registry.ActiveAccountID()
	projectConfigs := m.wrangler.ProjectConfigs()
	var cmds []tea.Cmd

	for i, pc := range projectConfigs {
		if pc.Config == nil {
			continue
		}
		for _, envName := range pc.Config.EnvNames() {
			scriptName := pc.Config.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			if !m.registry.IsDeploymentCacheStale(scriptName) {
				continue // cache is fresh, skip
			}
			cmds = append(cmds, m.fetchProjectDeployment(workersSvc, accountID, i, envName, scriptName))
		}
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fetchAllProjectDeploymentsForced is like fetchAllProjectDeployments but ignores
// cache staleness — every environment is fetched unconditionally. Used after
// mutating actions (deploy, versions deploy) and account switches.
func (m Model) fetchAllProjectDeploymentsForced() tea.Cmd {
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}

	accountID := m.registry.ActiveAccountID()
	projectConfigs := m.wrangler.ProjectConfigs()
	var cmds []tea.Cmd

	for i, pc := range projectConfigs {
		if pc.Config == nil {
			continue
		}
		for _, envName := range pc.Config.EnvNames() {
			scriptName := pc.Config.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			cmds = append(cmds, m.fetchProjectDeployment(workersSvc, accountID, i, envName, scriptName))
		}
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fetchSingleProjectDeployments returns a batch command that fetches deployment data
// for every environment in a single-project wrangler config whose deployment cache
// is stale or missing. Results arrive as EnvDeploymentLoadedMsg.
func (m Model) fetchSingleProjectDeployments(cfg *wcfg.WranglerConfig) tea.Cmd {
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}

	accountID := m.registry.ActiveAccountID()
	var cmds []tea.Cmd
	for _, envName := range cfg.EnvNames() {
		scriptName := cfg.ResolvedEnvName(envName)
		if scriptName == "" {
			continue
		}
		if !m.registry.IsDeploymentCacheStale(scriptName) {
			continue // cache is fresh, skip
		}
		cmds = append(cmds, m.fetchEnvDeployment(workersSvc, accountID, envName, scriptName))
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fetchSingleProjectDeploymentsForced is like fetchSingleProjectDeployments but
// ignores cache staleness. Used after mutating actions and account switches.
func (m Model) fetchSingleProjectDeploymentsForced(cfg *wcfg.WranglerConfig) tea.Cmd {
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}

	accountID := m.registry.ActiveAccountID()
	var cmds []tea.Cmd
	for _, envName := range cfg.EnvNames() {
		scriptName := cfg.ResolvedEnvName(envName)
		if scriptName == "" {
			continue
		}
		cmds = append(cmds, m.fetchEnvDeployment(workersSvc, accountID, envName, scriptName))
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fetchEnvDeployment returns a command that fetches the active deployment for
// a single env in a single-project config.
func (m Model) fetchEnvDeployment(workersSvc *svc.WorkersService, accountID, envName, scriptName string) tea.Cmd {
	return func() tea.Msg {
		subdomain, _ := workersSvc.GetSubdomain()

		dep, err := workersSvc.GetActiveDeployment(scriptName)
		if err != nil {
			return uiwrangler.EnvDeploymentLoadedMsg{
				AccountID:  accountID,
				EnvName:    envName,
				ScriptName: scriptName,
				Subdomain:  subdomain,
				Err:        err,
			}
		}

		var display *uiwrangler.DeploymentDisplay
		if dep != nil {
			display = &uiwrangler.DeploymentDisplay{}
			for _, v := range dep.Versions {
				shortID := v.VersionID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				display.Versions = append(display.Versions, uiwrangler.VersionSplit{
					ShortID:    shortID,
					Percentage: v.Percentage,
				})
			}
			if subdomain != "" {
				display.URL = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
			}
		}

		return uiwrangler.EnvDeploymentLoadedMsg{
			AccountID:  accountID,
			EnvName:    envName,
			ScriptName: scriptName,
			Deployment: display,
			Subdomain:  subdomain,
		}
	}
}

// displayToDeploymentInfo converts a UI DeploymentDisplay back to a service DeploymentInfo for caching.
// Returns nil if the display is nil or has no versions.
func displayToDeploymentInfo(d *uiwrangler.DeploymentDisplay) *svc.DeploymentInfo {
	if d == nil || len(d.Versions) == 0 {
		return nil
	}
	info := &svc.DeploymentInfo{}
	for _, v := range d.Versions {
		info.Versions = append(info.Versions, svc.DeploymentVersionInfo{
			VersionID:  v.ShortID,
			Percentage: v.Percentage,
		})
	}
	return info
}

// deploymentInfoToDisplay converts a cached DeploymentInfo to a UI DeploymentDisplay.
// Returns nil if the info is nil or has no versions.
func deploymentInfoToDisplay(info *svc.DeploymentInfo, scriptName, subdomain string) *uiwrangler.DeploymentDisplay {
	if info == nil || len(info.Versions) == 0 {
		return nil
	}
	display := &uiwrangler.DeploymentDisplay{}
	for _, v := range info.Versions {
		shortID := v.VersionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		display.Versions = append(display.Versions, uiwrangler.VersionSplit{
			ShortID:    shortID,
			Percentage: v.Percentage,
		})
	}
	if subdomain != "" {
		display.URL = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
	}
	return display
}

// restoreDeploymentsFromCache populates the wrangler UI with cached deployment data
// for the active account. Called on account switch for instant display while background
// refresh fetches fresh data.
func (m *Model) restoreDeploymentsFromCache() {
	caches := m.registry.GetAllDeploymentCaches()
	if len(caches) == 0 {
		return
	}

	if m.wrangler.IsMonorepo() {
		// Restore into ProjectBoxes
		for i, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			for _, envName := range pc.Config.EnvNames() {
				scriptName := pc.Config.ResolvedEnvName(envName)
				if scriptName == "" {
					continue
				}
				if entry, ok := caches[scriptName]; ok {
					display := deploymentInfoToDisplay(entry.Deployment, scriptName, entry.Subdomain)
					m.wrangler.SetProjectDeployment(i, envName, display, entry.Subdomain)
				}
			}
		}
	} else if cfg := m.wrangler.Config(); cfg != nil {
		// Restore into EnvBoxes
		for _, envName := range cfg.EnvNames() {
			scriptName := cfg.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			if entry, ok := caches[scriptName]; ok {
				display := deploymentInfoToDisplay(entry.Deployment, scriptName, entry.Subdomain)
				m.wrangler.SetEnvDeployment(envName, display, entry.Subdomain)
			}
		}
	}
}

// fetchProjectDeployment returns a command that fetches the active deployment for
// a single worker script and constructs its workers.dev URL using the cached subdomain.
func (m Model) fetchProjectDeployment(workersSvc *svc.WorkersService, accountID string, projectIdx int, envName, scriptName string) tea.Cmd {
	return func() tea.Msg {
		// Fetch subdomain (cached after first call)
		subdomain, _ := workersSvc.GetSubdomain()

		// Fetch active deployment
		dep, err := workersSvc.GetActiveDeployment(scriptName)
		if err != nil {
			return uiwrangler.ProjectDeploymentLoadedMsg{
				AccountID:    accountID,
				ProjectIndex: projectIdx,
				EnvName:      envName,
				ScriptName:   scriptName,
				Subdomain:    subdomain,
				Err:          err,
			}
		}

		var display *uiwrangler.DeploymentDisplay
		if dep != nil {
			display = &uiwrangler.DeploymentDisplay{}
			for _, v := range dep.Versions {
				shortID := v.VersionID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				display.Versions = append(display.Versions, uiwrangler.VersionSplit{
					ShortID:    shortID,
					Percentage: v.Percentage,
				})
			}
			if subdomain != "" {
				display.URL = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
			}
		}

		return uiwrangler.ProjectDeploymentLoadedMsg{
			AccountID:    accountID,
			ProjectIndex: projectIdx,
			EnvName:      envName,
			ScriptName:   scriptName,
			Deployment:   display,
			Subdomain:    subdomain,
		}
	}
}

// startWranglerCmd creates a Runner and starts the wrangler command.
func (m *Model) startWranglerCmd(action, envName string) tea.Cmd {
	if m.wranglerRunner != nil && m.wranglerRunner.IsRunning() {
		// Don't start a new command while one is running
		return nil
	}

	// Stop any active wrangler tail since the CmdPane is being taken over
	if m.tailSource == "wrangler" && m.tailSession != nil {
		m.stopTail()
	}

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		AccountID:  m.registry.ActiveAccountID(),
	}

	runner := wcfg.NewRunner()
	m.wranglerRunner = runner
	m.wranglerRunnerAction = action
	m.wrangler.StartCommand(action, envName)

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.CmdDoneMsg{Result: wcfg.RunResult{ExitCode: 1, Err: err}}
			}
			// Read first output line (or done signal)
			return readWranglerOutputMsg(runner)
		},
		m.wrangler.SpinnerInit(),
	)
}

// readWranglerOutputMsg reads the next output line or done signal from the runner.
// Since linesCh is closed before doneCh fires, we always drain lines first.
func readWranglerOutputMsg(runner *wcfg.Runner) tea.Msg {
	// Read lines until the channel is closed
	line, ok := <-runner.LinesCh()
	if ok {
		return uiwrangler.CmdOutputMsg{Line: line}
	}
	// Lines channel closed — all output consumed. Now read the result.
	result, ok := <-runner.DoneCh()
	if !ok {
		return uiwrangler.CmdDoneMsg{Result: wcfg.RunResult{}}
	}
	return uiwrangler.CmdDoneMsg{Result: result}
}

// waitForWranglerOutput returns a command that waits for the next output from the runner.
func waitForWranglerOutput(runner *wcfg.Runner) tea.Cmd {
	if runner == nil {
		return nil
	}
	return func() tea.Msg {
		return readWranglerOutputMsg(runner)
	}
}

// startWranglerCmdWithArgs creates a Runner and starts a wrangler command with extra arguments.
// Used for version deploy commands that need version specs and -y flag.
func (m *Model) startWranglerCmdWithArgs(action, envName string, extraArgs []string) tea.Cmd {
	if m.wranglerRunner != nil && m.wranglerRunner.IsRunning() {
		return nil
	}

	// Stop any active wrangler tail since the CmdPane is being taken over
	if m.tailSource == "wrangler" && m.tailSession != nil {
		m.stopTail()
	}

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		ExtraArgs:  extraArgs,
		AccountID:  m.registry.ActiveAccountID(),
	}

	runner := wcfg.NewRunner()
	m.wranglerRunner = runner
	m.wranglerRunnerAction = action
	m.wrangler.StartCommand(action, envName)

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.CmdDoneMsg{Result: wcfg.RunResult{ExitCode: 1, Err: err}}
			}
			return readWranglerOutputMsg(runner)
		},
		m.wrangler.SpinnerInit(),
	)
}

// fetchWranglerVersions runs `wrangler versions list --json` in the background
// and delivers the parsed results via VersionsFetchedMsg.
func (m *Model) fetchWranglerVersions(envName string) tea.Cmd {
	// Cancel any in-flight version fetch
	if m.wranglerVersionRunner != nil {
		m.wranglerVersionRunner.Stop()
	}

	cmd := wcfg.Command{
		Action:     "versions list",
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		ExtraArgs:  []string{"--json"},
		AccountID:  m.registry.ActiveAccountID(),
	}

	runner := wcfg.NewRunner()
	m.wranglerVersionRunner = runner

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.VersionsFetchedMsg{Err: err}
			}

			// Collect all stdout lines (the JSON output)
			var jsonBuf strings.Builder
			for line := range runner.LinesCh() {
				if !line.IsStderr {
					jsonBuf.WriteString(line.Text)
					jsonBuf.WriteByte('\n')
				}
			}

			// Wait for the command to finish
			result := <-runner.DoneCh()
			if result.Err != nil && result.ExitCode != 0 {
				return uiwrangler.VersionsFetchedMsg{
					Err: fmt.Errorf("wrangler versions list failed (exit %d)", result.ExitCode),
				}
			}

			versions, err := wcfg.ParseVersionsJSON([]byte(jsonBuf.String()))
			if err != nil {
				return uiwrangler.VersionsFetchedMsg{Err: err}
			}

			return uiwrangler.VersionsFetchedMsg{Versions: versions}
		},
		m.wrangler.SpinnerInit(),
	)
}

// openVersionPicker opens the version picker overlay, using cached versions if available
// or triggering a background fetch.
func (m *Model) openVersionPicker(mode uiwrangler.PickerMode, envName string) tea.Cmd {
	haveCached := m.wrangler.ShowVersionPicker(mode, envName)
	if haveCached {
		// Versions were served from cache — no fetch needed
		return nil
	}
	// Need to fetch versions in the background
	return m.fetchWranglerVersions(envName)
}

// buildMonorepoActionsPopup creates a minimal action popup for the monorepo project list.
// Only shows the "Load Wrangler Configuration..." action since per-project actions
// require drilling into a specific project first.
func (m Model) buildMonorepoActionsPopup() actions.Model {
	title := fmt.Sprintf("Monorepo — %s", m.wrangler.RootName())
	var items []actions.Item

	// Monitoring section: single entry that opens an environment sub-popup
	envNames := m.wrangler.AllEnvNames()
	if len(envNames) > 0 && m.client != nil {
		if m.parallelTailActive && m.wrangler.IsParallelTailActive() {
			items = append(items, actions.Item{
				Label:       "Stop Parallel Tail",
				Description: "Stop all live log streams",
				Section:     "Monitoring",
				Action:      "parallel_tail_stop",
			})
		} else {
			items = append(items, actions.Item{
				Label:       "Parallel Tail...",
				Description: "Stream all live logs from an environment",
				Section:     "Monitoring",
				Action:      "parallel_tail",
			})
		}
	}

	// Create project action
	items = append(items, actions.Item{
		Label:       "Create Project",
		Description: "Scaffold a new Worker project in this monorepo",
		Section:     "Configuration",
		Action:      "create_project",
	})

	// Add/Delete environment actions (if the selected project has a config)
	if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
		items = append(items, actions.Item{
			Label:       "Add Environment",
			Description: "Add a new environment to the selected project",
			Section:     "Configuration",
			Action:      "add_environment",
		})
		// Only show delete if there are named environments (not just "default")
		if len(cfg.EnvNames()) > 1 {
			items = append(items, actions.Item{
				Label:       "Delete Environment",
				Description: "Remove an environment from the selected project",
				Section:     "Configuration",
				Action:      "delete_environment",
			})
		}
	}

	items = append(items, actions.Item{
		Label:       "Load Wrangler Configuration...",
		Description: "Browse for a wrangler project directory",
		Section:     "Configuration",
		Action:      "wrangler_load_config",
	})
	return actions.New(title, items)
}

// buildParallelTailEnvPopup creates a sub-popup listing environments for parallel tailing.
func (m Model) buildParallelTailEnvPopup() actions.Model {
	title := "Parallel Tail — Select Environment"
	var items []actions.Item
	for _, envName := range m.wrangler.AllEnvNames() {
		// Count how many workers actually define this environment
		count := 0
		for _, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			if !pc.Config.HasEnv(envName) {
				continue
			}
			if scriptName := pc.Config.ResolvedEnvName(envName); scriptName != "" {
				count++
			}
		}
		items = append(items, actions.Item{
			Label:       envName,
			Description: fmt.Sprintf("%d workers", count),
			Section:     "Environments",
			Action:      "parallel_tail_env_" + envName,
		})
	}
	return actions.New(title, items)
}

// buildWranglerActionsPopup creates the action popup for the wrangler view.
// Always includes "Load Wrangler Configuration..." and conditionally includes
// command/binding items when a config is loaded.
func (m Model) buildWranglerActionsPopup() actions.Model {
	envName := m.wrangler.FocusedEnvName()
	title := "Wrangler"
	if m.wrangler.HasConfig() && envName != "" {
		title = fmt.Sprintf("Wrangler — %s", envName)
	}

	var items []actions.Item

	// Wrangler commands section (only when config is loaded)
	if m.wrangler.HasConfig() {
		// Navigate to the worker in the dashboard
		workerName := m.wrangler.Config().ResolvedEnvName(envName)
		if workerName != "" {
			items = append(items, actions.Item{
				Label:       fmt.Sprintf("View Worker: %s", workerName),
				Description: "Open worker in the dashboard",
				Section:     "Navigation",
				NavService:  "Workers",
				NavResource: workerName,
			})
		}

		cmdRunning := m.wrangler.CmdRunning()
		wranglerActions := []string{"deploy", "versions list", "deployments status"}
		for _, action := range wranglerActions {
			items = append(items, actions.Item{
				Label:       wcfg.CommandLabel(action),
				Description: wcfg.CommandDescription(action),
				Section:     "Commands",
				Action:      "wrangler_" + action,
				Disabled:    cmdRunning,
			})
		}

		// Dev server: show "Stop Dev Server" when running, otherwise show the two dev modes
		runningAction := m.wrangler.RunningAction()
		if wcfg.IsDevAction(runningAction) {
			items = append(items, actions.Item{
				Label:       "Stop Dev Server",
				Description: "Stop the running dev server",
				Section:     "Commands",
				Action:      "wrangler_stop_dev",
			})
		} else {
			devActions := []string{"dev", "dev --remote"}
			for _, action := range devActions {
				items = append(items, actions.Item{
					Label:       wcfg.CommandLabel(action),
					Description: wcfg.CommandDescription(action),
					Section:     "Commands",
					Action:      "wrangler_" + action,
					Disabled:    cmdRunning,
				})
			}
		}

		// Delete worker action
		items = append(items, actions.Item{
			Label:       "Delete",
			Description: "Delete the deployed worker for this environment",
			Section:     "Commands",
			Action:      "wrangler_delete",
			Disabled:    cmdRunning,
		})

		// Version deployment actions
		items = append(items, actions.Item{
			Label:       "Deploy Version...",
			Description: "Select a version to deploy at 100%",
			Section:     "Versions",
			Action:      "wrangler_deploy_version",
			Disabled:    cmdRunning,
		})
		items = append(items, actions.Item{
			Label:       "Gradual Deployment...",
			Description: "Split traffic between two versions",
			Section:     "Versions",
			Action:      "wrangler_gradual_deploy",
			Disabled:    cmdRunning,
		})

		// Monitoring section
		if workerName != "" {
			tailLabel := "Tail Logs"
			tailDesc := fmt.Sprintf("Stream live logs from %s", workerName)
			if m.tailSession != nil && m.wrangler.TailActive() {
				tailLabel = "Stop Tail Logs"
				tailDesc = "Stop the live log stream"
			}
			items = append(items, actions.Item{
				Label:       tailLabel,
				Description: tailDesc,
				Section:     "Monitoring",
				Action:      "wrangler_tail_toggle",
				Disabled:    cmdRunning && !m.wrangler.TailActive(),
			})
		}

		// Bindings section (from the focused env box, if inside)
		if m.wrangler.InsideBox() {
			envName := m.wrangler.FocusedEnvName()
			bindings := m.wrangler.Config().EnvBindings(envName)
			if len(bindings) > 0 {
				for _, b := range bindings {
					items = append(items, actions.Item{
						Label:       b.Name,
						Description: b.TypeLabel(),
						Section:     "Bindings",
						NavService:  b.NavService(),
						NavResource: b.ResourceID,
						Disabled:    b.NavService() == "",
					})
				}
			}
		}
	}

	// Add/Delete environment actions (only when config is loaded)
	if m.wrangler.HasConfig() {
		items = append(items, actions.Item{
			Label:       "Add Environment",
			Description: "Add a new environment to the config",
			Section:     "Configuration",
			Action:      "add_environment",
		})
		// Only show delete if there are named environments (not just "default")
		if cfg := m.wrangler.Config(); cfg != nil && len(cfg.EnvNames()) > 1 {
			items = append(items, actions.Item{
				Label:       "Delete Environment",
				Description: "Remove an environment from the config",
				Section:     "Configuration",
				Action:      "delete_environment",
			})
		}
	}

	// Always include the load/switch configuration action
	items = append(items, actions.Item{
		Label:       "Load Wrangler Configuration...",
		Description: "Browse for a wrangler project directory",
		Section:     "Configuration",
		Action:      "wrangler_load_config",
	})

	return actions.New(title, items)
}

// --- Action popup helpers ---

// updateActions forwards messages to the action popup when it's active.
func (m Model) updateActions(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.actionsPopup, cmd = m.actionsPopup.Update(msg)
	return m, cmd
}

// buildActionsPopup creates the action popup for the current detail view.
func (m Model) buildActionsPopup() actions.Model {
	serviceName := m.detail.Service()
	resourceName := m.detail.CurrentDetailName()
	title := fmt.Sprintf("%s — Actions", resourceName)

	var items []actions.Item

	switch serviceName {
	case "Workers":
		items = m.buildWorkerActions()
	case "KV", "R2", "D1":
		items = m.buildBoundWorkersActions()
	}

	return actions.New(title, items)
}

// buildWorkerActions builds the action items for a Worker detail view.
func (m Model) buildWorkerActions() []actions.Item {
	var items []actions.Item

	// Actions section
	tailLabel := "Start tail logs"
	if m.detail.TailActive() {
		tailLabel = "Stop tail logs"
	}
	items = append(items, actions.Item{
		Label:   tailLabel,
		Section: "Actions",
		Action:  "tail_toggle",
	})

	// Bindings section
	rd := m.detail.ResourceDetail()
	if rd != nil && len(rd.Bindings) > 0 {
		for _, b := range rd.Bindings {
			items = append(items, actions.Item{
				Label:       b.Name,
				Description: b.TypeDisplay,
				Section:     "Bindings",
				NavService:  b.NavService,
				NavResource: b.NavResource,
				Disabled:    b.NavService == "",
			})
		}
	}

	return items
}

// buildBoundWorkersActions builds the action items for KV/R2/D1 detail views,
// showing a "Workers" section with navigable links to Workers that bind to this resource.
func (m Model) buildBoundWorkersActions() []actions.Item {
	var items []actions.Item

	rd := m.detail.ResourceDetail()
	if rd == nil {
		return items
	}

	// The Bindings field was populated by enrichDetailWithBoundWorkers
	// with reverse references of type "worker_ref"
	for _, b := range rd.Bindings {
		if b.NavService == "Workers" {
			items = append(items, actions.Item{
				Label:       b.Name,
				Description: b.TypeDisplay,
				Section:     "Workers",
				NavService:  b.NavService,
				NavResource: b.NavResource,
			})
		}
	}

	return items
}

// handleActionSelect dispatches the selected action from the popup.
func (m *Model) handleActionSelect(item actions.Item) tea.Cmd {
	// Navigation to a bound resource
	if item.NavService != "" && item.NavResource != "" {
		return m.navigateTo(item.NavService, item.NavResource)
	}

	// Wrangler load config action
	if item.Action == "wrangler_load_config" {
		m.wrangler.ActivateDirBrowser()
		return nil
	}

	// Add environment action
	// Create project action
	if item.Action == "create_project" {
		m.showProjectPopup = true
		m.projectPopup = projectpopup.New(m.wrangler.ProjectDirNames(), m.wrangler.RootDir())
		return nil
	}

	if item.Action == "add_environment" {
		var configPath string
		var workerName string
		var existingEnvs []string

		if m.wrangler.IsOnProjectList() {
			// Monorepo project list: use the selected project
			configPath = m.wrangler.SelectedProjectConfigPath()
			if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
				workerName = cfg.Name
				existingEnvs = cfg.EnvNames()
			}
		} else if m.wrangler.HasConfig() {
			// Drilled into a project or single-project mode
			configPath = m.wrangler.ConfigPath()
			cfg := m.wrangler.Config()
			workerName = cfg.Name
			existingEnvs = cfg.EnvNames()
		}

		if configPath == "" {
			return nil
		}

		m.showEnvPopup = true
		m.envPopup = envpopup.New(configPath, workerName, existingEnvs)
		return nil
	}

	// Delete environment action
	if item.Action == "delete_environment" {
		var configPath string
		var workerName string
		var namedEnvs []string

		if m.wrangler.IsOnProjectList() {
			configPath = m.wrangler.SelectedProjectConfigPath()
			if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
				workerName = cfg.Name
				for _, e := range cfg.EnvNames() {
					if e != "default" {
						namedEnvs = append(namedEnvs, e)
					}
				}
			}
		} else if m.wrangler.HasConfig() {
			configPath = m.wrangler.ConfigPath()
			cfg := m.wrangler.Config()
			workerName = cfg.Name
			for _, e := range cfg.EnvNames() {
				if e != "default" {
					namedEnvs = append(namedEnvs, e)
				}
			}
		}

		if configPath == "" || len(namedEnvs) == 0 {
			return nil
		}

		m.showEnvPopup = true
		m.envPopup = envpopup.NewDelete(configPath, workerName, namedEnvs)
		return nil
	}

	// Parallel tail: open environment sub-popup
	if item.Action == "parallel_tail" {
		m.showActions = true
		m.actionsPopup = m.buildParallelTailEnvPopup()
		return nil
	}

	// Parallel tail: stop all sessions
	if item.Action == "parallel_tail_stop" {
		m.stopAllParallelTails()
		return nil
	}

	// Parallel tail: start tailing for selected environment
	if strings.HasPrefix(item.Action, "parallel_tail_env_") {
		envName := strings.TrimPrefix(item.Action, "parallel_tail_env_")
		var targets []uiwrangler.ParallelTailTarget
		caches := m.registry.GetAllDeploymentCaches()
		for _, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			// Only include workers that actually define this environment
			if !pc.Config.HasEnv(envName) {
				continue
			}
			scriptName := pc.Config.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			url := ""
			if entry, ok := caches[scriptName]; ok && entry.Subdomain != "" {
				url = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, entry.Subdomain)
			}
			targets = append(targets, uiwrangler.ParallelTailTarget{
				ScriptName: scriptName,
				URL:        url,
			})
		}
		if len(targets) == 0 {
			return nil
		}
		return func() tea.Msg {
			return uiwrangler.ParallelTailStartMsg{EnvName: envName, Scripts: targets}
		}
	}

	// Wrangler version picker actions (must be checked before generic wrangler_ prefix)
	if item.Action == "wrangler_deploy_version" {
		envName := m.wrangler.FocusedEnvName()
		return m.openVersionPicker(uiwrangler.PickerModeDeploy, envName)
	}
	if item.Action == "wrangler_gradual_deploy" {
		envName := m.wrangler.FocusedEnvName()
		return m.openVersionPicker(uiwrangler.PickerModeGradual, envName)
	}

	// Wrangler tail toggle (must be checked before generic wrangler_ prefix)
	if item.Action == "wrangler_tail_toggle" {
		if m.tailSession != nil {
			// Stop any active tail (wrangler or detail)
			m.stopTail()
			m.detail.ClearTail()
			return nil
		}
		// Start tailing the focused env's worker
		envName := m.wrangler.FocusedEnvName()
		cfg := m.wrangler.Config()
		if cfg == nil || m.client == nil {
			return nil
		}
		workerName := cfg.ResolvedEnvName(envName)
		if workerName == "" {
			return nil
		}
		// Stop any running wrangler command first
		m.stopWranglerRunner()
		// Stop any active detail tail
		m.detail.ClearTail()
		// Start tail in wrangler view
		m.tailSource = "wrangler"
		m.wrangler.StartTail(workerName)
		accountID := m.registry.ActiveAccountID()
		return tea.Batch(m.startTailCmd(accountID, workerName), m.wrangler.SpinnerInit())
	}

	// Wrangler dev server actions
	if item.Action == "wrangler_dev" || item.Action == "wrangler_dev --remote" {
		action := strings.TrimPrefix(item.Action, "wrangler_")
		envName := m.wrangler.FocusedEnvName()
		return m.startWranglerCmdWithArgs(action, envName, []string{"--show-interactive-dev-session=false"})
	}
	if item.Action == "wrangler_stop_dev" {
		m.wrangler.StopDevServer()
		m.stopWranglerRunner()
		return nil
	}

	if item.Action == "wrangler_delete" {
		envName := m.wrangler.FocusedEnvName()
		return m.startWranglerCmdWithArgs("delete", envName, []string{"--force"})
	}

	// Wrangler command actions
	if strings.HasPrefix(item.Action, "wrangler_") {
		action := strings.TrimPrefix(item.Action, "wrangler_")
		envName := m.wrangler.FocusedEnvName()
		return func() tea.Msg {
			return uiwrangler.ActionMsg{Action: action, EnvName: envName}
		}
	}

	// Named actions
	switch item.Action {
	case "tail_toggle":
		if m.detail.TailActive() || m.tailSession != nil {
			// Stop tailing
			m.stopTail()
			m.detail.ClearTail()
			return nil
		}
		// Start tailing
		if m.detail.IsWorkersDetail() && m.client != nil {
			// Stop any wrangler tail first
			m.wrangler.StopTailPane()
			m.tailSource = "detail"
			scriptName := m.detail.CurrentDetailName()
			accountID := m.registry.ActiveAccountID()
			m.detail.SetTailStarting()
			return m.startTailCmd(accountID, scriptName)
		}
	}

	return nil
}

// --- Binding popup helpers ---

// updateBindings forwards messages to the bindings popup when it's active.
func (m Model) updateBindings(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.bindingsPopup, cmd = m.bindingsPopup.Update(msg)
	return m, cmd
}

// listResourcesCmd fetches existing resources for the binding popup picker.
func (m Model) listResourcesCmd(resourceType string) tea.Cmd {
	return func() tea.Msg {
		var items []bindings.ResourceItem

		s := m.registry.Get(resourceTypeToServiceName(resourceType))
		if s == nil {
			return bindings.ResourcesLoadedMsg{
				ResourceType: resourceType,
				Err:          fmt.Errorf("service not available"),
			}
		}

		resources, err := s.List()
		if err != nil {
			return bindings.ResourcesLoadedMsg{
				ResourceType: resourceType,
				Err:          err,
			}
		}

		for _, r := range resources {
			items = append(items, bindings.ResourceItem{
				ID:   r.ID,
				Name: r.Name,
			})
		}

		return bindings.ResourcesLoadedMsg{
			ResourceType: resourceType,
			Items:        items,
		}
	}
}

// createResourceCmd runs a wrangler CLI command to create a new resource.
func (m Model) createResourceCmd(resourceType, name string) tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	return func() tea.Msg {
		result := wcfg.CreateResource(context.Background(), wcfg.CreateResourceCmd{
			ResourceType: resourceType,
			Name:         name,
			AccountID:    accountID,
		})
		return bindings.CreateResourceDoneMsg{
			ResourceType: resourceType,
			Name:         name,
			Success:      result.Success,
			Output:       result.Output,
			ResourceID:   result.ResourceID,
		}
	}
}

// writeBindingCmd writes a binding definition into the wrangler config file.
func (m Model) writeBindingCmd(configPath, envName string, binding wcfg.BindingDef) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.AddBinding(configPath, envName, binding)
		if err != nil {
			return bindings.WriteBindingDoneMsg{Success: false, Err: err}
		}
		return bindings.WriteBindingDoneMsg{Success: true}
	}
}

// --- Add environment popup helpers ---

// updateEnvPopup forwards messages to the env popup when it's active.
func (m Model) updateEnvPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.envPopup, cmd = m.envPopup.Update(msg)
	return m, cmd
}

// createEnvCmd writes a new empty environment section into the wrangler config file.
func (m Model) createEnvCmd(configPath, envName string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.AddEnvironment(configPath, envName)
		return envpopup.CreateEnvDoneMsg{
			EnvName: envName,
			Err:     err,
		}
	}
}

// deleteEnvCmd removes an environment section from the wrangler config file.
func (m Model) deleteEnvCmd(configPath, envName string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.DeleteEnvironment(configPath, envName)
		return envpopup.DeleteEnvDoneMsg{
			EnvName: envName,
			Err:     err,
		}
	}
}

// --- Create project popup helpers ---

// updateProjectPopup forwards messages to the project popup when it's active.
func (m Model) updateProjectPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.projectPopup, cmd = m.projectPopup.Update(msg)
	return m, cmd
}

// createProjectCmd runs C3 to scaffold a new Worker project.
func (m Model) createProjectCmd(name, lang, dir string) tea.Cmd {
	return func() tea.Msg {
		result := wcfg.CreateProject(context.Background(), wcfg.CreateProjectCmd{
			Name: name,
			Lang: lang,
			Dir:  dir,
		})
		return projectpopup.CreateProjectDoneMsg{
			Name:    name,
			Success: result.Success,
			Output:  result.Output,
		}
	}
}

// resourceTypeToServiceName maps a binding resource type to its service name in the registry.
func resourceTypeToServiceName(resourceType string) string {
	switch resourceType {
	case "d1":
		return "D1"
	case "kv":
		return "KV"
	case "r2":
		return "R2"
	case "queue":
		return "Queues"
	default:
		return resourceType
	}
}

// --- On-demand refresh helpers ---

// isMutatingAction returns true if the wrangler action modifies live deployment state.
func isMutatingAction(action string) bool {
	switch action {
	case "deploy", "versions deploy", "delete":
		return true
	}
	return false
}

// refreshAfterMutation returns commands to refresh deployment data and the Workers
// service list after a mutating wrangler action (deploy, versions deploy) completes.
// Uses forced variants that ignore cache staleness since we know data has changed.
func (m Model) refreshAfterMutation() tea.Cmd {
	var cmds []tea.Cmd
	// Refresh deployment data (env boxes / project cards)
	if m.wrangler.IsMonorepo() {
		if cmd := m.fetchAllProjectDeploymentsForced(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if cfg := m.wrangler.Config(); cfg != nil {
		if cmd := m.fetchSingleProjectDeploymentsForced(cfg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// Refresh the Workers service list (new worker may have appeared)
	cmds = append(cmds, m.backgroundRefresh("Workers"))
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// refreshDeploymentsIfStale returns commands to refresh deployment data only when
// the deployment cache has stale entries. Used when navigating to the home screen.
func (m Model) refreshDeploymentsIfStale() tea.Cmd {
	if m.client == nil || !m.registry.AnyDeploymentCacheStale() {
		return nil
	}
	if m.wrangler.IsMonorepo() {
		return m.fetchAllProjectDeployments()
	}
	if cfg := m.wrangler.Config(); cfg != nil {
		return m.fetchSingleProjectDeployments(cfg)
	}
	return nil
}

// View renders the full application.
func (m Model) View() string {
	switch m.phase {
	case PhaseSetup:
		return m.setup.View()
	case PhaseDashboard:
		return m.viewDashboard()
	}
	return ""
}

func (m Model) viewDashboard() string {
	// If an overlay is active, render the dashboard dimmed with the popup centered on top
	if m.wrangler.IsVersionPickerActive() {
		bg := dimContent(m.renderDashboardContent())
		fg := m.wrangler.VersionPickerView(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showLauncher {
		bg := dimContent(m.renderDashboardContent())
		fg := m.launcher.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showSearch {
		bg := dimContent(m.renderDashboardContent())
		fg := m.search.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showBindings {
		bg := dimContent(m.renderDashboardContent())
		fg := m.bindingsPopup.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showEnvPopup {
		bg := dimContent(m.renderDashboardContent())
		fg := m.envPopup.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showProjectPopup {
		bg := dimContent(m.renderDashboardContent())
		fg := m.projectPopup.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showActions {
		bg := dimContent(m.renderDashboardContent())
		fg := m.actionsPopup.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}

	return m.renderDashboardContent()
}

// renderDashboardContent renders the normal dashboard (header, content, help, status).
func (m Model) renderDashboardContent() string {
	headerView := m.header.View()

	var content string
	switch m.viewState {
	case ViewWrangler:
		content = m.wrangler.View()
	case ViewServiceList, ViewServiceDetail:
		content = m.detail.View()
	}

	helpText := m.renderHelp()

	parts := []string{headerView, content, helpText}
	if m.toastMsg != "" && time.Now().Before(m.toastExpiry) {
		parts = append(parts, theme.SuccessStyle.Render(fmt.Sprintf(" ✓ %s ", m.toastMsg)))
	} else if m.err != nil {
		parts = append(parts, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s ", m.err.Error())))
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// dimContent applies ANSI dim (faint) styling to every line of the rendered string.
// It wraps each line with the SGR dim code (\033[2m) and a full reset at the end.
// This causes the terminal to render all text at reduced brightness.
func dimContent(s string) string {
	const dimOn = "\033[2m"
	const reset = "\033[0m"
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// Wrap the entire line: dim-on at the start, reset at the end.
		// Any inner resets in the line will cancel dimming mid-line, so we
		// also inject dim-on after every reset sequence we find.
		lines[i] = dimOn + strings.ReplaceAll(line, reset, reset+dimOn) + reset
	}
	return strings.Join(lines, "\n")
}

// overlayCenter composites a foreground popup centered on top of a dimmed background.
// Lines outside the popup's Y range show the dimmed background as-is.
// Lines inside the popup's Y range splice the popup content into the dimmed
// background using ANSI-aware string truncation, preserving the dimmed background
// on the left and right sides of the popup.
func overlayCenter(bg, fg string, termWidth, termHeight int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	// Pad or truncate background to exactly termHeight lines
	for len(bgLines) < termHeight {
		bgLines = append(bgLines, "")
	}
	bgLines = bgLines[:termHeight]

	fgHeight := len(fgLines)
	fgWidth := 0
	for _, line := range fgLines {
		if w := lipgloss.Width(line); w > fgWidth {
			fgWidth = w
		}
	}

	startY := (termHeight - fgHeight) / 2
	startX := (termWidth - fgWidth) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	result := make([]string, termHeight)
	for i := 0; i < termHeight; i++ {
		if i >= startY && i < startY+fgHeight {
			fgIdx := i - startY
			// Splice: dimmed-bg-left + popup-line + dimmed-bg-right
			bgLeft := ansi.Truncate(bgLines[i], startX, "")
			bgRight := ansi.TruncateLeft(bgLines[i], startX+fgWidth, "")
			result[i] = bgLeft + fgLines[fgIdx] + bgRight
		} else {
			result[i] = bgLines[i]
		}
	}

	return strings.Join(result, "\n")
}

func (m Model) renderHelp() string {
	type helpEntry struct {
		key  string
		desc string
	}

	var entries []helpEntry

	switch m.viewState {
	case ViewWrangler:
		if m.wrangler.IsParallelTailActive() {
			entries = []helpEntry{
				{"esc", "back"},
				{"j/k", "scroll"},
			}
		} else {
			entries = []helpEntry{
				{"ctrl+l", "services"},
				{"ctrl+k", "search"},
				{"ctrl+p", "actions"},
				{"ctrl+n", "bindings"},
				{"[/]", "accounts"},
			}
			if m.wrangler.HasConfig() && !m.wrangler.IsOnProjectList() {
				entries = append(entries, helpEntry{"t", "tail"})
				entries = append(entries, helpEntry{"d", "del env"})
			}
			entries = append(entries, helpEntry{"q", "quit"})
		}
	case ViewServiceList:
		entries = []helpEntry{
			{"esc", "back"},
			{"ctrl+h", "home"},
			{"ctrl+l", "services"},
			{"ctrl+k", "search"},
			{"enter", "detail"},
			{"[/]", "accounts"},
		}
	case ViewServiceDetail:
		entries = []helpEntry{
			{"esc", "back"},
			{"ctrl+h", "home"},
			{"ctrl+p", "actions"},
			{"ctrl+k", "search"},
		}
		if m.detail.IsWorkersDetail() {
			entries = append(entries, helpEntry{"t", "tail"})
		}
		entries = append(entries, helpEntry{"[/]", "accounts"})
	}

	var parts []string
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s %s",
			lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render(e.key),
			theme.DimStyle.Render(e.desc)))
	}

	help := ""
	for i, p := range parts {
		if i > 0 {
			help += theme.DimStyle.Render("  |  ")
		}
		help += p
	}

	// Right-align the version string
	ver := theme.DimStyle.Render(version.GetVersion())
	helpWidth := ansi.StringWidth(help)
	verWidth := ansi.StringWidth(ver)
	gap := m.width - helpWidth - verWidth - 4 // 4 for HelpBarStyle padding
	if gap < 2 {
		gap = 2
	}
	help += strings.Repeat(" ", gap) + ver

	return theme.HelpBarStyle.Render(help)
}
