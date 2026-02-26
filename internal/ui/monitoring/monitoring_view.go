package monitoring

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

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

	// Right pane: analytics view or tail grid
	var rightView string
	if m.showAnalytics {
		rightView = m.analyticsView.View(rightWidth, contentHeight)
	} else {
		rightView = m.viewTailGrid(rightWidth, contentHeight)
	}

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
	if m.exportActive {
		exportBadge := lipgloss.NewStyle().Foreground(theme.ColorGreen).Bold(true).Render(" [export]")
		title += exportBadge
	}

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
	hint := " " + theme.DimStyle.Render("Select a worker and press space to start tailing.")

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
