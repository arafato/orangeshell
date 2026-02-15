// Package monitoring implements the Monitoring tab — a dual-pane view with a
// worker tree browser on the left (~20%) and a live tail grid on the right (~80%).
package monitoring

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Constants ---

const (
	gridMaxLines   = 200 // max log lines retained per grid pane
	gridMinPaneH   = 6   // minimum grid pane height
	gridCols       = 2   // fixed number of columns in the tail grid
	leftPaneRatio  = 20  // left pane gets ~20% of width
	rightPaneRatio = 80  // right pane gets ~80% of width
)

// --- Focus ---

// FocusPane identifies which pane has keyboard focus.
type FocusPane int

const (
	FocusLeft  FocusPane = iota // Worker tree browser
	FocusRight                  // Live tail grid
)

// --- Messages emitted by the monitoring model ---

// TailAddMsg requests the app to start tailing a worker and add it to the grid.
type TailAddMsg struct {
	ScriptName string
}

// TailRemoveMsg requests the app to stop tailing a worker and remove it from the grid.
type TailRemoveMsg struct {
	ScriptName string
}

// TailToggleMsg requests the app to toggle tailing for a specific grid pane.
type TailToggleMsg struct {
	ScriptName string
	Start      bool // true = start, false = stop
}

// TailToggleAllMsg requests the app to toggle all grid panes on or off.
type TailToggleAllMsg struct {
	Start bool // true = start all, false = stop all
}

// DevCronTriggerMsg requests the app to trigger a cron handler on a dev worker
// via the /cdn-cgi/handler/scheduled endpoint.
type DevCronTriggerMsg struct {
	ScriptName string // dev:worker-name
}

// TailStopMsg requests the app to stop the active single tail session (backward compat).
type TailStopMsg struct{}

// ParallelTailStopMsg requests the app to stop all parallel tail sessions (backward compat).
type ParallelTailStopMsg struct{}

// --- Worker tree ---

// WorkerTreeEntry represents a single item in the left-pane worker tree.
type WorkerTreeEntry struct {
	ProjectName string // wrangler project name (e.g. "express-d1-app")
	ScriptName  string // resolved worker name (empty for headers)
	EnvName     string // environment name (e.g. "default", "staging")
	IsHeader    bool   // true for project group headers (non-selectable)
	IsDev       bool   // true for dev-mode entries (wrangler dev sessions)
	DevKind     string // "local" or "remote" (only set when IsDev is true)
	DevPort     string // e.g. "8787" — extracted from wrangler dev output
}

// --- Tail pane (grid) ---

// TailPane holds per-worker tail state in the live grid.
type TailPane struct {
	ScriptName string
	URL        string
	Lines      []svc.TailLine
	Connecting bool
	Connected  bool
	Active     bool // true if tail is running (or starting)
	Error      string
	SessionID  string
	IsDev      bool   // true for dev-mode panes (wrangler dev output)
	DevKind    string // "local" or "remote"
}

func (p *TailPane) appendLines(lines []svc.TailLine) {
	p.Lines = append(p.Lines, lines...)
	if len(p.Lines) > gridMaxLines {
		p.Lines = p.Lines[len(p.Lines)-gridMaxLines:]
	}
}

// ParallelTailTarget identifies a worker to tail (used by app layer).
type ParallelTailTarget struct {
	ScriptName string
	URL        string
}

// --- Backward-compat Mode type (still used by app layer queries) ---

// Mode describes the monitoring state for external queries.
type Mode int

const (
	ModeIdle     Mode = iota // No active tailing
	ModeSingle               // Single worker being tailed (via grid)
	ModeParallel             // Multiple workers being tailed (via grid)
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

	// Focus
	focusPane FocusPane

	// Legacy single-tail state (for backward compat with t-from-Operations)
	singleScript string // script name if started via StartSingleTail
	singleActive bool   // true if the single tail was started (not from tree)

	// Dimensions
	width  int
	height int
}

// New creates an empty monitoring model.
func New() Model {
	return Model{}
}

// SetSize updates the available dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
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

// GridPaneInfo holds exportable information about a grid pane for use by the AI context panel.
type GridPaneInfo struct {
	ScriptName string
	IsDev      bool
	DevKind    string // "local" or "remote" (only set for dev panes)
	Active     bool
	LineCount  int
	Lines      []svc.TailLine // copy of the log lines
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
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
	case "a":
		// Add focused worker to the grid and start tailing
		if m.treeCursor >= 0 && m.treeCursor < len(m.workerTree) {
			entry := m.workerTree[m.treeCursor]
			if !entry.IsHeader && entry.ScriptName != "" && !m.IsInGrid(entry.ScriptName) {
				return m, func() tea.Msg {
					return TailAddMsg{ScriptName: entry.ScriptName}
				}
			}
		}
	case "d":
		// Remove focused worker from the grid and stop tailing
		if m.treeCursor >= 0 && m.treeCursor < len(m.workerTree) {
			entry := m.workerTree[m.treeCursor]
			if !entry.IsHeader && entry.ScriptName != "" && m.IsInGrid(entry.ScriptName) {
				return m, func() tea.Msg {
					return TailRemoveMsg{ScriptName: entry.ScriptName}
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

// --- View ---

// View renders the monitoring tab content.
func (m Model) View() string {
	if !m.HasWorkerTree() && len(m.gridPanes) == 0 {
		return m.viewEmpty()
	}
	return m.viewDualPane()
}

func (m Model) viewEmpty() string {
	contentHeight := m.height
	if contentHeight < 1 {
		contentHeight = 1
	}
	hint := theme.DimStyle.Render("  No workers available. Open a project in Operations first.")
	lines := []string{"", hint}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines[:contentHeight], "\n")
}

func (m Model) viewDualPane() string {
	if m.width < 20 || m.height < 3 {
		return ""
	}

	leftWidth := m.width * leftPaneRatio / 100
	if leftWidth < 12 {
		leftWidth = 12
	}
	rightWidth := m.width - leftWidth - 1 // 1 for the vertical separator
	if rightWidth < 10 {
		rightWidth = 10
	}

	contentHeight := m.height
	if contentHeight < 1 {
		contentHeight = 1
	}

	leftView := m.viewWorkerTree(leftWidth, contentHeight)
	rightView := m.viewTailGrid(rightWidth, contentHeight)

	// Vertical separator
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorDarkGray)
	var sepLines []string
	for i := 0; i < contentHeight; i++ {
		sepLines = append(sepLines, sepStyle.Render("│"))
	}
	separator := strings.Join(sepLines, "\n")

	result := lipgloss.JoinHorizontal(lipgloss.Top, leftView, separator, rightView)

	// Truncate to exact contentHeight to prevent overflow from lipgloss padding
	lines := strings.Split(result, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	return strings.Join(lines, "\n")
}

// --- Left pane: Worker tree ---

func (m Model) viewWorkerTree(width, height int) string {
	innerWidth := width - 2 // padding

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorWhite)
	title := " " + titleStyle.Render("Workers")

	var lines []string
	lines = append(lines, title)

	// Render tree entries
	for i, entry := range m.workerTree {
		if entry.IsHeader {
			if entry.IsDev {
				// Dev mode section: dashed separator + yellow header
				sepLine := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).
					Render(strings.Repeat("─", innerWidth))
				lines = append(lines, " "+sepLine)
				devHeaderStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorYellow)
				lines = append(lines, " "+devHeaderStyle.Render(truncateStr(entry.ProjectName, innerWidth-1)))
			} else {
				// Project group header
				headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorOrange)
				line := " " + headerStyle.Render(truncateStr(entry.ProjectName, innerWidth-1))
				lines = append(lines, line)
			}
		} else if entry.IsDev {
			// Dev worker item with badge
			cursor := "  "
			nameStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
			if i == m.treeCursor && m.focusPane == FocusLeft {
				cursor = lipgloss.NewStyle().Foreground(theme.ColorYellow).Bold(true).Render("> ")
				nameStyle = lipgloss.NewStyle().Foreground(theme.ColorYellow).Bold(true)
			}

			// Grid indicator
			indicator := " "
			if m.IsInGrid(entry.ScriptName) {
				indicator = lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("●")
			}

			// Display name (strip "dev:" prefix for display)
			displayName := strings.TrimPrefix(entry.ScriptName, "dev:")
			name := truncateStr(displayName, innerWidth-18) // room for badge + port

			// Dev badge
			badge := "[dev]"
			if entry.DevKind == "remote" {
				badge = "[dev-remote]"
			}
			badgeStyle := lipgloss.NewStyle().Foreground(theme.ColorYellow)

			// Port suffix
			portSuffix := ""
			if entry.DevPort != "" {
				portSuffix = " " + theme.DimStyle.Render(":"+entry.DevPort)
			}

			line := fmt.Sprintf(" %s%s %s %s%s", cursor, indicator,
				nameStyle.Render(name), badgeStyle.Render(badge), portSuffix)
			lines = append(lines, line)
		} else {
			// Regular worker item
			cursor := "  "
			nameStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
			if i == m.treeCursor && m.focusPane == FocusLeft {
				cursor = lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render("> ")
				nameStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true)
			}

			// Grid indicator
			indicator := " "
			if m.IsInGrid(entry.ScriptName) {
				indicator = lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("●")
			}

			name := truncateStr(entry.ScriptName, innerWidth-5)
			envSuffix := ""
			if entry.EnvName != "" && entry.EnvName != "default" {
				envSuffix = " " + theme.DimStyle.Render(fmt.Sprintf("[%s]", entry.EnvName))
			}

			line := fmt.Sprintf(" %s%s %s%s", cursor, indicator, nameStyle.Render(name), envSuffix)
			lines = append(lines, line)
		}
	}

	// Apply scroll
	visibleHeight := height - 1 // title
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	treeLines := lines[1:] // skip title
	scrollY := m.treeScrollY
	if scrollY > len(treeLines)-visibleHeight {
		scrollY = len(treeLines) - visibleHeight
	}
	if scrollY < 0 {
		scrollY = 0
	}
	endIdx := scrollY + visibleHeight
	if endIdx > len(treeLines) {
		endIdx = len(treeLines)
	}
	visible := treeLines[scrollY:endIdx]

	var result []string
	result = append(result, lines[0]) // title
	result = append(result, visible...)

	// Pad to exact height
	for len(result) < height {
		result = append(result, "")
	}
	if len(result) > height {
		result = result[:height]
	}

	// Apply width
	styled := lipgloss.NewStyle().Width(width)
	var output []string
	for _, line := range result {
		output = append(output, styled.Render(line))
	}
	return strings.Join(output, "\n")
}

// --- Right pane: Tail grid ---

func (m Model) viewTailGrid(width, height int) string {
	if len(m.gridPanes) == 0 {
		return m.viewGridEmpty(width, height)
	}

	// Title
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorWhite)
	activeCount := 0
	for _, p := range m.gridPanes {
		if p.Active {
			activeCount++
		}
	}
	title := fmt.Sprintf(" %s  %s",
		titleStyle.Render("Live Tail"),
		theme.DimStyle.Render(fmt.Sprintf("%d/%d active", activeCount, len(m.gridPanes))))

	// Grid layout
	totalRows := m.totalGridRows()
	colWidth := width / gridCols
	if colWidth < 10 {
		colWidth = 10
	}

	headerLines := 1 // title
	gridHeight := height - headerLines
	if gridHeight < gridMinPaneH {
		gridHeight = gridMinPaneH
	}

	visibleRows := gridHeight / gridMinPaneH
	if visibleRows < 1 {
		visibleRows = 1
	}
	if visibleRows > totalRows {
		visibleRows = totalRows
	}

	paneHeight := gridHeight / visibleRows
	if paneHeight < gridMinPaneH {
		paneHeight = gridMinPaneH
	}

	maxScroll := totalRows - visibleRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	scrollY := m.gridScrollY
	if scrollY > maxScroll {
		scrollY = maxScroll
	}

	// Build visible rows
	var rowViews []string
	for row := scrollY; row < scrollY+visibleRows && row < totalRows; row++ {
		leftIdx := row * gridCols
		rightIdx := leftIdx + 1

		leftFocused := leftIdx == m.gridCursor && m.focusPane == FocusRight
		leftView := m.renderGridPane(&m.gridPanes[leftIdx], colWidth, paneHeight, leftFocused)

		var rightView string
		if rightIdx < len(m.gridPanes) {
			rightFocused := rightIdx == m.gridCursor && m.focusPane == FocusRight
			rightView = m.renderGridPane(&m.gridPanes[rightIdx], colWidth, paneHeight, rightFocused)
		} else {
			rightView = strings.Repeat("\n", paneHeight-1)
			rightView = lipgloss.NewStyle().Width(colWidth).Render(rightView)
		}

		rowView := lipgloss.JoinHorizontal(lipgloss.Top, leftView, rightView)
		rowViews = append(rowViews, rowView)
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)

	var allLines []string
	allLines = append(allLines, title)

	gridLines := strings.Split(grid, "\n")
	allLines = append(allLines, gridLines...)

	// Truncate/pad to exact height
	if len(allLines) > height {
		allLines = allLines[:height]
	}
	for len(allLines) < height {
		allLines = append(allLines, "")
	}

	return strings.Join(allLines, "\n")
}

func (m Model) viewGridEmpty(width, height int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorWhite)
	title := " " + titleStyle.Render("Live Tail")
	hint := " " + theme.DimStyle.Render("Select a worker and press a to start tailing.")

	lines := []string{title, "", hint}
	for len(lines) < height {
		lines = append(lines, "")
	}
	styled := lipgloss.NewStyle().Width(width)
	var output []string
	for _, line := range lines[:height] {
		output = append(output, styled.Render(line))
	}
	return strings.Join(output, "\n")
}

func (m Model) renderGridPane(pane *TailPane, width, height int, focused bool) string {
	if height < 1 {
		return ""
	}

	innerWidth := width - 4 // border + padding

	// Header with status indicator
	var statusIcon string
	if pane.Active && pane.Connected {
		statusIcon = lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("●")
	} else if pane.Active && pane.Connecting {
		statusIcon = lipgloss.NewStyle().Foreground(theme.ColorYellow).Render("◌")
	} else if pane.Error != "" {
		statusIcon = lipgloss.NewStyle().Foreground(theme.ColorRed).Render("✕")
	} else {
		statusIcon = lipgloss.NewStyle().Foreground(theme.ColorGray).Render("○")
	}

	// Header: name + optional dev badge
	nameColor := theme.ColorOrange
	if pane.IsDev {
		nameColor = theme.ColorYellow
	}
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(nameColor)

	// Display name (strip "dev:" prefix for dev panes)
	displayName := pane.ScriptName
	if pane.IsDev {
		displayName = strings.TrimPrefix(displayName, "dev:")
	}

	header := fmt.Sprintf(" %s %s", statusIcon, nameStyle.Render(truncateStr(displayName, innerWidth-3)))

	// Dev badge (LOCAL/REMOTE pill)
	if pane.IsDev {
		badgeText := "LOCAL"
		if pane.DevKind == "remote" {
			badgeText = "REMOTE"
		}
		badgeStyle := lipgloss.NewStyle().Foreground(theme.ColorYellow).Bold(true)
		header += " " + badgeStyle.Render(badgeText)
	}

	sepWidth := width - 4
	if sepWidth < 0 {
		sepWidth = 0
	}
	paneSep := " " + lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(strings.Repeat("─", sepWidth))

	var lines []string
	lines = append(lines, header, paneSep)

	contentLines := height - len(lines) - 2 // border top/bottom
	if contentLines < 1 {
		contentLines = 1
	}

	if pane.Error != "" {
		errLine := " " + theme.ErrorStyle.Render(truncateStr(pane.Error, innerWidth))
		lines = append(lines, errLine)
	} else if pane.Connecting {
		lines = append(lines, " "+theme.DimStyle.Render("Connecting..."))
	} else if !pane.Active {
		lines = append(lines, " "+theme.DimStyle.Render("Tail stopped (t to restart)"))
	} else if len(pane.Lines) == 0 {
		lines = append(lines, " "+theme.DimStyle.Render("Waiting for log events..."))
	} else {
		start := len(pane.Lines) - contentLines
		if start < 0 {
			start = 0
		}
		for _, tl := range pane.Lines[start:] {
			ts := tl.Timestamp.Format(time.TimeOnly)
			text := truncateStr(tl.Text, innerWidth-10)
			style := styleTailLevel(tl.Level)
			logLine := fmt.Sprintf(" %s %s",
				theme.LogTimestampStyle.Render(ts),
				style.Render(text))
			lines = append(lines, logLine)
		}
	}

	// Pad to exact content height
	totalContentH := height - 2 // border top/bottom
	for len(lines) < totalContentH {
		lines = append(lines, "")
	}
	if len(lines) > totalContentH {
		lines = lines[:totalContentH]
	}

	content := strings.Join(lines, "\n")

	// Box border
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Width(width-2). // subtract border chars
		Padding(0, 0)

	if pane.IsDev {
		// Dev panes use yellow borders
		if focused {
			boxStyle = boxStyle.BorderForeground(theme.ColorYellow)
		} else {
			boxStyle = boxStyle.BorderForeground(theme.ColorYellowDim)
		}
	} else if focused {
		boxStyle = boxStyle.BorderForeground(theme.ColorOrange)
	} else {
		boxStyle = boxStyle.BorderForeground(theme.ColorDarkGray)
	}

	return boxStyle.Render(content)
}

// --- Utilities ---

func styleTailLevel(level string) lipgloss.Style {
	switch level {
	case "warn":
		return theme.LogLevelWarn
	case "error", "exception":
		return theme.LogLevelError
	case "request":
		return theme.LogLevelRequest
	case "system":
		return theme.LogLevelSystem
	default:
		return theme.LogLevelLog
	}
}

func truncateStr(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

// renderHyperlink renders an OSC 8 hyperlink (clickable in supporting terminals).
func renderHyperlink(url, text string) string {
	style := lipgloss.NewStyle().Foreground(theme.ColorBlue).Underline(true)
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, style.Render(text))
}
