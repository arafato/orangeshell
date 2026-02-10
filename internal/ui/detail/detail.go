package detail

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
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

	// CopyToClipboardMsg requests the app to copy text to the system clipboard.
	CopyToClipboardMsg struct {
		Text string
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

	// Tail state (Workers live logs)
	tailLines    []service.TailLine
	tailActive   bool
	tailStarting bool // true while waiting for TailStartedMsg
	tailScroll   int  // scroll offset within log console (0 = pinned to bottom)
	tailError    string

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

// TailActive returns whether a tail session is active.
func (m Model) TailActive() bool {
	return m.tailActive
}

// SetTailStarting marks the tail as starting (waiting for session creation).
func (m *Model) SetTailStarting() {
	m.tailStarting = true
	m.tailError = ""
}

// SetTailStarted marks the tail session as active.
func (m *Model) SetTailStarted() {
	m.tailActive = true
	m.tailStarting = false
	m.tailLines = nil
	m.tailScroll = 0
	m.tailError = ""
}

// AppendTailLines adds new lines to the tail buffer.
func (m *Model) AppendTailLines(lines []service.TailLine) {
	m.tailLines = append(m.tailLines, lines...)
	if len(m.tailLines) > 200 {
		m.tailLines = m.tailLines[len(m.tailLines)-200:]
	}
	// Auto-scroll to bottom if the user hasn't scrolled up
	if m.tailScroll == 0 {
		// tailScroll == 0 means "pinned to bottom" (no manual scroll offset)
	}
}

// SetTailError records a tail error message.
func (m *Model) SetTailError(err error) {
	m.tailStarting = false
	m.tailError = err.Error()
}

// SetTailStopped clears tail state.
func (m *Model) SetTailStopped() {
	m.tailActive = false
	m.tailStarting = false
	m.tailScroll = 0
}

// ClearTail resets all tail state (used on navigation away).
func (m *Model) ClearTail() {
	m.tailActive = false
	m.tailStarting = false
	m.tailLines = nil
	m.tailScroll = 0
	m.tailError = ""
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

// InDetailView returns true if the detail panel is in the detail drill-down view.
func (m Model) InDetailView() bool {
	return m.mode == viewDetail && m.detail != nil
}

// --- D1 SQL Console helpers ---

// InitD1Console initializes the D1 SQL console for a database.
func (m *Model) InitD1Console(databaseID string) tea.Cmd {
	m.d1Active = true
	m.d1DatabaseID = databaseID
	m.d1Output = nil
	m.d1Querying = false
	m.d1SchemaTables = nil
	m.d1SchemaErr = ""
	m.d1SchemaLoading = true

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
}

// SpinnerInit returns the command to start the spinner ticking.
func (m Model) SpinnerInit() tea.Cmd {
	return m.spinner.Tick
}

// IsLoading returns whether the detail panel is in a loading state (spinner should run).
func (m Model) IsLoading() bool {
	return m.loading || m.detailLoading || m.tailStarting || m.d1SchemaLoading || m.d1Querying
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
	// Handle mouse clicks for copy-on-click regardless of focus
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
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

	if !m.focused {
		return m, nil
	}

	// When D1 console is active, forward all messages to the textinput for cursor blink
	if m.d1Active && m.mode == viewDetail {
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
		// If tail is active, signal the app to stop it
		needStopTail := m.tailActive || m.tailStarting
		m.mode = viewList
		m.detail = nil
		m.detailErr = nil
		m.detailID = ""
		m.scrollOffset = 0
		m.ClearTail()
		if needStopTail {
			return m, func() tea.Msg { return TailStoppedMsg{} }
		}
		return m, nil
	case "t":
		// Only available in Workers detail view
		if m.service != "Workers" || m.detail == nil {
			return m, nil
		}
		if m.tailActive || m.tailStarting {
			// Stop tailing
			m.ClearTail()
			return m, func() tea.Msg { return TailStoppedMsg{} }
		}
		// Start tailing
		m.SetTailStarting()
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
		// Back to list, clear D1 state
		m.mode = viewList
		m.detail = nil
		m.detailErr = nil
		m.detailID = ""
		m.scrollOffset = 0
		m.ClearD1()
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

// View renders the detail panel.
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
	title := theme.TitleStyle.Render(fmt.Sprintf("  %s", d.Name)) + copyIcon()
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Track which allLines indices are copyable: index → raw text to copy
	copyLineMap := make(map[int]string)

	// Title is line 0, always copyable (resource name)
	copyLineMap[0] = d.Name

	var fieldLines []string
	for _, f := range d.Fields {
		label := theme.LabelStyle.Render(fmt.Sprintf("  %-16s", f.Label))
		value := theme.ValueStyle.Render(f.Value)
		line := fmt.Sprintf("%s %s", label, value)
		if isCopyableLabel(f.Label) {
			line += copyIcon()
			// Field lines start at allLines index 2 (title=0, sep=1)
			copyLineMap[2+len(fieldLines)] = f.Value
		}
		fieldLines = append(fieldLines, line)
	}

	// For Workers, split layout: fields on top, log console on bottom
	if m.service == "Workers" {
		return m.viewDetailWithTail(maxHeight, title, sep, fieldLines, copyLineMap)
	}

	// For D1, split layout: SQL console on left, schema on right
	if m.service == "D1" && m.d1Active {
		return m.viewDetailWithD1(maxHeight, title, sep, fieldLines, copyLineMap)
	}

	// Other services: original layout
	help := "\n" + theme.DimStyle.Render("  esc/backspace back  |  j/k scroll")

	allLines := []string{title, sep}
	allLines = append(allLines, fieldLines...)

	// Append ExtraContent (e.g. D1 schema diagram) if present
	if d.ExtraContent != "" {
		extraLines := strings.Split(d.ExtraContent, "\n")
		for _, el := range extraLines {
			allLines = append(allLines, theme.DimStyle.Render(el))
		}
	}

	allLines = append(allLines, help)

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

	// Register copy targets for visible lines only
	m.registerCopyTargets(copyLineMap, offset, endIdx)

	visible := allLines[offset:endIdx]

	return strings.Join(visible, "\n")
}

// viewDetailWithTail renders a split layout: detail fields on top, log console on bottom.
func (m Model) viewDetailWithTail(maxHeight int, title, sep string, fieldLines []string, copyLineMap map[int]string) string {
	// Calculate layout split: fields get ~60%, log console gets ~40%
	logConsoleHeight := maxHeight * 40 / 100
	if logConsoleHeight < 5 {
		logConsoleHeight = 5
	}
	fieldsHeight := maxHeight - logConsoleHeight
	if fieldsHeight < 3 {
		fieldsHeight = 3
	}

	// -- Upper region: detail fields --
	fieldContent := []string{title, sep}
	fieldContent = append(fieldContent, fieldLines...)

	// Apply scroll to fields
	visibleFieldsH := fieldsHeight
	if visibleFieldsH > len(fieldContent) {
		visibleFieldsH = len(fieldContent)
	}
	maxFieldScroll := len(fieldContent) - fieldsHeight
	if maxFieldScroll < 0 {
		maxFieldScroll = 0
	}
	offset := m.scrollOffset
	if offset > maxFieldScroll {
		offset = maxFieldScroll
	}
	endIdx := offset + fieldsHeight
	if endIdx > len(fieldContent) {
		endIdx = len(fieldContent)
	}
	visibleFields := fieldContent[offset:endIdx]

	// Register copy targets for visible field lines
	m.registerCopyTargets(copyLineMap, offset, endIdx)

	// Pad fields region to exact height
	for len(visibleFields) < fieldsHeight {
		visibleFields = append(visibleFields, "")
	}

	// -- Lower region: log console --
	logLines := m.renderLogConsole(logConsoleHeight)

	return strings.Join(visibleFields, "\n") + "\n" + strings.Join(logLines, "\n")
}

// viewDetailWithD1 renders the D1 detail layout:
// - Top: title + separator + compact metadata (2 rows)
// - Bottom: left (SQL console) | right (schema pane)
func (m Model) viewDetailWithD1(maxHeight int, title, sep string, fieldLines []string, copyLineMap map[int]string) string {
	// -- Top region: title + sep + compact metadata --
	// Render metadata as 2 compact rows instead of 7 individual lines
	topLines := []string{title, sep}
	topLines = append(topLines, m.renderD1CompactFields(copyLineMap)...)

	metaHeight := len(topLines)

	// Separator between metadata and the split pane
	panesSepWidth := m.width - 6
	if panesSepWidth < 0 {
		panesSepWidth = 0
	}
	panesSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", panesSepWidth)))
	topLines = append(topLines, panesSep)
	metaHeight++

	// -- Bottom region: left/right split --
	paneHeight := maxHeight - metaHeight
	if paneHeight < 5 {
		paneHeight = 5
	}

	// Calculate widths: ~50/50 split within the available detail panel width
	innerWidth := m.width - 4 // subtract border chars
	if innerWidth < 20 {
		innerWidth = 20
	}
	leftWidth := innerWidth / 2
	rightWidth := innerWidth - leftWidth - 1 // -1 for the vertical divider

	leftPane := m.renderD1SQLConsole(leftWidth, paneHeight)
	rightPane := m.renderD1SchemaPane(rightWidth, paneHeight)

	// Join left and right with a vertical divider
	divider := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render("│")
	splitPane := joinSideBySide(leftPane, rightPane, divider, leftWidth, paneHeight)

	// Register copy targets for the top metadata lines (always visible, no scroll)
	m.registerCopyTargets(copyLineMap, 0, len(topLines))

	return strings.Join(topLines, "\n") + "\n" + splitPane
}

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

// runeWidth returns the visible rune count of a string (approximate — doesn't strip ANSI).
// For our use case, lipgloss-styled strings have ANSI sequences, so we use lipgloss.Width.
func runeWidth(s string) int {
	return lipgloss.Width(s)
}

// renderLogConsole renders the tail log console region.
func (m Model) renderLogConsole(height int) []string {
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	consoleSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Header line
	var headerText string
	if m.tailActive {
		headerText = theme.LogConsoleHeaderStyle.Render("  ▸ Live Logs (tailing)")
	} else if m.tailStarting {
		headerText = fmt.Sprintf("  %s %s", m.spinner.View(), theme.DimStyle.Render("Connecting to tail..."))
	} else {
		headerText = theme.DimStyle.Render("  ▹ Live Logs")
	}

	// Help line at the bottom
	var helpText string
	if m.tailActive {
		helpText = theme.DimStyle.Render("  esc back  |  t stop tail  |  j/k scroll")
	} else {
		helpText = theme.DimStyle.Render("  esc back  |  t start tail  |  j/k scroll")
	}

	lines := []string{consoleSep, headerText}

	// Available lines for log content (minus sep, header, help)
	contentHeight := height - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	if m.tailError != "" {
		errLine := theme.ErrorStyle.Render(fmt.Sprintf("  Error: %s", m.tailError))
		lines = append(lines, errLine)
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, helpText)
		return lines
	}

	if !m.tailActive && !m.tailStarting {
		hint := theme.DimStyle.Render("  Press t to start tailing logs")
		lines = append(lines, hint)
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, helpText)
		return lines
	}

	if len(m.tailLines) == 0 {
		waiting := theme.DimStyle.Render("  Waiting for log events...")
		lines = append(lines, waiting)
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, helpText)
		return lines
	}

	// Render log lines with level-based coloring
	var logRendered []string
	for _, tl := range m.tailLines {
		ts := theme.LogTimestampStyle.Render(tl.Timestamp.Format(time.TimeOnly))
		text := m.styleTailLine(tl)
		logRendered = append(logRendered, fmt.Sprintf("  %s %s", ts, text))
	}

	// Show the most recent lines that fit, respecting tailScroll
	totalLogLines := len(logRendered)
	// tailScroll == 0 means pinned to bottom (show most recent)
	startLine := totalLogLines - contentHeight - m.tailScroll
	if startLine < 0 {
		startLine = 0
	}
	endLine := startLine + contentHeight
	if endLine > totalLogLines {
		endLine = totalLogLines
	}

	visible := logRendered[startLine:endLine]
	lines = append(lines, visible...)

	// Pad to fill remaining space
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, helpText)

	return lines
}

// styleTailLine applies level-based coloring to a tail line's text.
func (m Model) styleTailLine(tl service.TailLine) string {
	switch tl.Level {
	case "warn":
		return theme.LogLevelWarn.Render(tl.Text)
	case "error", "exception":
		return theme.LogLevelError.Render(tl.Text)
	case "request":
		return theme.LogLevelRequest.Render(tl.Text)
	case "system":
		return theme.LogLevelSystem.Render(tl.Text)
	default: // "log", "info"
		return theme.LogLevelLog.Render(tl.Text)
	}
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
