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

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/actions"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/header"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/setup"
	"github.com/oarafat/orangeshell/internal/ui/sidebar"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

const refreshInterval = 30 * time.Second

// Focus tracks which pane is active.
type Focus int

const (
	FocusSidebar Focus = iota
	FocusDetail
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

// tickRefreshMsg fires periodically to trigger background refresh of all services.
type tickRefreshMsg struct{}

// toastExpireMsg fires after the toast display duration to clear the toast.
type toastExpireMsg struct{}

// bindingIndexBuiltMsg carries a newly built binding index from the background.
type bindingIndexBuiltMsg struct {
	index     *svc.BindingIndex
	accountID string
}

// Model is the root Bubble Tea model that composes all UI components.
type Model struct {
	// Submodels
	setup   setup.Model
	header  header.Model
	sidebar sidebar.Model
	detail  detail.Model
	search  search.Model
	keys    theme.KeyMap

	// State
	phase        Phase
	focus        Focus
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
	wrangler       uiwrangler.Model
	wranglerShown  bool // true when sidebar has "Wrangler" selected
	wranglerRunner *wcfg.Runner

	// Active tail session (nil when no tail is running)
	tailSession *svc.TailSession

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

	sb := sidebar.New()

	// If going straight to dashboard, pre-set detail to "Loading..." for the
	// initially selected service so the user doesn't see "Select a service"
	// while authentication is in flight.
	var det detail.Model
	wranglerShown := false
	if phase == PhaseDashboard {
		if sb.SelectedService() == "Wrangler" {
			// Wrangler is selected by default — don't load any API service
			det = detail.New()
			wranglerShown = true
		} else {
			det = detail.NewLoading(sb.SelectedService())
		}
	} else {
		det = detail.New()
	}

	m := Model{
		setup:         setup.New(cfg),
		header:        header.New(cfg.AuthMethod),
		sidebar:       sb,
		detail:        det,
		search:        search.New(),
		wrangler:      uiwrangler.New(),
		wranglerShown: wranglerShown,
		keys:          theme.DefaultKeyMap(),
		phase:         phase,
		cfg:           cfg,
		registry:      svc.NewRegistry(),
	}

	return m
}

// Init returns the initial command.
func (m Model) Init() tea.Cmd {
	if m.phase == PhaseDashboard {
		cmds := []tea.Cmd{m.initDashboardCmd(), m.detail.SpinnerInit()}
		if m.wranglerShown {
			cmds = append(cmds, m.wrangler.SpinnerInit())
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

		// Load resources for the initially selected service (skip if Wrangler is selected)
		serviceName := m.sidebar.SelectedService()
		var loadCmd tea.Cmd
		if serviceName == "Wrangler" {
			m.wranglerShown = true
		} else {
			m.detail.SetService(serviceName)
			loadCmd = tea.Cmd(func() tea.Msg {
				return detail.LoadResourcesMsg{ServiceName: serviceName}
			})
		}

		// Start the periodic background refresh ticker and the spinner
		tickCmd := m.scheduleRefreshTick()

		// Scan CWD for wrangler config in the background
		wranglerCmd := m.scanWranglerConfigCmd()

		cmds := []tea.Cmd{tickCmd, m.detail.SpinnerInit(), wranglerCmd}
		if loadCmd != nil {
			cmds = append(cmds, loadCmd)
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
		m.detail.SetTailStarting()
		accountID := m.registry.ActiveAccountID()
		return m, m.startTailCmd(accountID, msg.ScriptName)

	case detail.TailStartedMsg:
		m.tailSession = msg.Session
		m.detail.SetTailStarted()
		return m, m.waitForTailLines()

	case detail.TailLogMsg:
		if m.tailSession == nil {
			return m, nil
		}
		m.detail.AppendTailLines(msg.Lines)
		// Continue polling for more lines
		return m, m.waitForTailLines()

	case detail.TailErrorMsg:
		m.detail.SetTailError(msg.Err)
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

	case tickRefreshMsg:
		if m.phase != PhaseDashboard || m.client == nil {
			return m, nil
		}
		// Refresh all registered services in background + schedule next tick
		cmds := []tea.Cmd{m.scheduleRefreshTick()}
		for _, name := range m.registry.RegisteredNames() {
			cmds = append(cmds, m.backgroundRefresh(name))
		}
		return m, tea.Batch(cmds...)

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
		return m, nil

	case uiwrangler.LoadConfigPathMsg:
		// User entered a custom path — scan it for wrangler config
		m.wrangler.SetConfigLoading()
		return m, tea.Batch(m.scanWranglerConfigFromDir(msg.Path), m.wrangler.SpinnerInit())

	case uiwrangler.NavigateMsg:
		m.wranglerShown = false
		return m, m.navigateTo(msg.ServiceName, msg.ResourceID)

	case uiwrangler.ActionMsg:
		return m, m.startWranglerCmd(msg.Action, msg.EnvName)

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
		m.wranglerRunner = nil
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

	case spinner.TickMsg:
		var cmds []tea.Cmd
		if m.detail.IsLoading() {
			cmds = append(cmds, m.detail.UpdateSpinner(msg))
		}
		if m.wrangler.IsLoading() {
			cmds = append(cmds, m.wrangler.UpdateSpinner(msg))
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
	// If search overlay is active, route everything there
	if m.showSearch {
		return m.updateSearch(msg)
	}

	// If action popup is active, route everything there
	if m.showActions {
		return m.updateActions(msg)
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		// Forward mouse events to the detail panel regardless of focus (for copy-on-click)
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			return m, tea.Quit
		case "tab":
			m.toggleFocus()
			return m, nil
		case "ctrl+k":
			m.showSearch = true
			m.search.SetItems(m.registry.AllSearchItems())
			m.search.Reset()
			cmds := m.fetchUncachedForSearch()
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
		case "ctrl+p":
			if m.wranglerShown && !m.wrangler.IsDirBrowserActive() {
				m.showActions = true
				m.actionsPopup = m.buildWranglerActionsPopup()
				return m, nil
			}
			if m.detail.InDetailView() {
				m.showActions = true
				m.actionsPopup = m.buildActionsPopup()
				return m, nil
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
		}
	}

	var cmd tea.Cmd
	prevService := m.sidebar.SelectedService()

	switch m.focus {
	case FocusSidebar:
		m.sidebar, cmd = m.sidebar.Update(msg)
		if m.sidebar.SelectedService() != prevService {
			newService := m.sidebar.SelectedService()
			loadCmd := m.switchToService(newService)
			if loadCmd != nil {
				return m, loadCmd
			}
		}
	case FocusDetail:
		if m.wranglerShown {
			m.wrangler, cmd = m.wrangler.Update(msg)
		} else {
			m.detail, cmd = m.detail.Update(msg)
		}
	}

	return m, cmd
}

func (m Model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	return m, cmd
}

func (m *Model) toggleFocus() {
	switch m.focus {
	case FocusSidebar:
		m.focus = FocusDetail
		m.sidebar.SetFocused(false)
		m.detail.SetFocused(true)
		m.wrangler.SetFocused(true)
	case FocusDetail:
		m.focus = FocusSidebar
		m.sidebar.SetFocused(true)
		m.detail.SetFocused(false)
		m.wrangler.SetFocused(false)
	}
}

// layout recalculates component sizes based on terminal dimensions.
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

	sidebarWidth := int(float64(m.width) * theme.SidebarRatio)
	if sidebarWidth < theme.SidebarMinWidth {
		sidebarWidth = theme.SidebarMinWidth
	}
	if sidebarWidth > theme.SidebarMaxWidth {
		sidebarWidth = theme.SidebarMaxWidth
	}

	detailWidth := m.width - sidebarWidth
	if detailWidth < 10 {
		detailWidth = 10
	}

	m.header.SetWidth(m.width)
	m.sidebar.SetSize(sidebarWidth, contentHeight)
	m.detail.SetSize(detailWidth, contentHeight)
	m.wrangler.SetSize(detailWidth, contentHeight)
	// Detail content starts after: header(1) + top border(1) = 2 rows from top of terminal
	m.detail.SetYOffset(headerHeight + 1)
}

// fetchUncachedForSearch triggers background fetches for all registered services
// that don't have cached data yet. Sets the search fetching counter so the UI
// can show a loading indicator. Returns the commands to run.
func (m *Model) fetchUncachedForSearch() []tea.Cmd {
	var cmds []tea.Cmd
	for _, name := range m.registry.RegisteredNames() {
		if m.registry.GetCache(name) == nil {
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

// switchToService handles switching to a service, using cached data if available.
func (m *Model) switchToService(name string) tea.Cmd {
	// Stop any active tail/D1 session when switching services
	m.stopTail()
	m.detail.ClearTail()
	m.detail.ClearD1()
	m.stopWranglerRunner()

	// Handle Wrangler specially — it doesn't go through the service interface
	if name == "Wrangler" {
		m.wranglerShown = true
		return nil
	}
	m.wranglerShown = false

	entry := m.registry.GetCache(name)
	if entry != nil {
		cmd := m.detail.SetServiceWithCache(name, entry.Resources)
		if m.detail.IsLoading() {
			return tea.Batch(cmd, m.detail.SpinnerInit())
		}
		return cmd
	}
	cmd := m.detail.SetService(name)
	if cmd != nil {
		return tea.Batch(cmd, m.detail.SpinnerInit())
	}
	return nil
}

// scheduleRefreshTick returns a command that sends a tickRefreshMsg after the refresh interval.
func (m Model) scheduleRefreshTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickRefreshMsg{}
	})
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
}

// switchAccount handles switching to a different account. Re-registers services with the
// new accountID, loads the currently selected sidebar service, and shows cached data
// instantly if we've visited this account before.
func (m *Model) switchAccount(accountID, accountName string) tea.Cmd {
	// Stop any active tail session and wrangler command
	m.stopTail()
	m.detail.ClearTail()
	m.detail.ClearD1()
	m.stopWranglerRunner()

	m.cfg.AccountID = accountID
	m.registerServices(accountID)

	// Force detail panel to accept new data even though the service name may be the same
	serviceName := m.sidebar.SelectedService()
	m.detail.ResetService()

	// If we have cached data for this account+service, show it instantly
	entry := m.registry.GetCache(serviceName)
	if entry != nil {
		m.detail.SetServiceWithCache(serviceName, entry.Resources)
	} else {
		m.detail.SetService(serviceName)
	}

	// Update search items with whatever is cached for this account
	m.search.SetItems(m.registry.AllSearchItems())

	// Load fresh data from the API
	loadCmd := tea.Cmd(func() tea.Msg {
		return detail.LoadResourcesMsg{ServiceName: serviceName}
	})

	return tea.Batch(loadCmd, m.detail.SpinnerInit())
}

// navigateTo switches the sidebar to a service and loads the detail for a specific resource.
func (m *Model) navigateTo(serviceName, resourceID string) tea.Cmd {
	m.wranglerShown = false

	// Find and select the service in the sidebar
	for i, s := range m.sidebar.Services() {
		if s.Name == serviceName {
			m.sidebar.SetCursor(i)
			break
		}
	}

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

	// Focus on the detail pane
	m.focus = FocusDetail
	m.sidebar.SetFocused(false)
	m.detail.SetFocused(true)
	m.wrangler.SetFocused(true)

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

// stopTail closes the active tail session and cleans up.
func (m *Model) stopTail() {
	if m.tailSession == nil {
		return
	}
	// Stop in a background goroutine to avoid blocking the UI
	session := m.tailSession
	client := m.client
	m.tailSession = nil
	m.detail.SetTailStopped()

	go func() {
		ctx := context.Background()
		svc.StopTail(ctx, client.CF, session)
	}()
}

// --- Wrangler config helpers ---

// scanWranglerConfigCmd returns a command that scans CWD for a wrangler config file.
func (m Model) scanWranglerConfigCmd() tea.Cmd {
	return func() tea.Msg {
		dir := "."
		path := wcfg.FindConfigUp(dir)
		if path == "" {
			return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
		}
		cfg, err := wcfg.Parse(path)
		return uiwrangler.ConfigLoadedMsg{Config: cfg, Path: path, Err: err}
	}
}

// scanWranglerConfigFromDir returns a command that scans a specific directory for a wrangler config file.
func (m Model) scanWranglerConfigFromDir(dir string) tea.Cmd {
	return func() tea.Msg {
		path := wcfg.FindConfigUp(dir)
		if path == "" {
			// Also try FindConfig directly in case it's a file path
			path = wcfg.FindConfig(dir)
		}
		if path == "" {
			return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
		}
		cfg, err := wcfg.Parse(path)
		return uiwrangler.ConfigLoadedMsg{Config: cfg, Path: path, Err: err}
	}
}

// startWranglerCmd creates a Runner and starts the wrangler command.
func (m *Model) startWranglerCmd(action, envName string) tea.Cmd {
	if m.wranglerRunner != nil && m.wranglerRunner.IsRunning() {
		// Don't start a new command while one is running
		return nil
	}

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
	}

	runner := wcfg.NewRunner()
	m.wranglerRunner = runner
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

		wranglerActions := []string{"deploy", "rollback", "versions list", "deployments status"}
		for _, action := range wranglerActions {
			disabled := m.wrangler.CmdRunning()
			items = append(items, actions.Item{
				Label:       wcfg.CommandLabel(action),
				Description: wcfg.CommandDescription(action),
				Section:     "Commands",
				Action:      "wrangler_" + action,
				Disabled:    disabled,
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
		// Ensure focus is on the detail pane so the browser receives key events
		m.focus = FocusDetail
		m.sidebar.SetFocused(false)
		m.detail.SetFocused(true)
		m.wrangler.SetFocused(true)
		return nil
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
			scriptName := m.detail.CurrentDetailName()
			accountID := m.registry.ActiveAccountID()
			m.detail.SetTailStarting()
			return m.startTailCmd(accountID, scriptName)
		}
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
	if m.showSearch {
		bg := dimContent(m.renderDashboardContent())
		fg := m.search.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}
	if m.showActions {
		bg := dimContent(m.renderDashboardContent())
		fg := m.actionsPopup.View(m.width, m.height)
		return overlayCenter(bg, fg, m.width, m.height)
	}

	return m.renderDashboardContent()
}

// renderDashboardContent renders the normal dashboard (header, sidebar, detail, help, status).
func (m Model) renderDashboardContent() string {
	headerView := m.header.View()
	sidebarView := m.sidebar.View()

	var rightPane string
	if m.wranglerShown {
		rightPane = m.wrangler.View()
	} else {
		rightPane = m.detail.View()
	}

	content := lipgloss.JoinHorizontal(lipgloss.Top, sidebarView, rightPane)
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
	bindings := m.keys.ShortHelp()
	var parts []string
	for _, b := range bindings {
		parts = append(parts,
			fmt.Sprintf("%s %s",
				lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render(b.Help().Key),
				theme.DimStyle.Render(b.Help().Desc)))
	}

	help := ""
	for i, p := range parts {
		if i > 0 {
			help += theme.DimStyle.Render("  |  ")
		}
		help += p
	}

	return theme.HelpBarStyle.Render(help)
}
