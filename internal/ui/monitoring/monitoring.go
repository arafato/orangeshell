// Package monitoring implements the Monitoring tab — a dual-pane view with a
// worker tree browser on the left (~20%) and a live tail grid on the right (~80%).
package monitoring

import (
	"math"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/api"
	svc "github.com/oarafat/orangeshell/internal/service"
)

// --- Model ---

// Model holds all monitoring tab state.
type Model struct {
	// Worker tree (left pane)
	workerTree  []WorkerTreeEntry
	treeCursor  int // cursor position in workerTree (skips headers)
	treeScrollY int // vertical scroll offset for tree

	// Live tail grid (right pane)
	gridPanes   []TailPane
	gridCursor  int // which pane is focused (index into gridPanes)
	gridScrollY int // vertical scroll offset for grid rows

	// Analytics view (replaces grid when active)
	showAnalytics bool
	analyticsView AnalyticsModel

	// Focus
	focusPane FocusPane

	// Legacy single-tail state (for backward compat with t-from-Operations)
	singleScript string // script name if started via StartSingleTail
	singleActive bool   // true if the single tail was started (not from tree)

	// Dimensions
	width  int
	height int

	// Export state (set by app layer, rendered in view)
	exportActive bool
}

// New creates an empty monitoring model.
func New() Model {
	return Model{}
}

// SetSize updates the available dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
	if m.showAnalytics {
		m.analyticsView.SetSize(m.analyticsRightWidth(), h)
	}
}

// --- Worker tree API ---

// SetWorkerTree sets the worker tree data for the left pane.
// Called by the app layer when switching to the Monitoring tab.
func (m *Model) SetWorkerTree(tree []WorkerTreeEntry) {
	m.workerTree = tree
	// Reset cursor if out of range
	if m.treeCursor >= len(tree) {
		m.treeCursor = 0
	}
	// Ensure cursor is on a selectable item (not a header)
	m.advanceCursorToSelectable(1)
}

// HasWorkerTree returns true if a worker tree is populated.
func (m Model) HasWorkerTree() bool {
	return len(m.workerTree) > 0
}

// CursorOnDev returns true if the tree cursor is on a dev-mode worker entry.
func (m Model) CursorOnDev() bool {
	if m.treeCursor >= 0 && m.treeCursor < len(m.workerTree) {
		e := m.workerTree[m.treeCursor]
		return e.IsDev && !e.IsHeader
	}
	return false
}

// Focus returns which pane currently has keyboard focus.
func (m Model) Focus() FocusPane {
	return m.focusPane
}

// SetFocusLeft switches keyboard focus to the left (worker tree) pane.
func (m *Model) SetFocusLeft() {
	m.focusPane = FocusLeft
}

// SetExportActive sets whether the log exporter is running (for view rendering).
func (m *Model) SetExportActive(active bool) {
	m.exportActive = active
}

// ExportActive returns whether the log exporter is running.
func (m Model) ExportActive() bool {
	return m.exportActive
}

// ShowAnalytics returns whether the analytics view is active.
func (m Model) ShowAnalytics() bool {
	return m.showAnalytics
}

// AnalyticsScript returns the script name being analyzed (empty if analytics is not shown).
func (m Model) AnalyticsScript() string {
	if m.showAnalytics {
		return m.analyticsView.ScriptName()
	}
	return ""
}

// OpenAnalytics switches the right pane to the analytics view for the given script.
func (m *Model) OpenAnalytics(scriptName string) {
	m.showAnalytics = true
	m.analyticsView = NewAnalytics(scriptName)
	m.analyticsView.SetSize(m.analyticsRightWidth(), m.height)
	m.analyticsView.SetLoading()
}

// CloseAnalytics switches back to the grid view.
func (m *Model) CloseAnalytics() {
	m.showAnalytics = false
}

// SetAnalyticsMetrics stores fetched analytics data.
func (m *Model) SetAnalyticsMetrics(metrics *api.WorkerMetrics) {
	m.analyticsView.SetMetrics(metrics)
}

// SetAnalyticsError records a fetch error in the analytics view.
func (m *Model) SetAnalyticsError(err error) {
	m.analyticsView.SetError(err)
}

// AnalyticsTimeRangeLabel returns the current time range label of the analytics view.
func (m Model) AnalyticsTimeRangeLabel() string {
	if m.showAnalytics {
		return m.analyticsView.TimeRangeLabel()
	}
	return ""
}

// analyticsRightWidth computes the right pane width for analytics.
func (m Model) analyticsRightWidth() int {
	leftWidth := m.width * leftPaneRatio / 100
	if leftWidth < 12 {
		leftWidth = 12
	}
	rightWidth := m.width - leftWidth - 1
	if rightWidth < 10 {
		rightWidth = 10
	}
	return rightWidth
}

// --- Grid API (called by app layer to route tail data) ---

// AddToGrid adds a worker to the tail grid. Does not start a session — that's
// signaled back to the app via TailAddMsg from Update().
func (m *Model) AddToGrid(scriptName, url string) {
	// Check if already in grid
	for _, p := range m.gridPanes {
		if p.ScriptName == scriptName {
			return
		}
	}
	m.gridPanes = append(m.gridPanes, TailPane{
		ScriptName: scriptName,
		URL:        url,
		Connecting: true,
		Active:     true,
	})
}

// AddDevToGrid adds a dev-mode worker to the tail grid.
// Dev panes are immediately "connected" since output comes from the process pipe,
// not from a Cloudflare WebSocket tail.
func (m *Model) AddDevToGrid(scriptName, devKind string) {
	for _, p := range m.gridPanes {
		if p.ScriptName == scriptName {
			return
		}
	}
	m.gridPanes = append(m.gridPanes, TailPane{
		ScriptName: scriptName,
		Active:     true,
		Connected:  true, // no WebSocket connect phase for dev panes
		IsDev:      true,
		DevKind:    devKind,
	})
}

// RemoveFromGrid removes a worker from the tail grid.
func (m *Model) RemoveFromGrid(scriptName string) {
	for i, p := range m.gridPanes {
		if p.ScriptName == scriptName {
			m.gridPanes = append(m.gridPanes[:i], m.gridPanes[i+1:]...)
			// Adjust grid cursor
			if m.gridCursor >= len(m.gridPanes) {
				m.gridCursor = len(m.gridPanes) - 1
			}
			if m.gridCursor < 0 {
				m.gridCursor = 0
			}
			return
		}
	}
}

// IsInGrid returns whether a script is in the live grid.
func (m Model) IsInGrid(scriptName string) bool {
	for _, p := range m.gridPanes {
		if p.ScriptName == scriptName {
			return true
		}
	}
	return false
}

// GridSetConnected marks a grid pane as connected.
func (m *Model) GridSetConnected(scriptName string) {
	for i := range m.gridPanes {
		if m.gridPanes[i].ScriptName == scriptName {
			m.gridPanes[i].Connecting = false
			m.gridPanes[i].Connected = true
			m.gridPanes[i].Active = true
			return
		}
	}
}

// GridSetSessionID records the tail session ID for a grid pane.
func (m *Model) GridSetSessionID(scriptName, sessionID string) {
	for i := range m.gridPanes {
		if m.gridPanes[i].ScriptName == scriptName {
			m.gridPanes[i].SessionID = sessionID
			return
		}
	}
}

// GridAppendLines routes lines to the correct grid pane.
func (m *Model) GridAppendLines(scriptName string, lines []svc.TailLine) {
	for i := range m.gridPanes {
		if m.gridPanes[i].ScriptName == scriptName {
			m.gridPanes[i].appendLines(lines)
			return
		}
	}
}

// GridSetError marks a grid pane as errored.
func (m *Model) GridSetError(scriptName string, err error) {
	for i := range m.gridPanes {
		if m.gridPanes[i].ScriptName == scriptName {
			m.gridPanes[i].Connecting = false
			m.gridPanes[i].Error = err.Error()
			return
		}
	}
}

// GridSetStopped marks a grid pane as stopped (but keeps it in the grid).
func (m *Model) GridSetStopped(scriptName string) {
	for i := range m.gridPanes {
		if m.gridPanes[i].ScriptName == scriptName {
			m.gridPanes[i].Active = false
			m.gridPanes[i].Connected = false
			m.gridPanes[i].Connecting = false
			return
		}
	}
}

// GridPaneCount returns the number of panes in the grid.
func (m Model) GridPaneCount() int {
	return len(m.gridPanes)
}

// GridPaneScripts returns the script names of all active grid panes.
func (m Model) GridPaneScripts() []string {
	var names []string
	for _, p := range m.gridPanes {
		if p.Active {
			names = append(names, p.ScriptName)
		}
	}
	return names
}

// AllGridPaneScripts returns the script names of all grid panes (active or not).
func (m Model) AllGridPaneScripts() []string {
	var names []string
	for _, p := range m.gridPanes {
		names = append(names, p.ScriptName)
	}
	return names
}

// GridPanes returns info about all grid panes (for the AI context panel).
func (m Model) GridPanes() []GridPaneInfo {
	result := make([]GridPaneInfo, len(m.gridPanes))
	for i, p := range m.gridPanes {
		// Copy lines to avoid data races
		lines := make([]svc.TailLine, len(p.Lines))
		copy(lines, p.Lines)
		result[i] = GridPaneInfo{
			ScriptName: p.ScriptName,
			IsDev:      p.IsDev,
			DevKind:    p.DevKind,
			Active:     p.Active || p.Connected || p.Connecting,
			LineCount:  len(p.Lines),
			Lines:      lines,
		}
	}
	return result
}

// --- Backward-compat API (used by app layer) ---

// StartSingleTail adds a worker to the grid and focuses it (backward compat).
// Called when the user presses t on a worker from Operations/Resources tab.
func (m *Model) StartSingleTail(scriptName string) {
	m.singleScript = scriptName
	m.singleActive = true
	m.AddToGrid(scriptName, "")
	m.focusPane = FocusRight
	// Focus the newly added pane
	for i, p := range m.gridPanes {
		if p.ScriptName == scriptName {
			m.gridCursor = i
			break
		}
	}
}

// SetTailConnected marks the single tail as connected (routes to grid).
func (m *Model) SetTailConnected() {
	if m.singleScript != "" {
		m.GridSetConnected(m.singleScript)
	}
}

// AppendTailLines adds lines to the single-tail buffer (routes to grid).
func (m *Model) AppendTailLines(lines []svc.TailLine) {
	if m.singleScript != "" {
		m.GridAppendLines(m.singleScript, lines)
	}
}

// SetTailError records a tail error (routes to grid).
func (m *Model) SetTailError(err error) {
	if m.singleScript != "" {
		m.GridSetError(m.singleScript, err)
	}
}

// SetTailStopped marks the single tail as stopped.
func (m *Model) SetTailStopped() {
	if m.singleScript != "" {
		m.GridSetStopped(m.singleScript)
	}
	m.singleActive = false
}

// ClearSingleTail resets single-tail state.
func (m *Model) ClearSingleTail() {
	if m.singleScript != "" {
		m.RemoveFromGrid(m.singleScript)
	}
	m.singleScript = ""
	m.singleActive = false
}

// SingleTailActive returns whether a single tail is actively connected.
func (m Model) SingleTailActive() bool {
	if m.singleScript == "" {
		return false
	}
	for _, p := range m.gridPanes {
		if p.ScriptName == m.singleScript {
			return p.Connected && p.Active
		}
	}
	return false
}

// SingleTailStarting returns whether a single tail is waiting for connection.
func (m Model) SingleTailStarting() bool {
	if m.singleScript == "" {
		return false
	}
	for _, p := range m.gridPanes {
		if p.ScriptName == m.singleScript {
			return p.Connecting
		}
	}
	return false
}

// ScriptName returns the script being tailed (single mode, backward compat).
func (m Model) ScriptName() string {
	return m.singleScript
}

// CurrentMode returns the monitoring mode for external queries (backward compat).
func (m Model) CurrentMode() Mode {
	if len(m.gridPanes) == 0 {
		return ModeIdle
	}
	if len(m.gridPanes) == 1 && m.singleActive {
		return ModeSingle
	}
	return ModeParallel
}

// IsActive returns whether any tail session is active or a grid is populated.
func (m Model) IsActive() bool {
	return len(m.gridPanes) > 0
}

// IsParallelTailActive returns whether the grid has multiple active panes.
func (m Model) IsParallelTailActive() bool {
	return len(m.gridPanes) > 1
}

// --- Backward-compat parallel API (routes to grid) ---

// StartParallelTail initializes the grid for parallel multi-worker tailing.
func (m *Model) StartParallelTail(envName string, targets []ParallelTailTarget) {
	m.gridPanes = make([]TailPane, len(targets))
	for i, t := range targets {
		m.gridPanes[i] = TailPane{
			ScriptName: t.ScriptName,
			URL:        t.URL,
			Connecting: true,
			Active:     true,
		}
	}
	m.gridCursor = 0
	m.gridScrollY = 0
	m.focusPane = FocusRight
	m.singleScript = ""
	m.singleActive = false
}

// StopParallelTail clears all grid panes.
func (m *Model) StopParallelTail() {
	m.gridPanes = nil
	m.gridCursor = 0
	m.gridScrollY = 0
}

// ParallelTailAppendLines routes lines to the correct grid pane.
func (m *Model) ParallelTailAppendLines(scriptName string, lines []svc.TailLine) {
	m.GridAppendLines(scriptName, lines)
}

// ParallelTailSetConnected marks a grid pane as connected.
func (m *Model) ParallelTailSetConnected(scriptName string) {
	m.GridSetConnected(scriptName)
}

// ParallelTailSetError marks a grid pane as errored.
func (m *Model) ParallelTailSetError(scriptName string, err error) {
	m.GridSetError(scriptName, err)
}

// ParallelTailSetSessionID records the tail session ID for a grid pane.
func (m *Model) ParallelTailSetSessionID(scriptName, sessionID string) {
	m.GridSetSessionID(scriptName, sessionID)
}

// EnvName returns the environment being tailed (backward compat).
func (m Model) EnvName() string { return "" }

// PaneCount returns the number of grid panes (backward compat).
func (m Model) PaneCount() int { return len(m.gridPanes) }

// --- Clear ---

// Clear resets the entire monitoring model.
func (m *Model) Clear() {
	m.gridPanes = nil
	m.gridCursor = 0
	m.gridScrollY = 0
	m.singleScript = ""
	m.singleActive = false
	m.focusPane = FocusLeft
	// Preserve workerTree — it's rebuilt on tab switch
}

// --- Update ---

// Update handles key events for the monitoring tab.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	// Route auto-refresh ticks to analytics model
	if _, ok := msg.(autoRefreshTickMsg); ok && m.showAnalytics {
		var cmd tea.Cmd
		m.analyticsView, cmd = m.analyticsView.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// If analytics view is active, route all keys there
		if m.showAnalytics {
			var cmd tea.Cmd
			m.analyticsView, cmd = m.analyticsView.Update(msg)
			return m, cmd
		}

		// Tab switches focus between panes
		if msg.String() == "tab" {
			if m.focusPane == FocusLeft {
				m.focusPane = FocusRight
			} else {
				m.focusPane = FocusLeft
			}
			return m, nil
		}

		switch m.focusPane {
		case FocusLeft:
			return m.updateLeftPane(msg)
		case FocusRight:
			return m.updateRightPane(msg)
		}
	}
	return m, nil
}

func (m Model) updateLeftPane(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.moveCursorDown()
	case "k", "up":
		m.moveCursorUp()
	case " ":
		// Toggle focused worker in/out of the grid
		if m.treeCursor >= 0 && m.treeCursor < len(m.workerTree) {
			entry := m.workerTree[m.treeCursor]
			if !entry.IsHeader && entry.ScriptName != "" {
				if m.IsInGrid(entry.ScriptName) {
					return m, func() tea.Msg {
						return TailRemoveMsg{ScriptName: entry.ScriptName}
					}
				}
				return m, func() tea.Msg {
					return TailAddMsg{ScriptName: entry.ScriptName}
				}
			}
		}
	case "a":
		// Open analytics view for focused worker
		if m.treeCursor >= 0 && m.treeCursor < len(m.workerTree) {
			entry := m.workerTree[m.treeCursor]
			if !entry.IsHeader && entry.ScriptName != "" && !entry.IsDev {
				// Strip "dev:" prefix if present (shouldn't be for non-dev, but just in case)
				scriptName := entry.ScriptName
				return m, func() tea.Msg {
					return AnalyticsRequestMsg{ScriptName: scriptName, TimeRangeIndex: 2} // default 24h
				}
			}
		}
	case "c":
		// Trigger cron handler on focused dev worker
		if m.treeCursor >= 0 && m.treeCursor < len(m.workerTree) {
			entry := m.workerTree[m.treeCursor]
			if entry.IsDev && !entry.IsHeader && entry.ScriptName != "" {
				return m, func() tea.Msg {
					return DevCronTriggerMsg{ScriptName: entry.ScriptName}
				}
			}
		}
	}
	return m, nil
}

func (m Model) updateRightPane(msg tea.KeyMsg) (Model, tea.Cmd) {
	if len(m.gridPanes) == 0 {
		return m, nil
	}

	switch msg.String() {
	case "j", "down":
		// Move grid cursor down (next row)
		next := m.gridCursor + gridCols
		if next < len(m.gridPanes) {
			m.gridCursor = next
			m.adjustGridScroll()
		}
	case "k", "up":
		// Move grid cursor up (prev row)
		prev := m.gridCursor - gridCols
		if prev >= 0 {
			m.gridCursor = prev
			m.adjustGridScroll()
		}
	case "h", "left":
		if m.gridCursor > 0 {
			m.gridCursor--
			m.adjustGridScroll()
		}
	case "l", "right":
		if m.gridCursor < len(m.gridPanes)-1 {
			m.gridCursor++
			m.adjustGridScroll()
		}
	case "t":
		// Toggle tail for focused pane
		pane := m.gridPanes[m.gridCursor]
		return m, func() tea.Msg {
			return TailToggleMsg{ScriptName: pane.ScriptName, Start: !pane.Active}
		}
	case "ctrl+t":
		// Toggle all panes: if any are active, stop all; otherwise start all
		anyActive := false
		for _, p := range m.gridPanes {
			if p.Active {
				anyActive = true
				break
			}
		}
		return m, func() tea.Msg {
			return TailToggleAllMsg{Start: !anyActive}
		}
	}
	return m, nil
}

// --- Cursor helpers ---

func (m *Model) moveCursorDown() {
	for i := m.treeCursor + 1; i < len(m.workerTree); i++ {
		if !m.workerTree[i].IsHeader {
			m.treeCursor = i
			m.adjustTreeScroll()
			return
		}
	}
}

func (m *Model) moveCursorUp() {
	for i := m.treeCursor - 1; i >= 0; i-- {
		if !m.workerTree[i].IsHeader {
			m.treeCursor = i
			m.adjustTreeScroll()
			return
		}
	}
}

func (m *Model) advanceCursorToSelectable(direction int) {
	if len(m.workerTree) == 0 {
		return
	}
	if m.treeCursor < 0 {
		m.treeCursor = 0
	}
	if m.treeCursor >= len(m.workerTree) {
		m.treeCursor = len(m.workerTree) - 1
	}
	if !m.workerTree[m.treeCursor].IsHeader {
		return
	}
	// Search in the given direction for a non-header
	if direction >= 0 {
		for i := m.treeCursor; i < len(m.workerTree); i++ {
			if !m.workerTree[i].IsHeader {
				m.treeCursor = i
				return
			}
		}
	}
	for i := m.treeCursor; i >= 0; i-- {
		if !m.workerTree[i].IsHeader {
			m.treeCursor = i
			return
		}
	}
}

func (m *Model) adjustTreeScroll() {
	leftHeight := m.height - 2 // borders
	if leftHeight < 1 {
		leftHeight = 1
	}
	if m.treeCursor < m.treeScrollY {
		m.treeScrollY = m.treeCursor
	}
	if m.treeCursor >= m.treeScrollY+leftHeight {
		m.treeScrollY = m.treeCursor - leftHeight + 1
	}
}

func (m *Model) adjustGridScroll() {
	row := m.gridCursor / gridCols
	gridHeight := m.height - 4
	if gridHeight < gridMinPaneH {
		gridHeight = gridMinPaneH
	}
	visibleRows := gridHeight / gridMinPaneH
	if visibleRows < 1 {
		visibleRows = 1
	}
	if row < m.gridScrollY {
		m.gridScrollY = row
	}
	if row >= m.gridScrollY+visibleRows {
		m.gridScrollY = row - visibleRows + 1
	}
}

func (m Model) totalGridRows() int {
	return int(math.Ceil(float64(len(m.gridPanes)) / float64(gridCols)))
}
