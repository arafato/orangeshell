// Package monitoring implements the Monitoring tab — a dedicated view for
// Workers live log tailing (single-worker and parallel/monorepo multi-worker).
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

// Mode describes whether we're in single-worker or parallel (multi-worker) tail mode.
type Mode int

const (
	ModeIdle     Mode = iota // No active tail
	ModeSingle               // Tailing one worker
	ModeParallel             // Tailing multiple workers (monorepo)
)

const (
	singleTailMaxLines   = 500
	parallelTailMaxLines = 200
	parallelTailMinPaneH = 6
	parallelTailCols     = 2
)

// --- Messages emitted by the monitoring model ---

// TailStopMsg requests the app to stop the active tail session.
type TailStopMsg struct{}

// ParallelTailStopMsg requests the app to stop all parallel tail sessions.
type ParallelTailStopMsg struct{}

// TailPane holds per-worker tail state in the parallel tail grid.
type TailPane struct {
	ScriptName string
	URL        string
	Lines      []svc.TailLine
	Connecting bool
	Connected  bool
	Error      string
	SessionID  string
}

func (p *TailPane) appendLines(lines []svc.TailLine) {
	p.Lines = append(p.Lines, lines...)
	if len(p.Lines) > parallelTailMaxLines {
		p.Lines = p.Lines[len(p.Lines)-parallelTailMaxLines:]
	}
}

// ParallelTailTarget identifies a worker to tail.
type ParallelTailTarget struct {
	ScriptName string
	URL        string
}

// Model holds all monitoring tab state.
type Model struct {
	mode Mode

	// Single-tail state
	scriptName   string
	tailLines    []svc.TailLine
	tailActive   bool
	tailStarting bool
	tailError    string
	scrollOffset int  // lines scrolled up from bottom (0 = pinned to bottom)
	userScrolled bool // true if user has manually scrolled

	// Parallel-tail state
	envName         string
	panes           []TailPane
	parallelScrollY int

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

// Mode returns the current monitoring mode.
func (m Model) CurrentMode() Mode {
	return m.mode
}

// IsActive returns whether any tail session is active or starting.
func (m Model) IsActive() bool {
	return m.mode != ModeIdle
}

// --- Single-tail API ---

// StartSingleTail initializes the model for tailing a single worker.
func (m *Model) StartSingleTail(scriptName string) {
	m.mode = ModeSingle
	m.scriptName = scriptName
	m.tailLines = nil
	m.tailActive = false
	m.tailStarting = true
	m.tailError = ""
	m.scrollOffset = 0
	m.userScrolled = false
}

// SetTailConnected marks the single tail as connected.
func (m *Model) SetTailConnected() {
	m.tailStarting = false
	m.tailActive = true
	m.tailLines = nil
	m.scrollOffset = 0
	m.tailError = ""
}

// AppendTailLines adds lines to the single-tail buffer.
func (m *Model) AppendTailLines(lines []svc.TailLine) {
	m.tailLines = append(m.tailLines, lines...)
	if len(m.tailLines) > singleTailMaxLines {
		m.tailLines = m.tailLines[len(m.tailLines)-singleTailMaxLines:]
	}
	if !m.userScrolled {
		m.scrollOffset = 0
	}
}

// SetTailError records a tail error.
func (m *Model) SetTailError(err error) {
	m.tailStarting = false
	m.tailError = err.Error()
}

// SetTailStopped marks the single tail as stopped (but keeps mode so we show the last lines).
func (m *Model) SetTailStopped() {
	m.tailActive = false
	m.tailStarting = false
	m.scrollOffset = 0
}

// ClearSingleTail resets all single-tail state and returns to idle.
func (m *Model) ClearSingleTail() {
	m.mode = ModeIdle
	m.scriptName = ""
	m.tailLines = nil
	m.tailActive = false
	m.tailStarting = false
	m.tailError = ""
	m.scrollOffset = 0
	m.userScrolled = false
}

// SingleTailActive returns whether a single tail is actively connected.
func (m Model) SingleTailActive() bool {
	return m.mode == ModeSingle && m.tailActive
}

// SingleTailStarting returns whether a single tail is waiting for connection.
func (m Model) SingleTailStarting() bool {
	return m.mode == ModeSingle && m.tailStarting
}

// ScriptName returns the script being tailed (single mode).
func (m Model) ScriptName() string {
	return m.scriptName
}

// --- Parallel-tail API ---

// StartParallelTail initializes the model for parallel multi-worker tailing.
func (m *Model) StartParallelTail(envName string, targets []ParallelTailTarget) {
	m.mode = ModeParallel
	m.envName = envName
	m.panes = make([]TailPane, len(targets))
	for i, t := range targets {
		m.panes[i] = TailPane{
			ScriptName: t.ScriptName,
			URL:        t.URL,
			Connecting: true,
		}
	}
	m.parallelScrollY = 0
	// Clear single-tail state
	m.scriptName = ""
	m.tailLines = nil
	m.tailActive = false
	m.tailStarting = false
	m.tailError = ""
	m.scrollOffset = 0
	m.userScrolled = false
}

// StopParallelTail clears all parallel tail state and returns to idle.
func (m *Model) StopParallelTail() {
	m.mode = ModeIdle
	m.panes = nil
	m.envName = ""
	m.parallelScrollY = 0
}

// IsParallelTailActive returns whether parallel tailing is active.
func (m Model) IsParallelTailActive() bool {
	return m.mode == ModeParallel
}

// ParallelTailAppendLines routes lines to the correct pane.
func (m *Model) ParallelTailAppendLines(scriptName string, lines []svc.TailLine) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].appendLines(lines)
			return
		}
	}
}

// ParallelTailSetConnected marks a pane as connected.
func (m *Model) ParallelTailSetConnected(scriptName string) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].Connecting = false
			m.panes[i].Connected = true
			return
		}
	}
}

// ParallelTailSetError marks a pane as errored.
func (m *Model) ParallelTailSetError(scriptName string, err error) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].Connecting = false
			m.panes[i].Error = err.Error()
			return
		}
	}
}

// ParallelTailSetSessionID records the tail session ID for a pane.
func (m *Model) ParallelTailSetSessionID(scriptName, sessionID string) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].SessionID = sessionID
			return
		}
	}
}

// EnvName returns the environment being tailed (parallel mode).
func (m Model) EnvName() string {
	return m.envName
}

// PaneCount returns the number of parallel panes.
func (m Model) PaneCount() int {
	return len(m.panes)
}

// --- Clear all ---

// Clear resets the entire monitoring model to idle.
func (m *Model) Clear() {
	m.mode = ModeIdle
	m.scriptName = ""
	m.tailLines = nil
	m.tailActive = false
	m.tailStarting = false
	m.tailError = ""
	m.scrollOffset = 0
	m.userScrolled = false
	m.envName = ""
	m.panes = nil
	m.parallelScrollY = 0
}

// --- Update ---

// Update handles key events for the monitoring tab.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case ModeSingle:
			return m.updateSingle(msg)
		case ModeParallel:
			return m.updateParallel(msg)
		}
	}
	return m, nil
}

func (m Model) updateSingle(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "t":
		if m.tailActive || m.tailStarting {
			// Stop the tail
			return m, func() tea.Msg { return TailStopMsg{} }
		}
	case "pgup":
		m.scrollOffset += 10
		max := len(m.tailLines)
		if m.scrollOffset > max {
			m.scrollOffset = max
		}
		m.userScrolled = true
	case "pgdown":
		m.scrollOffset -= 10
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}
		if m.scrollOffset == 0 {
			m.userScrolled = false
		}
	case "end":
		m.scrollOffset = 0
		m.userScrolled = false
	case "j", "down":
		m.scrollOffset -= 1
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}
		if m.scrollOffset == 0 {
			m.userScrolled = false
		}
	case "k", "up":
		m.scrollOffset += 1
		max := len(m.tailLines)
		if m.scrollOffset > max {
			m.scrollOffset = max
		}
		m.userScrolled = true
	}
	return m, nil
}

func (m Model) updateParallel(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return ParallelTailStopMsg{} }
	case "j", "down":
		totalRows := m.totalRows()
		if m.parallelScrollY < totalRows-1 {
			m.parallelScrollY++
		}
	case "k", "up":
		if m.parallelScrollY > 0 {
			m.parallelScrollY--
		}
	case "g", "home":
		m.parallelScrollY = 0
	case "G", "end":
		totalRows := m.totalRows()
		if totalRows > 0 {
			m.parallelScrollY = totalRows - 1
		}
	}
	return m, nil
}

func (m Model) totalRows() int {
	return int(math.Ceil(float64(len(m.panes)) / float64(parallelTailCols)))
}

// --- View ---

// View renders the monitoring tab content.
func (m Model) View() string {
	switch m.mode {
	case ModeSingle:
		return m.viewSingle()
	case ModeParallel:
		return m.viewParallel()
	default:
		return m.viewIdle()
	}
}

func (m Model) viewIdle() string {
	contentHeight := m.height
	if contentHeight < 1 {
		contentHeight = 1
	}
	hint := theme.DimStyle.Render("  No active tail session. Press t on a Worker to start tailing.")
	lines := []string{"", hint}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines[:contentHeight], "\n")
}

func (m Model) viewSingle() string {
	contentHeight := m.height
	if contentHeight < 3 {
		contentHeight = 3
	}
	boxWidth := m.width - 4
	if boxWidth < 10 {
		boxWidth = 10
	}

	// Title
	var titleText string
	if m.tailActive {
		titleText = fmt.Sprintf("  \u25b8 Live Logs — %s", m.scriptName)
	} else if m.tailStarting {
		titleText = fmt.Sprintf("  Connecting to %s...", m.scriptName)
	} else {
		titleText = fmt.Sprintf("  \u25b9 %s (stopped)", m.scriptName)
	}
	title := theme.LogConsoleHeaderStyle.Render(titleText)

	sepWidth := boxWidth - 2
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("\u2500", sepWidth)))

	// Help line
	var helpText string
	if m.tailActive || m.tailStarting {
		helpText = theme.DimStyle.Render("  t stop tail  |  j/k scroll  |  pgup/pgdn page  |  end bottom")
	} else {
		helpText = theme.DimStyle.Render("  esc back to previous tab")
	}

	headerLines := []string{title, sep}
	footerLines := []string{helpText}

	// Content area
	availableLines := contentHeight - len(headerLines) - len(footerLines)
	if availableLines < 1 {
		availableLines = 1
	}

	var contentLines []string

	if m.tailError != "" {
		errLine := theme.ErrorStyle.Render(fmt.Sprintf("  Error: %s", m.tailError))
		contentLines = append(contentLines, errLine)
	} else if m.tailStarting {
		contentLines = append(contentLines, "  "+theme.DimStyle.Render("Waiting for connection..."))
	} else if len(m.tailLines) == 0 {
		contentLines = append(contentLines, "  "+theme.DimStyle.Render("Waiting for log events..."))
	} else {
		contentLines = m.renderTailLines(boxWidth)
		contentLines = m.applyScroll(contentLines, availableLines)
	}

	// Pad content to fill space
	for len(contentLines) < availableLines {
		contentLines = append(contentLines, "")
	}
	if len(contentLines) > availableLines {
		contentLines = contentLines[:availableLines]
	}

	var allLines []string
	allLines = append(allLines, headerLines...)
	allLines = append(allLines, contentLines...)
	allLines = append(allLines, footerLines...)

	// Apply black background to every line
	return m.applyBackground(allLines, m.width)
}

func (m Model) renderTailLines(width int) []string {
	var out []string
	maxTextWidth := width - 14 // "  HH:MM:SS  " prefix
	if maxTextWidth < 5 {
		maxTextWidth = 5
	}
	for _, tl := range m.tailLines {
		text := truncateStr(tl.Text, maxTextWidth)
		ts := theme.LogTimestampStyle.Render(tl.Timestamp.Format(time.TimeOnly))
		style := styleTailLevel(tl.Level)
		out = append(out, fmt.Sprintf("  %s %s", ts, style.Render(text)))
	}
	return out
}

func (m Model) applyScroll(lines []string, viewHeight int) []string {
	if len(lines) <= viewHeight {
		return lines
	}
	end := len(lines) - m.scrollOffset
	if end < viewHeight {
		end = viewHeight
	}
	if end > len(lines) {
		end = len(lines)
	}
	start := end - viewHeight
	if start < 0 {
		start = 0
	}
	return lines[start:end]
}

func (m Model) applyBackground(lines []string, width int) string {
	bgStyle := lipgloss.NewStyle().Background(theme.LogConsoleBg).Width(width)
	var result []string
	for _, line := range lines {
		result = append(result, bgStyle.Render(line))
	}
	return strings.Join(result, "\n")
}

// --- Parallel tail view ---

func (m Model) viewParallel() string {
	if len(m.panes) == 0 {
		return m.viewIdle()
	}

	contentHeight := m.height - 4
	if contentHeight < 1 {
		contentHeight = 1
	}
	boxWidth := m.width - 4

	// Title
	title := theme.TitleStyle.Render(fmt.Sprintf("  Tail \u2014 %s", m.envName))
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("\u2500", sepWidth)))

	subtitle := theme.DimStyle.Render(fmt.Sprintf("  %d workers", len(m.panes)))

	// Grid layout
	totalRows := m.totalRows()
	colWidth := boxWidth / parallelTailCols
	if colWidth < 10 {
		colWidth = 10
	}

	headerLines := 4 // title + sep + subtitle + spacer
	gridHeight := contentHeight - headerLines
	if gridHeight < parallelTailMinPaneH {
		gridHeight = parallelTailMinPaneH
	}

	visibleRows := gridHeight / parallelTailMinPaneH
	if visibleRows < 1 {
		visibleRows = 1
	}
	if visibleRows > totalRows {
		visibleRows = totalRows
	}

	paneHeight := gridHeight / visibleRows
	if paneHeight < parallelTailMinPaneH {
		paneHeight = parallelTailMinPaneH
	}

	maxScroll := totalRows - visibleRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	scrollY := m.parallelScrollY
	if scrollY > maxScroll {
		scrollY = maxScroll
	}

	// Build visible rows
	var rowViews []string
	for row := scrollY; row < scrollY+visibleRows && row < totalRows; row++ {
		leftIdx := row * parallelTailCols
		rightIdx := leftIdx + 1

		leftView := m.renderPane(&m.panes[leftIdx], colWidth, paneHeight)
		rightView := ""
		if rightIdx < len(m.panes) {
			rightView = m.renderPane(&m.panes[rightIdx], colWidth, paneHeight)
		} else {
			rightView = strings.Repeat("\n", paneHeight-1)
			rightView = lipgloss.NewStyle().Width(colWidth).Render(rightView)
		}

		rowView := lipgloss.JoinHorizontal(lipgloss.Top, leftView, rightView)
		rowViews = append(rowViews, rowView)
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)

	scrollHint := ""
	if totalRows > visibleRows {
		scrollHint = theme.DimStyle.Render(fmt.Sprintf("  [%d/%d rows]", scrollY+1, totalRows))
	}

	helpText := theme.DimStyle.Render("  esc back  |  j/k scroll")

	var allLines []string
	allLines = append(allLines, title, sep, subtitle)
	if scrollHint != "" {
		allLines = append(allLines, scrollHint)
	}
	allLines = append(allLines, "")

	gridLines := strings.Split(grid, "\n")
	allLines = append(allLines, gridLines...)
	allLines = append(allLines, "", helpText)

	if len(allLines) > contentHeight {
		allLines = allLines[:contentHeight]
	}
	for len(allLines) < contentHeight {
		allLines = append(allLines, "")
	}

	return strings.Join(allLines, "\n")
}

func (m Model) renderPane(pane *TailPane, width, height int) string {
	if height < 1 {
		return ""
	}

	innerWidth := width - 3

	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorOrange)
	header := "  " + nameStyle.Render(truncateStr(pane.ScriptName, innerWidth))

	urlLine := ""
	if pane.URL != "" {
		urlText := truncateStr(pane.URL, innerWidth)
		urlLine = "  " + renderHyperlink(pane.URL, urlText)
	}

	sepWidth := width - 2
	if sepWidth < 0 {
		sepWidth = 0
	}
	paneSep := "  " + lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(strings.Repeat("\u2500", sepWidth))

	var lines []string
	lines = append(lines, header)
	if urlLine != "" {
		lines = append(lines, urlLine)
	}
	lines = append(lines, paneSep)

	contentLines := height - len(lines)
	if contentLines < 1 {
		contentLines = 1
	}

	if pane.Error != "" {
		errLine := "  " + theme.ErrorStyle.Render(truncateStr(pane.Error, innerWidth))
		lines = append(lines, errLine)
	} else if pane.Connecting {
		lines = append(lines, "  "+theme.DimStyle.Render("Connecting..."))
	} else if len(pane.Lines) == 0 {
		lines = append(lines, "  "+theme.DimStyle.Render("Waiting for log events..."))
	} else {
		start := len(pane.Lines) - contentLines
		if start < 0 {
			start = 0
		}
		for _, tl := range pane.Lines[start:] {
			ts := tl.Timestamp.Format("15:04:05")
			text := truncateStr(tl.Text, innerWidth-10)
			style := styleTailLevel(tl.Level)
			logLine := fmt.Sprintf("  %s %s",
				theme.LogTimestampStyle.Render(ts),
				style.Render(text))
			lines = append(lines, logLine)
		}
	}

	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Render(content)
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
