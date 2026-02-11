package wrangler

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

const (
	parallelTailMaxLines = 200 // max log lines per pane
	parallelTailMinPaneH = 6   // minimum pane height
	parallelTailCols     = 2   // fixed column count
)

// ParallelTailTarget identifies a worker to tail.
type ParallelTailTarget struct {
	ScriptName string
	URL        string // workers.dev URL (may be empty)
}

// ParallelTailStartMsg requests the app to start parallel tailing for an env.
type ParallelTailStartMsg struct {
	EnvName string
	Scripts []ParallelTailTarget
}

// ParallelTailExitMsg requests the app to stop all parallel tail sessions.
type ParallelTailExitMsg struct{}

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

// appendLines adds tail lines to the pane, capping at the max.
func (p *TailPane) appendLines(lines []svc.TailLine) {
	p.Lines = append(p.Lines, lines...)
	if len(p.Lines) > parallelTailMaxLines {
		p.Lines = p.Lines[len(p.Lines)-parallelTailMaxLines:]
	}
}

// ParallelTailModel manages the grid of parallel tail panes.
type ParallelTailModel struct {
	envName string
	panes   []TailPane
	scrollY int // vertical scroll offset (in rows)
	width   int
	height  int
	active  bool
}

// Start initializes the parallel tail grid for the given env and targets.
func (m *ParallelTailModel) Start(envName string, targets []ParallelTailTarget) {
	m.envName = envName
	m.panes = make([]TailPane, len(targets))
	for i, t := range targets {
		m.panes[i] = TailPane{
			ScriptName: t.ScriptName,
			URL:        t.URL,
			Connecting: true,
		}
	}
	m.scrollY = 0
	m.active = true
}

// Stop clears all state and marks the model as inactive.
func (m *ParallelTailModel) Stop() {
	m.panes = nil
	m.envName = ""
	m.scrollY = 0
	m.active = false
}

// IsActive returns whether the parallel tail grid is currently displayed.
func (m *ParallelTailModel) IsActive() bool {
	return m.active
}

// EnvName returns the environment being tailed.
func (m *ParallelTailModel) EnvName() string {
	return m.envName
}

// PaneCount returns the number of panes.
func (m *ParallelTailModel) PaneCount() int {
	return len(m.panes)
}

// AppendLines routes incoming lines to the correct pane by script name.
func (m *ParallelTailModel) AppendLines(scriptName string, lines []svc.TailLine) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].appendLines(lines)
			return
		}
	}
}

// SetConnected marks a pane as connected.
func (m *ParallelTailModel) SetConnected(scriptName string) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].Connecting = false
			m.panes[i].Connected = true
			return
		}
	}
}

// SetError marks a pane as errored.
func (m *ParallelTailModel) SetError(scriptName string, errMsg string) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].Connecting = false
			m.panes[i].Error = errMsg
			return
		}
	}
}

// SetSessionID records the tail session ID for a pane.
func (m *ParallelTailModel) SetSessionID(scriptName, sessionID string) {
	for i := range m.panes {
		if m.panes[i].ScriptName == scriptName {
			m.panes[i].SessionID = sessionID
			return
		}
	}
}

// SetSize updates the available dimensions.
func (m *ParallelTailModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// totalRows returns the number of grid rows.
func (m *ParallelTailModel) totalRows() int {
	return int(math.Ceil(float64(len(m.panes)) / float64(parallelTailCols)))
}

// Update handles key events for the parallel tail grid.
func (m ParallelTailModel) Update(msg tea.Msg) (ParallelTailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return ParallelTailExitMsg{} }
		case "j", "down":
			totalRows := m.totalRows()
			if m.scrollY < totalRows-1 {
				m.scrollY++
			}
		case "k", "up":
			if m.scrollY > 0 {
				m.scrollY--
			}
		case "g", "home":
			m.scrollY = 0
		case "G", "end":
			totalRows := m.totalRows()
			if totalRows > 0 {
				m.scrollY = totalRows - 1
			}
		}
	}
	return m, nil
}

// View renders the 2-column grid of tail panes.
func (m ParallelTailModel) View() string {
	if len(m.panes) == 0 {
		return ""
	}

	contentHeight := m.height - 4 // border + title + separator
	if contentHeight < 1 {
		contentHeight = 1
	}
	boxWidth := m.width - 4

	// Title
	title := theme.TitleStyle.Render(fmt.Sprintf("  Tail — %s", m.envName))
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	subtitle := theme.DimStyle.Render(fmt.Sprintf("  %d workers", len(m.panes)))

	// Calculate grid dimensions
	totalRows := m.totalRows()
	colWidth := boxWidth / parallelTailCols
	if colWidth < 10 {
		colWidth = 10
	}

	// Calculate pane height based on available space
	headerLines := 4 // title + sep + subtitle + spacer
	gridHeight := contentHeight - headerLines
	if gridHeight < parallelTailMinPaneH {
		gridHeight = parallelTailMinPaneH
	}

	// How many rows can we show at once?
	visibleRows := gridHeight / parallelTailMinPaneH
	if visibleRows < 1 {
		visibleRows = 1
	}
	if visibleRows > totalRows {
		visibleRows = totalRows
	}

	// Each visible row gets equal height
	paneHeight := gridHeight / visibleRows
	if paneHeight < parallelTailMinPaneH {
		paneHeight = parallelTailMinPaneH
	}

	// Apply scroll bounds
	maxScroll := totalRows - visibleRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	scrollY := m.scrollY
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
			// Empty placeholder for odd count
			rightView = strings.Repeat("\n", paneHeight-1)
			rightView = lipgloss.NewStyle().Width(colWidth).Render(rightView)
		}

		rowView := lipgloss.JoinHorizontal(lipgloss.Top, leftView, rightView)
		rowViews = append(rowViews, rowView)
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)

	// Scroll indicator
	scrollHint := ""
	if totalRows > visibleRows {
		scrollHint = theme.DimStyle.Render(fmt.Sprintf("  [%d/%d rows]", scrollY+1, totalRows))
	}

	// Help text
	helpText := theme.DimStyle.Render("  esc back  |  j/k scroll")

	// Assemble all lines
	var allLines []string
	allLines = append(allLines, title, sep, subtitle)
	if scrollHint != "" {
		allLines = append(allLines, scrollHint)
	}
	allLines = append(allLines, "")

	// Add grid lines
	gridLines := strings.Split(grid, "\n")
	allLines = append(allLines, gridLines...)

	allLines = append(allLines, "", helpText)

	// Truncate/pad to content height
	if len(allLines) > contentHeight {
		allLines = allLines[:contentHeight]
	}
	for len(allLines) < contentHeight {
		allLines = append(allLines, "")
	}

	return strings.Join(allLines, "\n")
}

// renderPane renders a single tail pane within the grid.
func (m ParallelTailModel) renderPane(pane *TailPane, width, height int) string {
	if height < 1 {
		return ""
	}

	innerWidth := width - 3 // left padding + right margin

	// Header: script name (bold)
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorOrange)
	header := "  " + nameStyle.Render(truncateStr(pane.ScriptName, innerWidth))

	// URL (dim, hyperlink if available)
	urlLine := ""
	if pane.URL != "" {
		urlText := truncateStr(pane.URL, innerWidth)
		urlLine = "  " + renderHyperlink(pane.URL, urlText)
	}

	// Separator
	sepWidth := width - 2
	if sepWidth < 0 {
		sepWidth = 0
	}
	paneSep := "  " + lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(strings.Repeat("─", sepWidth))

	var lines []string
	lines = append(lines, header)
	if urlLine != "" {
		lines = append(lines, urlLine)
	}
	lines = append(lines, paneSep)

	// Content area
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
		// Show the most recent log lines that fit
		start := len(pane.Lines) - contentLines
		if start < 0 {
			start = 0
		}
		for _, tl := range pane.Lines[start:] {
			ts := tl.Timestamp.Format("15:04:05")
			text := truncateStr(tl.Text, innerWidth-10) // timestamp + space
			style := styleTailLevel(tl.Level)
			logLine := fmt.Sprintf("  %s %s",
				theme.LogTimestampStyle.Render(ts),
				style.Render(text))
			lines = append(lines, logLine)
		}
	}

	// Pad to exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(width).Render(content)
}

// truncateStr truncates a string to max visible characters, adding "..." if truncated.
func truncateStr(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
