package detail

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	"github.com/oarafat/orangeshell/internal/wrangler"
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

// Messages, D1, version history, build log, dropdown, and helper code are in
// detail_messages.go, detail_d1.go, detail_versions.go, detail_dropdown.go, detail_helpers.go

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

	// Version history (Workers only)
	versionHistory        []wrangler.VersionHistoryEntry
	versionHistoryLoading bool
	versionHistoryErr     error
	versionHistoryCursor  int    // selected row in the version history table
	versionHistoryScroll  int    // scroll offset for the table
	versionHistoryScript  string // script name the history was fetched for

	// Build log view (Workers Builds — Phase 2)
	buildLogVisible bool                         // true when showing a build log overlay
	buildLogEntry   wrangler.VersionHistoryEntry // the entry whose build log is shown
	buildLogLines   []string                     // log text lines
	buildLogLoading bool
	buildLogErr     error
	buildLogScroll  int // scroll offset within the build log
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

	// Clear version history when switching services
	m.versionHistory = nil
	m.versionHistoryLoading = false
	m.versionHistoryErr = nil
	m.versionHistoryScript = ""

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
	// Close build log overlay if open (user navigated to a different resource)
	if m.buildLogVisible {
		m.CloseBuildLog()
	}
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
	return m.loading || m.detailLoading || m.d1SchemaLoading || m.d1Querying || m.versionHistoryLoading || m.buildLogLoading
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

		// 'tab' switches focus between list and detail panes.
		// ReadWrite services (D1): enters/exits interactive mode with EnterInteractiveMsg.
		// ReadOnly services (Workers, KV, etc.): toggles focus for scrolling/cursor nav.
		// When build log is visible, tab closes it instead of switching panes.
		if msg.String() == "tab" && m.mode == viewDetail {
			if m.buildLogVisible {
				m.CloseBuildLog()
				return m, nil
			}
			if m.focus == FocusList {
				if len(m.resources) > 0 && m.cursor < len(m.resources) {
					m.focus = FocusDetail
					m.scrollOffset = 0
					if m.activeServiceMode() == ReadWrite {
						m.interacting = true
						if m.detail != nil {
							svc := m.service
							resID := m.detailID
							return m, func() tea.Msg {
								return EnterInteractiveMsg{ServiceName: svc, ResourceID: resID, Mode: ReadWrite}
							}
						}
					}
				}
			} else {
				// Return focus to list pane
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

func (m Model) updateDetail(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Build log overlay intercepts all keys when visible
	if m.buildLogVisible {
		return m.updateBuildLog(msg)
	}

	switch msg.String() {
	case "esc", "backspace":
		// Exit interactive mode and return focus to list pane
		m.interacting = false
		m.focus = FocusList
		return m, nil
	case "enter":
		// Workers: open build log for CI entry
		if m.service == "Workers" && len(m.versionHistory) > 0 {
			entry := m.versionHistory[m.versionHistoryCursor]
			if entry.HasBuildLog && entry.BuildID != "" {
				m.StartBuildLogLoad(entry)
				return m, func() tea.Msg {
					return FetchBuildLogMsg{
						ScriptName: m.versionHistoryScript,
						BuildUUID:  entry.BuildID,
						Entry:      entry,
					}
				}
			}
		}
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
		// Workers: navigate version history table
		if m.service == "Workers" && len(m.versionHistory) > 0 {
			if m.versionHistoryCursor > 0 {
				m.versionHistoryCursor--
			}
			return m, nil
		}
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
	case "down", "j":
		// Workers: navigate version history table
		if m.service == "Workers" && len(m.versionHistory) > 0 {
			if m.versionHistoryCursor < len(m.versionHistory)-1 {
				m.versionHistoryCursor++
			}
			return m, nil
		}
		// Clamp scroll offset to avoid drifting beyond content
		maxScroll := m.calcMaxScroll()
		if m.scrollOffset < maxScroll {
			m.scrollOffset++
		}
	}
	return m, nil
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
	// Orange border when the detail pane has focus, dark gray otherwise.
	rightBorderColor := theme.ColorDarkGray
	if m.focus == FocusDetail {
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
	// Build log overlay takes over the entire right pane
	if m.buildLogVisible {
		return m.renderBuildLog(width, height)
	}

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

	// For Workers, append version history table below the detail fields
	if m.service == "Workers" {
		allLines = append(allLines, m.renderVersionHistory(width)...)
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
