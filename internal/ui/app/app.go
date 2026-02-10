package app

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/header"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/setup"
	"github.com/oarafat/orangeshell/internal/ui/sidebar"
	"github.com/oarafat/orangeshell/internal/ui/theme"
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
	phase      Phase
	focus      Focus
	showSearch bool
	cfg        *config.Config
	client     *api.Client
	registry   *svc.Registry

	// Dimensions
	width  int
	height int

	// Error display
	err error
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
	if phase == PhaseDashboard {
		det = detail.NewLoading(sb.SelectedService())
	} else {
		det = detail.New()
	}

	m := Model{
		setup:    setup.New(cfg),
		header:   header.New(cfg.AuthMethod),
		sidebar:  sb,
		detail:   det,
		search:   search.New(),
		keys:     theme.DefaultKeyMap(),
		phase:    phase,
		cfg:      cfg,
		registry: svc.NewRegistry(),
	}

	return m
}

// Init returns the initial command.
func (m Model) Init() tea.Cmd {
	if m.phase == PhaseDashboard {
		return tea.Batch(m.initDashboardCmd(), m.detail.SpinnerInit())
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

		client, err := api.NewClient(authenticator, m.cfg)
		if err != nil {
			return errMsg{err}
		}

		ctx := context.Background()
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

		// Load resources for the initially selected service
		serviceName := m.sidebar.SelectedService()
		m.detail.SetService(serviceName)
		loadCmd := tea.Cmd(func() tea.Msg {
			return detail.LoadResourcesMsg{ServiceName: serviceName}
		})

		// Start the periodic background refresh ticker and the spinner
		tickCmd := m.scheduleRefreshTick()

		cmds := []tea.Cmd{loadCmd, tickCmd, m.detail.SpinnerInit()}
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
		// Cache the result regardless of which service is displayed
		if msg.Err == nil && msg.Resources != nil {
			m.registry.SetCache(msg.ServiceName, msg.Resources)
		}
		// Staleness check: ignore if the user has already switched services
		if msg.ServiceName != m.detail.Service() {
			// Still update search items even if not the active service
			if msg.Err == nil {
				m.search.SetItems(m.registry.AllSearchItems())
			}
			return m, nil
		}
		m.detail.SetResources(msg.Resources, msg.Err, msg.NotIntegrated)
		// Update search items after loading
		if msg.Err == nil {
			m.search.SetItems(m.registry.AllSearchItems())
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
		m.detail.SetDetail(msg.Detail, msg.Err)
		return m, nil

	case detail.BackgroundRefreshMsg:
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

	// Search messages
	case search.NavigateMsg:
		m.showSearch = false
		// Navigate to the selected service and resource
		return m, m.navigateTo(msg.ServiceName, msg.ResourceID)

	case search.CloseMsg:
		m.showSearch = false
		return m, nil

	case spinner.TickMsg:
		if m.detail.IsLoading() {
			cmd := m.detail.UpdateSpinner(msg)
			return m, cmd
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

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			return m, tea.Quit
		case "tab":
			m.toggleFocus()
			return m, nil
		case "ctrl+k", "/":
			m.showSearch = true
			m.search.SetItems(m.registry.AllSearchItems())
			m.search.Reset()
			cmds := m.fetchUncachedForSearch()
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
			return m, nil
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
		m.detail, cmd = m.detail.Update(msg)
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
	case FocusDetail:
		m.focus = FocusSidebar
		m.sidebar.SetFocused(true)
		m.detail.SetFocused(false)
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

// switchToService handles switching to a service, using cached data if available.
func (m *Model) switchToService(name string) tea.Cmd {
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

// backgroundRefresh creates a command that fetches resources for a service in the background.
// Returns a BackgroundRefreshMsg instead of ResourcesLoadedMsg to avoid interfering with
// the normal load flow.
func (m Model) backgroundRefresh(serviceName string) tea.Cmd {
	return func() tea.Msg {
		s := m.registry.Get(serviceName)
		if s == nil {
			return detail.BackgroundRefreshMsg{
				ServiceName: serviceName,
				Resources:   nil,
				Err:         nil,
			}
		}

		resources, err := s.List()
		return detail.BackgroundRefreshMsg{
			ServiceName: serviceName,
			Resources:   resources,
			Err:         err,
		}
	}
}

// loadServiceResources creates a command that fetches resources from a registered service.
func (m Model) loadServiceResources(serviceName string) tea.Cmd {
	return func() tea.Msg {
		s := m.registry.Get(serviceName)
		if s == nil {
			return detail.ResourcesLoadedMsg{
				ServiceName:   serviceName,
				Resources:     nil,
				Err:           nil,
				NotIntegrated: true,
			}
		}

		resources, err := s.List()
		return detail.ResourcesLoadedMsg{
			ServiceName: serviceName,
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
}

// switchAccount handles switching to a different account. Re-registers services with the
// new accountID, loads the currently selected sidebar service, and shows cached data
// instantly if we've visited this account before.
func (m *Model) switchAccount(accountID, accountName string) tea.Cmd {
	m.cfg.AccountID = accountID
	m.registerServices(accountID)

	// Reset detail panel for the current sidebar service
	serviceName := m.sidebar.SelectedService()

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

	return tea.Batch(loadCmd, detailCmd)
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
	// If search overlay is active, render it on top
	if m.showSearch {
		return m.search.View(m.width, m.height)
	}

	headerView := m.header.View()
	sidebarView := m.sidebar.View()
	detailView := m.detail.View()

	content := lipgloss.JoinHorizontal(lipgloss.Top, sidebarView, detailView)
	helpText := m.renderHelp()

	errView := ""
	if m.err != nil {
		errView = "\n" + theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s ", m.err.Error()))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		headerView,
		content,
		helpText,
		errView,
	)
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
