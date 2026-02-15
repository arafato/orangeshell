package detail

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// ResourceItemZoneID returns the bubblezone marker ID for a resource list item.
func ResourceItemZoneID(idx int) string {
	return fmt.Sprintf("res-item-%d", idx)
}

// View mode tracks whether we're looking at a list or a single item.
type viewMode int

const (
	viewList   viewMode = iota // List of resources
	viewDetail                 // Single resource detail
)

// DetailFocus identifies which pane has keyboard focus in the dual-pane layout.
type DetailFocus int

const (
	FocusList   DetailFocus = iota // Left pane (resource list)
	FocusDetail                    // Right pane (resource detail)
)

// DetailMode describes the interactivity level of a service's detail view.
type DetailMode int

const (
	ReadOnly  DetailMode = iota // Detail view supports scrolling only (Workers, KV, R2, Queues)
	ReadWrite                   // Detail view has interactive elements (D1 SQL console, future KV editor)
)

// ServiceEntry describes a service available in the dropdown selector.
type ServiceEntry struct {
	Name       string     // e.g. "Workers", "KV", "R2"
	Integrated bool       // false for "coming soon" services
	Mode       DetailMode // ReadOnly or ReadWrite — determines interactive capabilities
}

// SelectServiceMsg is emitted when the user selects a service from the dropdown.
// The app layer handles this to trigger resource loading.
type SelectServiceMsg struct {
	ServiceName string
}

// Messages sent by the detail panel to the parent (app model).
type (
	// LoadResourcesMsg requests the app to load resources for a service.
	LoadResourcesMsg struct {
		ServiceName string
	}
	// ResourcesLoadedMsg carries the loaded resources back.
	ResourcesLoadedMsg struct {
		ServiceName   string
		AccountID     string // account this response belongs to (for staleness checks)
		Resources     []service.Resource
		Err           error
		NotIntegrated bool // true only when the service has no backend integration
	}
	// LoadDetailMsg requests the app to load detail for a single resource.
	LoadDetailMsg struct {
		ServiceName string
		ResourceID  string
	}
	// DetailLoadedMsg carries the loaded resource detail back.
	DetailLoadedMsg struct {
		ServiceName string
		ResourceID  string
		Detail      *service.ResourceDetail
		Err         error
	}
	// BackgroundRefreshMsg carries updated resources from a background refresh.
	BackgroundRefreshMsg struct {
		ServiceName string
		AccountID   string // account this response belongs to (for staleness checks)
		Resources   []service.Resource
		Err         error
	}

	// Tail-related messages

	// TailStartMsg requests the app to start tailing a Worker's logs.
	TailStartMsg struct {
		ScriptName string
		AccountID  string
	}
	// TailStartedMsg indicates a tail session was created successfully.
	TailStartedMsg struct {
		Session *service.TailSession
	}
	// TailLogMsg carries new log lines from the websocket.
	TailLogMsg struct {
		Lines []service.TailLine
	}
	// TailErrorMsg indicates tail creation/connection failed.
	TailErrorMsg struct {
		Err error
	}
	// TailStoppedMsg indicates the tail was stopped (cleanup complete).
	TailStoppedMsg struct{}

	// D1 SQL console messages

	// D1QueryMsg requests the app to execute a SQL query against a D1 database.
	D1QueryMsg struct {
		DatabaseID string
		SQL        string
	}
	// D1QueryResultMsg carries the result of a SQL query.
	D1QueryResultMsg struct {
		Result *service.D1QueryResult
		Err    error
	}
	// D1SchemaLoadMsg requests the app to load the schema for a D1 database.
	D1SchemaLoadMsg struct {
		DatabaseID string
	}
	// D1SchemaLoadedMsg carries the structured schema data.
	D1SchemaLoadedMsg struct {
		DatabaseID string
		Tables     []service.SchemaTable
		Err        error
	}

	// EnterInteractiveMsg is emitted when the user enters interactive mode on a
	// ReadWrite service's detail view. The app layer handles this to initialize
	// service-specific interactive features (e.g. D1 SQL console).
	EnterInteractiveMsg struct {
		ServiceName string
		ResourceID  string
		Mode        DetailMode
	}

	// CopyToClipboardMsg requests the app to copy text to the system clipboard.
	CopyToClipboardMsg struct {
		Text string
	}

	// DeleteResourceRequestMsg requests the app to open the delete confirmation popup.
	DeleteResourceRequestMsg struct {
		ServiceName  string
		ResourceID   string
		ResourceName string
	}
)

// Model represents the Resources tab — a dual-pane layout with a service dropdown,
// a resource list on the left (~20%), and a resource detail on the right (~80%).
type Model struct {
	service string // currently selected service name
	focused bool
	width   int
	height  int
	mode    viewMode

	// Dual-pane layout
	focus       DetailFocus // which pane has keyboard focus
	interacting bool        // true when in interactive mode (detail pane engaged for scrolling/editing)

	// Service dropdown
	services       []ServiceEntry // available services for the dropdown
	dropdownOpen   bool           // true when the dropdown overlay is visible
	dropdownCursor int            // cursor position in the dropdown

	// Wrangler-managed resource detection
	managedIDs   map[string]bool // set of resource IDs that are wrangler-managed
	managedCount int             // number of managed resources at the front of the slice

	// List state
	resources  []service.Resource
	cursor     int
	loading    bool
	refreshing bool // true when showing cached data while a background refresh is in flight
	err        error

	// Detail state
	detail        *service.ResourceDetail
	detailLoading bool
	detailErr     error
	detailID      string // resource ID being loaded for staleness checks

	// Cross-tab navigation: when navigating to a specific resource via navigateTo(),
	// this stores the target resource ID so the cursor can be positioned correctly
	// once the resource list finishes loading (which may happen asynchronously).
	pendingNavigateID string

	// Service not yet integrated
	notIntegrated bool

	// Scroll offset for detail view
	scrollOffset int

	// D1 SQL console state
	d1Input      textinput.Model // SQL text input
	d1Active     bool            // true when D1 console is initialized
	d1Output     []string        // accumulated output lines (query results)
	d1Querying   bool            // true while a query is in flight
	d1DatabaseID string          // current database UUID

	// D1 Schema pane state
	d1SchemaTables  []service.SchemaTable // structured schema data (nil = not loaded)
	d1SchemaErr     string                // schema load error message
	d1SchemaLoading bool                  // true while schema is being fetched

	// Loading spinner
	spinner spinner.Model

	// Copy-on-click state
	yOffset     int            // absolute Y of content area start (set by app)
	copyTargets map[int]string // relative content Y → text to copy
}

// isCopyableLabel returns true if a detail field label should get a copy icon.
func isCopyableLabel(label string) bool {
	switch label {
	case "Database ID", "Namespace ID", "Name", "Title", "Bucket Name":
		return true
	}
	return false
}

// copyIcon returns the styled copy icon appended to copyable values.
func copyIcon() string {
	return " " + theme.CopyIconStyle.Render("⧉")
}

// SetYOffset sets the absolute Y coordinate of the detail content start.
// Called by the app after layout to enable mouse click → copy target mapping.
func (m *Model) SetYOffset(y int) {
	m.yOffset = y
}

// newSpinner creates a styled spinner using the Dot style.
func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)
	return s
}

// New creates a new detail panel model.
func New() Model {
	return Model{
		spinner:     newSpinner(),
		copyTargets: make(map[int]string),
	}
}

// NewLoading creates a detail panel model pre-set to loading state for a service.
// This avoids showing "Select a service" during initial authentication.
func NewLoading(serviceName string) Model {
	return Model{
		service:     serviceName,
		loading:     true,
		spinner:     newSpinner(),
		copyTargets: make(map[int]string),
	}
}

// SetServices sets the available services for the dropdown selector.
func (m *Model) SetServices(services []ServiceEntry) {
	m.services = services
}

// SetManagedResources sets the set of resource IDs that are wrangler-managed.
// Resources in this set are rendered in white; others in dim/gray.
// Re-sorts the resources slice so managed items appear first, preserving cursor
// on the same resource.
func (m *Model) SetManagedResources(ids map[string]bool) {
	// When no resources are managed (empty map), there's nothing to
	// distinguish — disable color coding so all items appear normal/white.
	if ids != nil && len(ids) == 0 {
		ids = nil
	}
	m.managedIDs = ids
	if ids == nil || len(m.resources) == 0 {
		m.managedCount = 0
		return
	}

	// Remember currently selected resource to restore cursor after sort.
	// Prefer the pending navigate target if set (cross-tab navigation in flight).
	var selectedID string
	if m.pendingNavigateID != "" {
		selectedID = m.pendingNavigateID
	} else if m.cursor >= 0 && m.cursor < len(m.resources) {
		selectedID = m.resources[m.cursor].ID
	}

	// Copy the slice before sorting to avoid mutating shared cache data
	sorted := make([]service.Resource, len(m.resources))
	copy(sorted, m.resources)
	m.resources = sorted

	// Stable sort: managed first, then unmanaged, preserving order within each group
	sort.SliceStable(m.resources, func(i, j int) bool {
		iManaged := ids[m.resources[i].ID]
		jManaged := ids[m.resources[j].ID]
		if iManaged != jManaged {
			return iManaged // managed before unmanaged
		}
		return false // preserve original order within group
	})

	// Count managed items
	m.managedCount = 0
	for _, r := range m.resources {
		if ids[r.ID] {
			m.managedCount++
		} else {
			break
		}
	}

	// Restore cursor position (match by ID, then fall back to Name for bindings
	// that store a resource name rather than a UUID, e.g. Queues)
	if selectedID != "" {
		for i, r := range m.resources {
			if r.ID == selectedID {
				m.cursor = i
				m.pendingNavigateID = "" // clear if it was the nav target
				return
			}
		}
		for i, r := range m.resources {
			if r.Name == selectedID {
				m.cursor = i
				m.pendingNavigateID = "" // clear if it was the nav target
				return
			}
		}
	}
}

// IsManaged returns whether a resource ID is wrangler-managed.
func (m Model) IsManaged(resourceID string) bool {
	if m.managedIDs == nil {
		return false
	}
	return m.managedIDs[resourceID]
}

// DropdownOpen returns whether the service dropdown is currently open.
func (m Model) DropdownOpen() bool {
	return m.dropdownOpen
}

// OpenDropdown opens the service dropdown. If a service is already selected,
// positions the cursor on it.
func (m *Model) OpenDropdown() {
	m.dropdownOpen = true
	m.dropdownCursor = 0
	for i, s := range m.services {
		if s.Name == m.service {
			m.dropdownCursor = i
			break
		}
	}
}

// CloseDropdown closes the service dropdown without changing the selection.
func (m *Model) CloseDropdown() {
	m.dropdownOpen = false
}

// Focus returns which pane currently has keyboard focus.
func (m Model) Focus() DetailFocus {
	return m.focus
}

// Interacting returns whether the detail pane is in interactive mode
// (user pressed enter/tab to engage with the detail view for scrolling or editing).
func (m Model) Interacting() bool {
	return m.interacting
}

// ActiveServiceMode returns the DetailMode for the currently selected service.
func (m Model) ActiveServiceMode() DetailMode {
	return m.activeServiceMode()
}

// activeServiceMode returns the DetailMode for the currently selected service.
func (m Model) activeServiceMode() DetailMode {
	for _, s := range m.services {
		if s.Name == m.service {
			return s.Mode
		}
	}
	return ReadOnly
}

// SetFocusList switches keyboard focus to the left (list) pane.
func (m *Model) SetFocusList() {
	m.focus = FocusList
}

// SetSize updates the detail panel dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetFocused sets whether the detail panel is the focused pane.
func (m *Model) SetFocused(f bool) {
	m.focused = f
}

// Focused returns whether the panel is focused.
func (m Model) Focused() bool {
	return m.focused
}

// Service returns the currently displayed service name (for staleness checks).
func (m Model) Service() string {
	return m.service
}

// ResetService clears the current service name so that a subsequent SetService or
// SetServiceWithCache call for the same service name won't be skipped.
// Used when switching accounts — the service name stays the same but the data changes.
func (m *Model) ResetService() {
	m.service = ""
}

// SetService updates which service to display. Resets state and triggers load.
func (m *Model) SetService(name string) tea.Cmd {
	if name == m.service {
		return nil
	}
	m.service = name
	m.mode = viewList
	m.resources = nil
	m.cursor = 0
	m.loading = true
	m.refreshing = false
	m.err = nil
	m.detail = nil
	m.detailErr = nil
	m.detailID = ""
	m.scrollOffset = 0
	m.notIntegrated = false
	m.managedIDs = nil
	m.managedCount = 0
	m.interacting = false

	return func() tea.Msg {
		return LoadResourcesMsg{ServiceName: name}
	}
}

// SetServiceWithCache updates which service to display, showing cached data immediately
// while a background refresh is triggered. If no cache is available, falls back to loading state.
// Returns (refreshCmd, previewCmd) — caller should batch both.
func (m *Model) SetServiceWithCache(name string, cached []service.Resource) (tea.Cmd, tea.Cmd) {
	if name == m.service {
		return nil, nil
	}
	m.service = name
	m.mode = viewList
	m.detail = nil
	m.detailErr = nil
	m.detailID = ""
	m.scrollOffset = 0
	m.notIntegrated = false
	m.err = nil
	m.managedIDs = nil
	m.managedCount = 0
	m.interacting = false

	refreshCmd := tea.Cmd(func() tea.Msg {
		return LoadResourcesMsg{ServiceName: name}
	})

	if cached != nil {
		// Show cached data immediately, mark as refreshing
		m.resources = cached
		m.cursor = 0
		m.loading = false
		m.refreshing = true

		// Auto-preview first resource
		var previewCmd tea.Cmd
		if len(cached) > 0 {
			m.mode = viewDetail
			r := cached[0]
			m.detailLoading = true
			m.detailID = r.ID
			previewCmd = func() tea.Msg {
				return LoadDetailMsg{ServiceName: name, ResourceID: r.ID}
			}
		}
		return refreshCmd, previewCmd
	}

	// No cache — show loading spinner
	m.resources = nil
	m.cursor = 0
	m.loading = true
	m.refreshing = false
	return refreshCmd, nil
}

// SetServiceFresh updates which service to display using cached data that is
// known to be fresh (within CacheTTL). No background refresh is triggered.
// Returns a Cmd to auto-preview the first resource (if any).
func (m *Model) SetServiceFresh(name string, cached []service.Resource) tea.Cmd {
	if name == m.service {
		return nil
	}
	m.service = name
	m.mode = viewList
	m.detail = nil
	m.detailErr = nil
	m.detailID = ""
	m.scrollOffset = 0
	m.notIntegrated = false
	m.err = nil
	m.managedIDs = nil
	m.managedCount = 0
	m.resources = cached
	m.cursor = 0
	m.loading = false
	m.refreshing = false
	m.interacting = false

	// Auto-preview first resource
	if len(cached) > 0 {
		m.mode = viewDetail
		r := cached[0]
		m.detailLoading = true
		m.detailID = r.ID
		return func() tea.Msg {
			return LoadDetailMsg{ServiceName: name, ResourceID: r.ID}
		}
	}
	return nil
}

// SetResources is called when resources have been loaded.
// Returns a Cmd to auto-preview the first resource (if any).
func (m *Model) SetResources(resources []service.Resource, err error, notIntegrated bool) tea.Cmd {
	m.loading = false
	m.refreshing = false
	m.resources = resources
	m.err = err
	m.cursor = 0
	m.notIntegrated = notIntegrated

	// If a cross-tab navigation is pending, position cursor on the target resource
	// and skip auto-preview (the detail is already loading for the target).
	if m.pendingNavigateID != "" && err == nil && len(resources) > 0 {
		m.applyCursorForPendingNavigate()
		return nil
	}

	// Auto-preview: switch to detail view and load first resource
	if err == nil && !notIntegrated && len(resources) > 0 {
		m.mode = viewDetail
		r := resources[0]
		m.detailLoading = true
		m.detailErr = nil
		m.detail = nil
		m.detailID = r.ID
		m.scrollOffset = 0
		return func() tea.Msg {
			return LoadDetailMsg{ServiceName: m.service, ResourceID: r.ID}
		}
	}
	return nil
}

// RefreshResources updates the resource list from a background refresh.
// Preserves cursor position when possible. Only updates if we're still on the same service.
func (m *Model) RefreshResources(resources []service.Resource) {
	m.refreshing = false
	if resources == nil {
		return
	}
	// Preserve cursor: try to keep the same resource selected
	var selectedID string
	if m.cursor < len(m.resources) && m.cursor >= 0 {
		selectedID = m.resources[m.cursor].ID
	}

	m.resources = resources
	m.err = nil

	// Restore cursor position
	if selectedID != "" {
		for i, r := range resources {
			if r.ID == selectedID {
				m.cursor = i
				return
			}
		}
	}
	// If the previously selected resource is gone, clamp cursor
	if m.cursor >= len(resources) {
		m.cursor = len(resources) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// SetDetail is called when a resource detail has been loaded.
func (m *Model) SetDetail(detail *service.ResourceDetail, err error) {
	m.detailLoading = false
	m.detail = detail
	m.detailErr = err
	m.scrollOffset = 0
}

// IsWorkersDetail returns true if we're viewing a Workers resource detail.
func (m Model) IsWorkersDetail() bool {
	return m.mode == viewDetail && m.service == "Workers"
}

// CurrentDetailName returns the name of the currently displayed detail resource.
func (m Model) CurrentDetailName() string {
	if m.detail != nil {
		return m.detail.Name
	}
	return ""
}

// ResourceDetail returns the full resource detail, or nil if not loaded.
func (m Model) ResourceDetail() *service.ResourceDetail {
	return m.detail
}

// InDetailView returns true if the detail panel is in the detail view
// (either auto-preview is loading or detail data is available).
func (m Model) InDetailView() bool {
	return m.mode == viewDetail && (m.detail != nil || m.detailLoading)
}

// BackToList resets the detail panel from detail view back to list view.
// Used when the app intercepts a detail navigation (e.g. Env Variables)
// and needs the detail model to remain in list mode.
func (m *Model) BackToList() {
	m.mode = viewList
	m.detail = nil
	m.detailErr = nil
	m.detailID = ""
	m.detailLoading = false
	m.scrollOffset = 0
	m.interacting = false
}

// --- D1 SQL Console helpers ---

// PreviewD1Schema sets up the D1 database ID and marks the schema as loading,
// without initializing the interactive SQL console. Used in preview mode so the
// schema is visible in the read-only detail view.
func (m *Model) PreviewD1Schema(databaseID string) {
	m.d1DatabaseID = databaseID
	m.d1SchemaTables = nil
	m.d1SchemaErr = ""
	m.d1SchemaLoading = true
}

// InitD1Console initializes the D1 SQL console for a database.
// Preserves schema data if it was already loaded in preview mode for the same database.
func (m *Model) InitD1Console(databaseID string) tea.Cmd {
	preserveSchema := m.d1DatabaseID == databaseID && len(m.d1SchemaTables) > 0
	m.d1Active = true
	m.d1DatabaseID = databaseID
	m.d1Output = nil
	m.d1Querying = false
	if !preserveSchema {
		m.d1SchemaTables = nil
		m.d1SchemaErr = ""
		m.d1SchemaLoading = true
	}

	ti := textinput.New()
	ti.Prompt = "sql> "
	ti.PromptStyle = theme.D1PromptStyle
	ti.TextStyle = theme.ValueStyle
	ti.PlaceholderStyle = theme.DimStyle
	ti.Placeholder = "SELECT * FROM ..."
	ti.CharLimit = 0
	m.d1Input = ti
	return m.d1Input.Focus()
}

// D1DatabaseID returns the current D1 database UUID.
func (m Model) D1DatabaseID() string {
	return m.d1DatabaseID
}

// D1Active returns whether the D1 console is active.
func (m Model) D1Active() bool {
	return m.d1Active
}

// SetD1Schema sets the schema data for the D1 detail view.
func (m *Model) SetD1Schema(tables []service.SchemaTable, err error) {
	m.d1SchemaLoading = false
	if err != nil {
		m.d1SchemaErr = err.Error()
		m.d1SchemaTables = nil
	} else {
		m.d1SchemaErr = ""
		m.d1SchemaTables = tables
	}
}

// SetD1SchemaLoading marks the schema as loading (for auto-refresh after mutation).
func (m *Model) SetD1SchemaLoading() {
	m.d1SchemaLoading = true
}

// SetD1QueryResult appends query results to the output area.
func (m *Model) SetD1QueryResult(result *service.D1QueryResult, err error) {
	m.d1Querying = false
	if err != nil {
		m.d1Output = append(m.d1Output, theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", err)))
		m.d1Output = append(m.d1Output, "")
		return
	}
	// Append the output lines
	outputLines := strings.Split(result.Output, "\n")
	m.d1Output = append(m.d1Output, outputLines...)
	if result.Meta != "" {
		m.d1Output = append(m.d1Output, theme.D1MetaStyle.Render(result.Meta))
	}
	m.d1Output = append(m.d1Output, "") // blank separator between queries
}

// ClearD1 resets all D1 console state (used on navigation away).
func (m *Model) ClearD1() {
	m.d1Active = false
	m.d1Output = nil
	m.d1Querying = false
	m.d1DatabaseID = ""
	m.d1SchemaTables = nil
	m.d1SchemaErr = ""
	m.d1SchemaLoading = false
	m.d1Input.Blur()
}

// NavigateToDetail switches directly to the detail view for a resource (used by search).
func (m *Model) NavigateToDetail(resourceID string) {
	m.mode = viewDetail
	m.detailLoading = true
	m.detailErr = nil
	m.detail = nil
	m.detailID = resourceID
	m.scrollOffset = 0
	m.pendingNavigateID = resourceID

	// If we already have the resource list, position the cursor now.
	m.applyCursorForPendingNavigate()
}

// applyCursorForPendingNavigate positions the list cursor on the resource
// matching pendingNavigateID, then clears the pending ID.
// Matches by ID first, then falls back to Name (some bindings like Queues
// store the resource name rather than the UUID).
func (m *Model) applyCursorForPendingNavigate() {
	if m.pendingNavigateID == "" || len(m.resources) == 0 {
		return
	}
	// Try matching by ID
	for i, r := range m.resources {
		if r.ID == m.pendingNavigateID {
			m.cursor = i
			m.pendingNavigateID = ""
			return
		}
	}
	// Fall back to matching by Name (some bindings store names, not UUIDs)
	for i, r := range m.resources {
		if r.Name == m.pendingNavigateID {
			m.cursor = i
			m.pendingNavigateID = ""
			return
		}
	}
	// Resource not found in list (may arrive after re-sort) — keep pending
}

// SpinnerInit returns the command to start the spinner ticking.
func (m Model) SpinnerInit() tea.Cmd {
	return m.spinner.Tick
}

// IsLoading returns whether the detail panel is in a loading state (spinner should run).
func (m Model) IsLoading() bool {
	return m.loading || m.detailLoading || m.d1SchemaLoading || m.d1Querying
}

// UpdateSpinner forwards a message to the embedded spinner and returns the updated model + cmd.
func (m *Model) UpdateSpinner(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// SelectedResource returns the currently highlighted resource, if any.
func (m Model) SelectedResource() *service.Resource {
	if len(m.resources) > 0 && m.cursor < len(m.resources) {
		return &m.resources[m.cursor]
	}
	return nil
}

// Update handles events for the detail panel.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	// Handle mouse clicks
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		if mouseMsg.Button == tea.MouseButtonLeft && mouseMsg.Action == tea.MouseActionRelease {
			// Check resource list item clicks (list mode only)
			if !m.dropdownOpen && len(m.resources) > 0 {
				for i := range m.resources {
					if z := zone.Get(ResourceItemZoneID(i)); z != nil && z.InBounds(mouseMsg) {
						if i == m.cursor && m.focus == FocusList {
							// Already selected — enter interactive mode (ReadWrite only)
							if m.activeServiceMode() == ReadWrite {
								m.interacting = true
								m.focus = FocusDetail
								m.scrollOffset = 0
								if m.detail != nil {
									svc := m.service
									resID := m.detailID
									return m, func() tea.Msg {
										return EnterInteractiveMsg{ServiceName: svc, ResourceID: resID, Mode: ReadWrite}
									}
								}
							}
							return m, nil
						}
						// Select the item and auto-preview
						m.cursor = i
						m.focus = FocusList
						return m, m.autoPreview()
					}
				}
			}
			// Copy-on-click for detail view fields
			if mouseMsg.Action == tea.MouseActionRelease {
				relY := mouseMsg.Y - m.yOffset
				if text, found := m.copyTargets[relY]; found {
					return m, func() tea.Msg {
						return CopyToClipboardMsg{Text: text}
					}
				}
			}
		}
		// Also handle press for copy-on-click (backward compat)
		if mouseMsg.Button == tea.MouseButtonLeft && mouseMsg.Action == tea.MouseActionPress {
			relY := mouseMsg.Y - m.yOffset
			if text, found := m.copyTargets[relY]; found {
				return m, func() tea.Msg {
					return CopyToClipboardMsg{Text: text}
				}
			}
		}
		return m, nil
	}

	// When D1 console is active, forward all messages to the textinput for cursor blink
	if m.d1Active && m.mode == viewDetail && m.focus == FocusDetail && m.interacting {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			return m.updateD1(msg)
		default:
			// Forward cursor blink and other messages to textinput
			var cmd tea.Cmd
			m.d1Input, cmd = m.d1Input.Update(msg)
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Dropdown takes exclusive key focus when open
		if m.dropdownOpen {
			return m.updateDropdown(msg)
		}

		// 's' toggles the service dropdown from either pane
		if msg.String() == "s" {
			m.OpenDropdown()
			return m, nil
		}

		// 'tab' switches focus between panes (only for ReadWrite services in detail view).
		// From list: enters interactive mode (same as enter).
		// From detail: exits interactive mode back to list.
		// ReadOnly services don't support interactive mode, so tab is a no-op.
		if msg.String() == "tab" && m.mode == viewDetail && m.activeServiceMode() == ReadWrite {
			if m.focus == FocusList {
				// Enter interactive mode
				if len(m.resources) > 0 && m.cursor < len(m.resources) {
					m.interacting = true
					m.focus = FocusDetail
					m.scrollOffset = 0
					if m.detail != nil {
						svc := m.service
						resID := m.detailID
						return m, func() tea.Msg {
							return EnterInteractiveMsg{ServiceName: svc, ResourceID: resID, Mode: ReadWrite}
						}
					}
				}
			} else {
				// Exit interactive mode
				m.interacting = false
				m.focus = FocusList
			}
			return m, nil
		}

		switch m.focus {
		case FocusList:
			return m.updateList(msg)
		case FocusDetail:
			if m.mode == viewDetail {
				return m.updateDetail(msg)
			}
			// Detail pane focused but no detail loaded — ignore keys
		}
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			return m, m.autoPreview()
		}
	case "down", "j":
		if m.cursor < len(m.resources)-1 {
			m.cursor++
			return m, m.autoPreview()
		}
	case "enter":
		// Switch to interactive mode — only for ReadWrite services (e.g. D1 SQL console).
		// ReadOnly services (Workers, KV, R2, Queues) have preview-only detail.
		if m.activeServiceMode() == ReadWrite && len(m.resources) > 0 && m.cursor < len(m.resources) && m.mode == viewDetail {
			m.interacting = true
			m.focus = FocusDetail
			m.scrollOffset = 0
			if m.detail != nil {
				svc := m.service
				resID := m.detailID
				return m, func() tea.Msg {
					return EnterInteractiveMsg{ServiceName: svc, ResourceID: resID, Mode: ReadWrite}
				}
			}
			return m, nil
		}
	case "d":
		// Delete resource — only for deletable services (not Workers)
		if isDeletableService(m.service) && len(m.resources) > 0 && m.cursor < len(m.resources) {
			r := m.resources[m.cursor]
			return m, func() tea.Msg {
				return DeleteResourceRequestMsg{
					ServiceName:  m.service,
					ResourceID:   r.ID,
					ResourceName: r.Name,
				}
			}
		}
	}
	return m, nil
}

// autoPreview emits a LoadDetailMsg for the currently highlighted resource
// if it differs from the already-loaded detail (staleness check).
// Also switches to viewDetail mode so the right pane shows the detail.
func (m *Model) autoPreview() tea.Cmd {
	if len(m.resources) == 0 || m.cursor >= len(m.resources) {
		return nil
	}
	r := m.resources[m.cursor]
	m.mode = viewDetail

	// Staleness check: if we already have detail for this resource, skip re-fetch
	if r.ID == m.detailID && (m.detail != nil || m.detailLoading) {
		return nil
	}

	m.detailLoading = true
	m.detailErr = nil
	m.detail = nil
	m.detailID = r.ID
	m.scrollOffset = 0
	// Clear stale D1 schema when previewing a different resource
	if m.service == "D1" {
		m.d1SchemaTables = nil
		m.d1SchemaErr = ""
		m.d1SchemaLoading = false
	}
	return func() tea.Msg {
		return LoadDetailMsg{ServiceName: m.service, ResourceID: r.ID}
	}
}

// updateDropdown handles key events when the service dropdown is open.
func (m Model) updateDropdown(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.dropdownCursor < len(m.services)-1 {
			m.dropdownCursor++
		}
	case "k", "up":
		if m.dropdownCursor > 0 {
			m.dropdownCursor--
		}
	case "enter":
		if m.dropdownCursor >= 0 && m.dropdownCursor < len(m.services) {
			entry := m.services[m.dropdownCursor]
			m.dropdownOpen = false
			if entry.Integrated && entry.Name != m.service {
				return m, func() tea.Msg {
					return SelectServiceMsg{ServiceName: entry.Name}
				}
			}
		}
	case "esc", "s":
		m.dropdownOpen = false
	}
	return m, nil
}

// isDeletableService returns true for services that support resource deletion from the list view.
func isDeletableService(name string) bool {
	switch name {
	case "KV", "D1", "R2", "Queues":
		return true
	}
	return false
}

func (m Model) updateDetail(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		// Exit interactive mode and return focus to list pane
		m.interacting = false
		m.focus = FocusList
		return m, nil
	case "t":
		// Only available in Workers detail view — starts tail on Monitoring tab
		if m.service != "Workers" || m.detail == nil {
			return m, nil
		}
		// Emit TailStartMsg — the app layer handles routing to the Monitoring tab
		scriptName := m.detail.Name
		return m, func() tea.Msg {
			return TailStartMsg{ScriptName: scriptName}
		}
	case "up", "k":
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
	case "down", "j":
		// Clamp scroll offset to avoid drifting beyond content
		maxScroll := m.calcMaxScroll()
		if m.scrollOffset < maxScroll {
			m.scrollOffset++
		}
	}
	return m, nil
}

// updateD1 handles key events when the D1 SQL console is active.
func (m Model) updateD1(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Exit interactive mode, switch focus to list pane
		m.interacting = false
		m.focus = FocusList
		return m, nil
	case tea.KeyEnter:
		// Submit the SQL query
		sql := strings.TrimSpace(m.d1Input.Value())
		if sql == "" || m.d1Querying {
			return m, nil
		}
		m.d1Querying = true
		m.d1Output = append(m.d1Output, theme.D1PromptStyle.Render("sql> ")+theme.ValueStyle.Render(sql))
		m.d1Input.Reset()
		dbID := m.d1DatabaseID
		return m, func() tea.Msg {
			return D1QueryMsg{DatabaseID: dbID, SQL: sql}
		}
	}

	// Forward all other keys to the textinput
	var cmd tea.Cmd
	m.d1Input, cmd = m.d1Input.Update(msg)
	return m, cmd
}

// calcMaxScroll computes the maximum scroll offset for the detail view.
func (m Model) calcMaxScroll() int {
	if m.detail == nil {
		return 0
	}
	// title + sep + fields + extra content + help
	extraLines := 0
	if m.detail.ExtraContent != "" {
		extraLines = len(strings.Split(m.detail.ExtraContent, "\n"))
	}
	totalLines := 2 + len(m.detail.Fields) + extraLines + 2
	contentHeight := m.height - 4
	if contentHeight < 1 {
		contentHeight = 1
	}
	max := totalLines - contentHeight
	if max < 0 {
		max = 0
	}
	return max
}

// View renders the detail panel as a dual-pane layout:
//   - Dropdown line at the top (collapsed service indicator or expanded overlay)
//   - Left pane (~20%): resource list
//   - Vertical separator: │
//   - Right pane (~80%): resource detail
func (m Model) View() string {
	// Clear copy targets for this render cycle (map is a reference type,
	// so writes from a value receiver mutate the underlying map).
	for k := range m.copyTargets {
		delete(m.copyTargets, k)
	}

	borderStyle := theme.BorderStyle
	if m.focused {
		borderStyle = theme.ActiveBorderStyle
	}

	// Total content height inside the border (border takes 2 lines)
	contentHeight := m.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Dropdown line always takes 1 line at the top
	dropdownLine := m.viewDropdownLine()
	paneHeight := contentHeight - 1 // remaining for the dual-pane area

	// If dropdown is open, overlay takes precedence over the panes
	if m.dropdownOpen {
		overlay := m.viewDropdownOverlay(contentHeight - 1) // full remaining height for overlay
		content := dropdownLine + "\n" + overlay
		contentLines := strings.Split(content, "\n")
		if len(contentLines) > contentHeight {
			contentLines = contentLines[:contentHeight]
			content = strings.Join(contentLines, "\n")
		}
		return borderStyle.
			Width(m.width - 2).
			Height(contentHeight).
			Render(content)
	}

	if paneHeight < 1 {
		paneHeight = 1
	}

	// Calculate pane widths: left ~25%, right ~75%.
	// Outer border takes 2 chars on each side. Right pane has its own rounded border (2 chars).
	innerWidth := m.width - 4 // outer border takes 2 chars on each side
	if innerWidth < 10 {
		innerWidth = 10
	}
	leftWidth := innerWidth / 4
	if leftWidth < 15 {
		leftWidth = 15
	}
	rightOuterWidth := innerWidth - leftWidth // total width for right pane including its border

	// Right pane inner dimensions (inside its rounded border)
	rightInnerWidth := rightOuterWidth - 2
	if rightInnerWidth < 5 {
		rightInnerWidth = 5
	}
	rightInnerHeight := paneHeight - 2
	if rightInnerHeight < 1 {
		rightInnerHeight = 1
	}

	leftPane := m.viewResourceList(leftWidth, paneHeight)
	rightPaneLines := m.viewResourceDetail(rightInnerWidth, rightInnerHeight)

	// Render right pane inside a rounded border box.
	// Orange border when in interactive mode, dark gray otherwise.
	rightBorderColor := theme.ColorDarkGray
	if m.focus == FocusDetail && m.interacting {
		rightBorderColor = theme.ColorOrange
	}
	rightContent := strings.Join(rightPaneLines, "\n")
	rightBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(rightBorderColor).
		Width(rightInnerWidth).
		Height(rightInnerHeight).
		Render(rightContent)

	// Split the rendered right box back into lines for side-by-side join
	rightBoxLines := strings.Split(rightBox, "\n")

	// Join left pane and right box side by side (no divider — the border IS the separator)
	dualPane := joinSideBySideNoDivider(leftPane, rightBoxLines, leftWidth, paneHeight)

	content := dropdownLine + "\n" + dualPane
	contentLines := strings.Split(content, "\n")
	if len(contentLines) > contentHeight {
		contentLines = contentLines[:contentHeight]
		content = strings.Join(contentLines, "\n")
	}

	return borderStyle.
		Width(m.width - 2).
		Height(contentHeight).
		Render(content)
}

// viewDropdownLine renders the collapsed dropdown indicator line.
// e.g. "▼ KV (5 items)" or "▼ Select Service"
func (m Model) viewDropdownLine() string {
	arrow := theme.DimStyle.Render("▼")
	if m.dropdownOpen {
		arrow = theme.TitleStyle.Render("▲")
	}

	if m.service == "" {
		return fmt.Sprintf(" %s %s", arrow, theme.DimStyle.Render("Select Service"))
	}

	serviceName := theme.TitleStyle.Render(m.service)
	count := ""
	if !m.loading && m.err == nil && !m.notIntegrated {
		count = theme.DimStyle.Render(fmt.Sprintf(" (%d items)", len(m.resources)))
	} else if m.loading {
		count = " " + m.spinner.View()
	}
	return fmt.Sprintf(" %s %s%s", arrow, serviceName, count)
}

// viewDropdownOverlay renders the expanded service dropdown list.
func (m Model) viewDropdownOverlay(maxHeight int) string {
	if len(m.services) == 0 {
		return theme.DimStyle.Render("  No services available")
	}

	var lines []string
	for i, s := range m.services {
		cursor := "  "
		if i == m.dropdownCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		nameStyle := theme.NormalItemStyle
		if i == m.dropdownCursor {
			nameStyle = theme.SelectedItemStyle
		}

		label := nameStyle.Render(s.Name)
		if !s.Integrated {
			label = theme.DimStyle.Render(s.Name + " (coming soon)")
			if i == m.dropdownCursor {
				label = theme.SelectedItemStyle.Render(s.Name) + theme.DimStyle.Render(" (coming soon)")
			}
		}

		// Mark current service with a bullet
		current := "  "
		if s.Name == m.service {
			current = theme.TitleStyle.Render("● ")
		}

		lines = append(lines, fmt.Sprintf("%s%s%s", cursor, current, label))
	}

	// Pad to maxHeight
	for len(lines) < maxHeight {
		lines = append(lines, "")
	}
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	return strings.Join(lines, "\n")
}

// viewResourceList renders the left pane: resource list items without outer border.
func (m Model) viewResourceList(width, height int) []string {
	lines := make([]string, 0, height)

	if m.service == "" {
		lines = append(lines, theme.DimStyle.Render(" No service"))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	if m.loading {
		lines = append(lines, fmt.Sprintf(" %s", m.spinner.View()))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	if m.err != nil {
		lines = append(lines, theme.ErrorStyle.Render(" Error"))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	if m.notIntegrated {
		lines = append(lines, theme.DimStyle.Render(" Coming soon"))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	if len(m.resources) == 0 {
		lines = append(lines, theme.DimStyle.Render(" No resources"))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	// Resource items
	availableWidth := width - 4
	if availableWidth < 5 {
		availableWidth = 5
	}
	visibleHeight := height
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	// Build visual lines: resource items + optional separator between managed/unmanaged.
	// visualLines holds rendered strings; visualToRes maps visual index → resource index
	// (-1 for separator lines that aren't selectable).
	hasUnmanaged := m.managedIDs != nil && m.managedCount > 0 && m.managedCount < len(m.resources)

	type visualLine struct {
		text  string
		resID int // resource index, or -1 for separator lines
	}
	var vLines []visualLine

	for i, r := range m.resources {
		// Insert separator before first unmanaged item
		if hasUnmanaged && i == m.managedCount {
			separatorLabel := "unmanaged"
			if m.service != "Workers" {
				separatorLabel = "unbound"
			}
			vLines = append(vLines, visualLine{text: "", resID: -1})
			vLines = append(vLines, visualLine{
				text:  theme.DimStyle.Render(" " + separatorLabel),
				resID: -1,
			})
		}

		cursor := " "
		nameStyle := theme.NormalItemStyle
		if m.IsManaged(r.ID) {
			nameStyle = theme.NormalItemStyle // white for managed
		} else if m.managedIDs != nil {
			nameStyle = theme.DimStyle // dim/gray for unmanaged
		}

		if i == m.cursor {
			cursor = theme.SelectedItemStyle.Render(">")
			nameStyle = theme.SelectedItemStyle
		}

		name := nameStyle.Render(truncateRunes(r.Name, availableWidth))
		line := fmt.Sprintf("%s%s", cursor, name)
		vLines = append(vLines, visualLine{
			text:  zone.Mark(ResourceItemZoneID(i), line),
			resID: i,
		})
	}

	// Find the visual index of the cursor's resource to anchor scrolling
	cursorVisIdx := 0
	for vi, vl := range vLines {
		if vl.resID == m.cursor {
			cursorVisIdx = vi
			break
		}
	}

	// Scroll window anchored on the cursor's visual position
	startIdx := 0
	if cursorVisIdx >= visibleHeight {
		startIdx = cursorVisIdx - visibleHeight + 1
	}
	endIdx := startIdx + visibleHeight
	if endIdx > len(vLines) {
		endIdx = len(vLines)
	}
	for _, vl := range vLines[startIdx:endIdx] {
		lines = append(lines, vl.text)
	}

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// viewResourceDetail renders the right pane: resource detail content without outer border.
func (m Model) viewResourceDetail(width, height int) []string {
	lines := make([]string, 0, height)

	// When no detail is loaded yet, show context-appropriate hint or loading spinner
	if m.detail == nil && !m.detailLoading && m.detailErr == nil {
		hint := theme.DimStyle.Render(" Select a resource")
		if len(m.resources) == 0 {
			hint = "" // no resources, keep pane empty
		}
		lines = append(lines, hint)
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	if m.detailLoading {
		lines = append(lines, fmt.Sprintf(" %s %s", m.spinner.View(), theme.DimStyle.Render("Loading details...")))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	if m.detailErr != nil {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s", m.detailErr.Error())))
		for len(lines) < height {
			lines = append(lines, "")
		}
		return lines
	}

	d := m.detail

	// Title + copy icon
	title := theme.DimStyle.Render(fmt.Sprintf(" %s ", m.service)) + theme.TitleStyle.Render(d.Name) + copyIcon()
	sepWidth := width - 3
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", sepWidth))

	// Track which allLines indices are copyable
	copyLineMap := make(map[int]string)
	copyLineMap[0] = d.Name // title line

	allLines := []string{title, sep}

	for _, f := range d.Fields {
		label := theme.LabelStyle.Render(fmt.Sprintf(" %-16s", f.Label))
		// Split multi-line values (e.g. Worker bindings) into separate visual lines.
		// First line gets the label; continuation lines are indented to align under the value.
		valueLines := strings.Split(f.Value, "\n")
		for vi, vl := range valueLines {
			if vi == 0 {
				line := fmt.Sprintf("%s %s", label, theme.ValueStyle.Render(vl))
				if isCopyableLabel(f.Label) {
					line += copyIcon()
					copyLineMap[len(allLines)] = f.Value
				}
				allLines = append(allLines, line)
			} else {
				// Continuation: 1 leading space + 16 label chars + 1 space = 18 char indent
				indent := strings.Repeat(" ", 18)
				allLines = append(allLines, indent+theme.ValueStyle.Render(vl))
			}
		}
	}

	// For D1 with active console, use the special D1 split layout
	if m.service == "D1" && m.d1Active {
		return m.viewResourceDetailD1(width, height, title, sep, allLines, copyLineMap)
	}

	// For D1 in preview mode, render schema below fields (read-only)
	if m.service == "D1" && !m.d1Active {
		schemaSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
			strings.Repeat("─", sepWidth))
		allLines = append(allLines, "", schemaSep)
		if m.d1SchemaLoading {
			allLines = append(allLines, fmt.Sprintf(" %s %s", m.spinner.View(), theme.DimStyle.Render("Loading schema...")))
		} else if m.d1SchemaErr != "" {
			allLines = append(allLines, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s", m.d1SchemaErr)))
		} else if len(m.d1SchemaTables) > 0 {
			allLines = append(allLines, " "+theme.D1SchemaTitleStyle.Render("Schema"))
			allLines = append(allLines, m.renderSchemaStyled(m.d1SchemaTables)...)
		} else {
			allLines = append(allLines, " "+theme.D1SchemaTitleStyle.Render("Schema"))
			allLines = append(allLines, theme.DimStyle.Render(" No tables found"))
		}
		allLines = append(allLines, "")
		allLines = append(allLines, theme.DimStyle.Render(" Press enter to open SQL console"))
	}

	// Append ExtraContent if present
	if d.ExtraContent != "" {
		extraLines := strings.Split(d.ExtraContent, "\n")
		for _, el := range extraLines {
			allLines = append(allLines, theme.DimStyle.Render(el))
		}
	}

	// Scroll
	visibleHeight := height
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	maxScroll := len(allLines) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	offset := m.scrollOffset
	if offset > maxScroll {
		offset = maxScroll
	}
	endIdx := offset + visibleHeight
	if endIdx > len(allLines) {
		endIdx = len(allLines)
	}

	// Register copy targets for visible lines
	m.registerCopyTargets(copyLineMap, offset, endIdx)

	lines = allLines[offset:endIdx]

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// viewResourceDetailD1 renders the right pane for D1 with the SQL console split.
func (m Model) viewResourceDetailD1(width, height int, title, sep string, topFieldLines []string, copyLineMap map[int]string) []string {
	// Compact metadata at top
	topLines := []string{title, sep}
	topLines = append(topLines, m.renderD1CompactFields(copyLineMap)...)

	metaHeight := len(topLines)

	// Separator between metadata and the split pane
	panesSepWidth := width - 3
	if panesSepWidth < 0 {
		panesSepWidth = 0
	}
	panesSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", panesSepWidth))
	topLines = append(topLines, panesSep)
	metaHeight++

	// Bottom region: left/right split for SQL console and schema
	paneHeight := height - metaHeight
	if paneHeight < 5 {
		paneHeight = 5
	}

	halfWidth := width / 2
	leftWidth := halfWidth
	rightWidth := width - halfWidth - 1 // -1 for divider

	leftPane := m.renderD1SQLConsole(leftWidth, paneHeight)
	rightPane := m.renderD1SchemaPane(rightWidth, paneHeight)

	divider := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render("│")
	splitPane := joinSideBySide(leftPane, rightPane, divider, leftWidth, paneHeight)

	// Register copy targets for metadata lines
	m.registerCopyTargets(copyLineMap, 0, len(topLines))

	result := strings.Join(topLines, "\n") + "\n" + splitPane
	lines := strings.Split(result, "\n")

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// (old viewList, viewDetail, viewDetailWithD1 methods removed — replaced by
// viewResourceList, viewResourceDetail, viewResourceDetailD1 in the dual-pane layout)

// renderD1CompactFields renders metadata as 2 compact rows.
// copyLineMap is populated with the topLines index → text for copyable values.
// The compact rows start at topLines index 2 (after title=0, sep=1).
func (m Model) renderD1CompactFields(copyLineMap map[int]string) []string {
	if m.detail == nil {
		return nil
	}

	fields := m.detail.Fields
	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Label] = f.Value
	}

	// Row 1: Database ID, Name, Version
	row1Parts := []string{}
	if v, ok := fieldMap["Database ID"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s%s",
			theme.LabelStyle.Render("ID"), theme.ValueStyle.Render(v), copyIcon()))
	}
	if v, ok := fieldMap["Name"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Name"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["Version"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Ver"), theme.ValueStyle.Render(v)))
	}

	// Row 2: Created, File Size, Tables, Replication
	row2Parts := []string{}
	if v, ok := fieldMap["Created"]; ok {
		// Show just the date part
		if len(v) > 10 {
			v = v[:10]
		}
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Created"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["File Size"]; ok {
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Size"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["Tables"]; ok {
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Tables"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["Replication"]; ok {
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Repl"), theme.ValueStyle.Render(v)))
	}

	var rows []string
	if len(row1Parts) > 0 {
		rows = append(rows, "  "+strings.Join(row1Parts, "   "))
		// Row 1 is at topLines index 2 (title=0, sep=1) — copy the Database ID
		if v, ok := fieldMap["Database ID"]; ok {
			copyLineMap[2] = v
		}
	}
	if len(row2Parts) > 0 {
		rows = append(rows, "  "+strings.Join(row2Parts, "   "))
	}
	return rows
}

// renderD1SQLConsole renders the SQL console left pane as a list of lines.
func (m Model) renderD1SQLConsole(width, height int) []string {
	header := theme.D1SchemaTitleStyle.Render("SQL Console")

	// Help at the bottom
	help := theme.DimStyle.Render("esc back | enter query")

	// Input line
	inputLine := m.d1Input.View()
	if m.d1Querying {
		inputLine = fmt.Sprintf("%s %s", m.spinner.View(), theme.DimStyle.Render("Running..."))
	}

	// Available lines for output (minus header, input, help)
	outputHeight := height - 3
	if outputHeight < 1 {
		outputHeight = 1
	}

	// Build output lines, wrapped/truncated to width
	var outputLines []string
	for _, line := range m.d1Output {
		// Truncate long lines to fit the pane width
		if utf8.RuneCountInString(line) > width-1 {
			runes := []rune(line)
			line = string(runes[:width-2]) + "…"
		}
		outputLines = append(outputLines, line)
	}

	// Show most recent output that fits (scroll to bottom)
	if len(outputLines) > outputHeight {
		outputLines = outputLines[len(outputLines)-outputHeight:]
	}

	// Build the pane
	lines := []string{header}

	// Pad output to fill available space
	for len(outputLines) < outputHeight {
		outputLines = append([]string{""}, outputLines...)
	}
	lines = append(lines, outputLines...)
	lines = append(lines, inputLine)
	lines = append(lines, help)

	// Ensure exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return lines
}

// renderD1SchemaPane renders the schema diagram right pane with syntax coloring.
func (m Model) renderD1SchemaPane(width, height int) []string {
	header := theme.D1SchemaTitleStyle.Render("Schema")

	lines := []string{header}

	if m.d1SchemaLoading {
		lines = append(lines, fmt.Sprintf("%s %s", m.spinner.View(), theme.DimStyle.Render("Loading schema...")))
	} else if m.d1SchemaErr != "" {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", m.d1SchemaErr)))
	} else if len(m.d1SchemaTables) == 0 {
		lines = append(lines, theme.DimStyle.Render("No tables found"))
	} else {
		lines = append(lines, m.renderSchemaStyled(m.d1SchemaTables)...)
	}

	// Pad to exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return lines
}

// renderSchemaStyled renders structured schema data with per-element syntax coloring.
// Returns a slice of styled lines ready for display.
func (m Model) renderSchemaStyled(tables []service.SchemaTable) []string {
	var lines []string

	// Build a global FK list for the relations summary at the bottom
	var allFKs []string

	for ti, t := range tables {
		// Table name header
		lines = append(lines, theme.D1SchemaTableNameStyle.Render(t.Name))

		// Build FK lookup for this table
		fkMap := make(map[string]string)
		for _, fk := range t.FKs {
			ref := fmt.Sprintf("-> %s.%s", fk.ToTable, fk.ToCol)
			fkMap[fk.FromCol] = ref
			allFKs = append(allFKs, fmt.Sprintf("%s.%s -> %s.%s", t.Name, fk.FromCol, fk.ToTable, fk.ToCol))
		}

		// Calculate max column name width for alignment
		maxNameLen := 0
		for _, c := range t.Columns {
			if len(c.Name) > maxNameLen {
				maxNameLen = len(c.Name)
			}
		}
		if maxNameLen < 4 {
			maxNameLen = 4
		}

		for i, c := range t.Columns {
			// Branch character
			branchChar := "├─"
			if i == len(t.Columns)-1 {
				branchChar = "└─"
			}
			branch := theme.D1SchemaBranchStyle.Render(branchChar)

			// Tag: PK, FK, or blank
			var tag string
			if c.PK {
				tag = theme.D1SchemaPKTagStyle.Render("PK")
			} else if _, isFK := fkMap[c.Name]; isFK {
				tag = theme.D1SchemaFKTagStyle.Render("FK")
			} else {
				tag = "  "
			}

			// Column name (padded for alignment)
			paddedName := fmt.Sprintf("%-*s", maxNameLen, c.Name)
			colName := theme.D1SchemaColNameStyle.Render(paddedName)

			// Column type
			colType := c.Type
			if colType == "" {
				colType = "ANY"
			}
			colTypeStyled := theme.D1SchemaColTypeStyle.Render(fmt.Sprintf("%-8s", colType))

			// NOT NULL constraint (skip for PKs since they're implicitly NOT NULL)
			notNull := ""
			if c.NotNull && !c.PK {
				notNull = " " + theme.D1SchemaNotNullStyle.Render("NOT NULL")
			}

			// FK reference
			fkRef := ""
			if ref, ok := fkMap[c.Name]; ok {
				fkRef = "  " + theme.D1SchemaFKRefStyle.Render(ref)
			}

			line := fmt.Sprintf("%s %s %s  %s%s%s", branch, tag, colName, colTypeStyled, notNull, fkRef)
			lines = append(lines, line)
		}

		// Blank line between tables (but not after the last one before relations)
		if ti < len(tables)-1 {
			lines = append(lines, "")
		}
	}

	// Relations summary
	if len(allFKs) > 0 {
		lines = append(lines, "")
		lines = append(lines, theme.D1SchemaTableNameStyle.Render("Relations"))
		for _, fk := range allFKs {
			lines = append(lines, theme.D1SchemaRelationStyle.Render("  "+fk))
		}
	}

	return lines
}

// registerCopyTargets maps allLines indices (within the visible range) to
// absolute Y screen coordinates in the copyTargets map.
// copyLineMap: allLines index → raw text to copy.
// visStart/visEnd: the range of allLines indices currently visible on screen.
func (m Model) registerCopyTargets(copyLineMap map[int]string, visStart, visEnd int) {
	for idx, text := range copyLineMap {
		if idx >= visStart && idx < visEnd {
			screenY := idx - visStart // relative to content area top
			m.copyTargets[screenY] = text
		}
	}
}

// joinSideBySide joins two panes (as line arrays) side by side with a divider.
// leftWidth is used to pad left lines to a fixed column so the divider aligns.
func joinSideBySide(left, right []string, divider string, leftWidth, height int) string {
	var result []string
	for i := 0; i < height; i++ {
		l := ""
		if i < len(left) {
			l = left[i]
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		// Pad left to fixed width using rune count for ANSI-safe padding
		visLen := runeWidth(l)
		if visLen < leftWidth {
			l = l + strings.Repeat(" ", leftWidth-visLen)
		}
		result = append(result, l+divider+r)
	}
	return strings.Join(result, "\n")
}

// joinSideBySideNoDivider joins left pane lines and a pre-rendered right box
// side by side without an explicit divider (the right box's border serves as separator).
func joinSideBySideNoDivider(left []string, right []string, leftWidth, height int) string {
	var result []string
	for i := 0; i < height; i++ {
		l := ""
		if i < len(left) {
			l = left[i]
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		visLen := runeWidth(l)
		if visLen < leftWidth {
			l = l + strings.Repeat(" ", leftWidth-visLen)
		}
		result = append(result, l+r)
	}
	return strings.Join(result, "\n")
}

// runeWidth returns the visible rune count of a string (approximate — doesn't strip ANSI).
// For our use case, lipgloss-styled strings have ANSI sequences, so we use lipgloss.Width.
func runeWidth(s string) int {
	return lipgloss.Width(s)
}

// truncateRunes truncates a string to maxLen runes, appending "..." if needed.
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxLen {
		return s
	}
	if maxLen <= 3 {
		runes := []rune(s)
		return string(runes[:maxLen])
	}
	runes := []rune(s)
	return string(runes[:maxLen-3]) + "..."
}
