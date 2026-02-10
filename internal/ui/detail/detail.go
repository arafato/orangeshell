package detail

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// View mode tracks whether we're looking at a list or a single item.
type viewMode int

const (
	viewList   viewMode = iota // List of resources
	viewDetail                 // Single resource detail
)

// Messages sent by the detail panel to the parent (app model).
type (
	// LoadResourcesMsg requests the app to load resources for a service.
	LoadResourcesMsg struct {
		ServiceName string
	}
	// ResourcesLoadedMsg carries the loaded resources back.
	ResourcesLoadedMsg struct {
		ServiceName   string
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
		Resources   []service.Resource
		Err         error
	}
)

// Model represents the right-side detail panel.
type Model struct {
	service string // currently selected service name
	focused bool
	width   int
	height  int
	mode    viewMode

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

	// Service not yet integrated
	notIntegrated bool

	// Scroll offset for detail view
	scrollOffset int

	// Loading spinner
	spinner spinner.Model
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
		spinner: newSpinner(),
	}
}

// NewLoading creates a detail panel model pre-set to loading state for a service.
// This avoids showing "Select a service" during initial authentication.
func NewLoading(serviceName string) Model {
	return Model{
		service: serviceName,
		loading: true,
		spinner: newSpinner(),
	}
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

	return func() tea.Msg {
		return LoadResourcesMsg{ServiceName: name}
	}
}

// SetServiceWithCache updates which service to display, showing cached data immediately
// while a background refresh is triggered. If no cache is available, falls back to loading state.
func (m *Model) SetServiceWithCache(name string, cached []service.Resource) tea.Cmd {
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

	if cached != nil {
		// Show cached data immediately, mark as refreshing
		m.resources = cached
		m.cursor = 0
		m.loading = false
		m.refreshing = true
	} else {
		// No cache — show loading spinner
		m.resources = nil
		m.cursor = 0
		m.loading = true
		m.refreshing = false
	}

	return func() tea.Msg {
		return LoadResourcesMsg{ServiceName: name}
	}
}

// SetResources is called when resources have been loaded.
func (m *Model) SetResources(resources []service.Resource, err error, notIntegrated bool) {
	m.loading = false
	m.refreshing = false
	m.resources = resources
	m.err = err
	m.cursor = 0
	m.notIntegrated = notIntegrated
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

// NavigateToDetail switches directly to the detail view for a resource (used by search).
func (m *Model) NavigateToDetail(resourceID string) {
	m.mode = viewDetail
	m.detailLoading = true
	m.detailErr = nil
	m.detail = nil
	m.detailID = resourceID
	m.scrollOffset = 0
}

// SpinnerInit returns the command to start the spinner ticking.
func (m Model) SpinnerInit() tea.Cmd {
	return m.spinner.Tick
}

// IsLoading returns whether the detail panel is in a loading state (spinner should run).
func (m Model) IsLoading() bool {
	return m.loading || m.detailLoading
}

// UpdateSpinner forwards a message to the embedded spinner and returns the updated model + cmd.
func (m *Model) UpdateSpinner(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// SelectedResource returns the currently highlighted resource, if any.
func (m Model) SelectedResource() *service.Resource {
	if m.mode == viewList && len(m.resources) > 0 && m.cursor < len(m.resources) {
		return &m.resources[m.cursor]
	}
	return nil
}

// Update handles events for the detail panel.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case viewList:
			return m.updateList(msg)
		case viewDetail:
			return m.updateDetail(msg)
		}
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.resources)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.resources) > 0 && m.cursor < len(m.resources) {
			r := m.resources[m.cursor]
			m.mode = viewDetail
			m.detailLoading = true
			m.detailErr = nil
			m.detail = nil
			m.detailID = r.ID
			m.scrollOffset = 0
			return m, func() tea.Msg {
				return LoadDetailMsg{ServiceName: m.service, ResourceID: r.ID}
			}
		}
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.mode = viewList
		m.detail = nil
		m.detailErr = nil
		m.detailID = ""
		m.scrollOffset = 0
		return m, nil
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

// calcMaxScroll computes the maximum scroll offset for the detail view.
func (m Model) calcMaxScroll() int {
	if m.detail == nil {
		return 0
	}
	// title + sep + fields + help
	totalLines := 2 + len(m.detail.Fields) + 2
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

// View renders the detail panel.
func (m Model) View() string {
	borderStyle := theme.BorderStyle
	if m.focused {
		borderStyle = theme.ActiveBorderStyle
	}

	contentHeight := m.height - 4 // border + title + separator
	if contentHeight < 0 {
		contentHeight = 0
	}

	var content string
	switch m.mode {
	case viewList:
		content = m.viewList(contentHeight)
	case viewDetail:
		content = m.viewDetail(contentHeight)
	}

	return borderStyle.
		Width(m.width - 2).
		Height(contentHeight).
		Render(content)
}

func (m Model) viewList(maxHeight int) string {
	title := theme.TitleStyle.Render(fmt.Sprintf("  %s", m.service))
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	if m.service == "" {
		body := theme.DimStyle.Render("\n  Select a service from the sidebar")
		return fmt.Sprintf("%s\n%s\n%s", title, sep, body)
	}

	if m.loading {
		body := fmt.Sprintf("\n  %s %s", m.spinner.View(), theme.DimStyle.Render("Loading resources..."))
		return fmt.Sprintf("%s\n%s\n%s", title, sep, body)
	}

	if m.err != nil {
		body := theme.ErrorStyle.Render(fmt.Sprintf("\n  Error: %s", m.err.Error()))
		return fmt.Sprintf("%s\n%s\n%s", title, sep, body)
	}

	if m.notIntegrated {
		body := fmt.Sprintf(
			"\n  %s integration coming soon.\n\n  %s\n  %s",
			theme.LabelStyle.Render(m.service),
			theme.DimStyle.Render("This panel will show a list of all"),
			theme.DimStyle.Render(fmt.Sprintf("%s instances in your account.", m.service)),
		)
		return fmt.Sprintf("%s\n%s\n%s", title, sep, body)
	}

	if len(m.resources) == 0 {
		body := theme.DimStyle.Render("\n  No resources found in this account")
		return fmt.Sprintf("%s\n%s\n%s", title, sep, body)
	}

	// Count line for total
	countText := fmt.Sprintf("  %d item(s)", len(m.resources))
	if m.refreshing {
		countText += "  ↻"
	}
	countLine := theme.DimStyle.Render(countText)

	// Build resource list
	var lines []string
	availableWidth := m.width - 8 // padding + borders

	for i, r := range m.resources {
		cursor := "  "
		nameStyle := theme.NormalItemStyle
		if i == m.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			nameStyle = theme.SelectedItemStyle
		}

		name := nameStyle.Render(r.Name)
		summary := theme.DimStyle.Render(truncateRunes(r.Summary, availableWidth-utf8.RuneCountInString(r.Name)-4))
		line := fmt.Sprintf("%s%s  %s", cursor, name, summary)
		lines = append(lines, line)
	}

	// Apply scroll window to list if too long
	visibleHeight := maxHeight - 4 // title + sep + count + help
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	startIdx := 0
	if m.cursor >= visibleHeight {
		startIdx = m.cursor - visibleHeight + 1
	}
	endIdx := startIdx + visibleHeight
	if endIdx > len(lines) {
		endIdx = len(lines)
	}
	visibleLines := lines[startIdx:endIdx]

	help := theme.DimStyle.Render("  enter detail  |  esc back")

	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s",
		title, sep, countLine,
		strings.Join(visibleLines, "\n"),
		help)
}

func (m Model) viewDetail(maxHeight int) string {
	if m.detailLoading {
		title := fmt.Sprintf("  %s %s", m.spinner.View(), theme.DimStyle.Render("Loading details..."))
		return title
	}

	if m.detailErr != nil {
		title := theme.TitleStyle.Render(fmt.Sprintf("  %s", m.service))
		body := theme.ErrorStyle.Render(fmt.Sprintf("\n  Error: %s", m.detailErr.Error()))
		return fmt.Sprintf("%s\n%s", title, body)
	}

	if m.detail == nil {
		return theme.DimStyle.Render("  No data")
	}

	d := m.detail
	title := theme.TitleStyle.Render(fmt.Sprintf("  %s", d.Name))
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	var fieldLines []string
	for _, f := range d.Fields {
		label := theme.LabelStyle.Render(fmt.Sprintf("  %-16s", f.Label))
		value := theme.ValueStyle.Render(f.Value)
		fieldLines = append(fieldLines, fmt.Sprintf("%s %s", label, value))
	}

	help := "\n" + theme.DimStyle.Render("  esc/backspace back  |  j/k scroll")

	// Combine all lines
	allLines := []string{title, sep}
	allLines = append(allLines, fieldLines...)
	allLines = append(allLines, help)

	// Apply scroll offset (clamped)
	visibleHeight := maxHeight
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
	visible := allLines[offset:endIdx]

	return strings.Join(visible, "\n")
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
