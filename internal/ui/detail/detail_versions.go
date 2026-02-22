package detail

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	"github.com/oarafat/orangeshell/internal/wrangler"
)

// SetVersionHistory is called when version history data has been loaded.
func (m *Model) SetVersionHistory(scriptName string, entries []wrangler.VersionHistoryEntry, err error) {
	// Staleness check: ignore if we've navigated away from this script
	if m.versionHistoryScript != scriptName {
		return
	}
	m.versionHistoryLoading = false
	m.versionHistory = entries
	m.versionHistoryErr = err
	m.versionHistoryCursor = 0
	m.versionHistoryScroll = 0
}

// NeedsVersionHistory returns the script name if a version history fetch should
// be triggered for the currently viewed Worker, or "" if not needed.
func (m Model) NeedsVersionHistory() string {
	if m.service != "Workers" || m.mode != viewDetail || m.detail == nil {
		return ""
	}
	name := m.detail.Name
	if name == "" {
		return ""
	}
	// Already loaded or loading for this script
	if m.versionHistoryScript == name {
		return ""
	}
	return name
}

// StartVersionHistoryLoad marks that a version history fetch is in progress.
func (m *Model) StartVersionHistoryLoad(scriptName string) {
	m.versionHistoryScript = scriptName
	m.versionHistoryLoading = true
	m.versionHistory = nil
	m.versionHistoryErr = nil
	m.versionHistoryCursor = 0
	m.versionHistoryScroll = 0
	m.buildsRestricted = false // clear restriction flag on new load
}

// SetBuildsRestricted marks that the Builds API returned 401/403 for the
// current version history. Renders an inline hint below the heading.
func (m *Model) SetBuildsRestricted() {
	m.buildsRestricted = true
}

// IsWorkersDetail returns true if we're viewing a Workers resource detail.
func (m Model) IsWorkersDetail() bool {
	return m.mode == viewDetail && m.service == "Workers"
}

// SetBuildsEnriched updates the version history entries with build metadata.
func (m *Model) SetBuildsEnriched(scriptName string, entries []wrangler.VersionHistoryEntry) {
	if m.versionHistoryScript != scriptName {
		return
	}
	m.versionHistory = entries
}

// VersionHistory returns the current version history entries (for the app layer to enrich).
func (m Model) VersionHistory() []wrangler.VersionHistoryEntry {
	return m.versionHistory
}

// VersionHistoryScript returns the script name for the current version history.
func (m Model) VersionHistoryScript() string {
	return m.versionHistoryScript
}

// HasCIEntries returns true if any version history entry has HasBuildLog set.
func (m Model) HasCIEntries() bool {
	for _, e := range m.versionHistory {
		if e.HasBuildLog {
			return true
		}
	}
	return false
}

// SetBuildLog stores the fetched build log data.
func (m *Model) SetBuildLog(buildUUID string, lines []string, err error, entry wrangler.VersionHistoryEntry) {
	m.buildLogLoading = false
	m.buildLogLines = lines
	m.buildLogErr = err
	m.buildLogEntry = entry
}

// StartBuildLogLoad marks that a build log fetch is in progress.
func (m *Model) StartBuildLogLoad(entry wrangler.VersionHistoryEntry) {
	m.buildLogVisible = true
	m.buildLogLoading = true
	m.buildLogEntry = entry
	m.buildLogLines = nil
	m.buildLogErr = nil
	m.buildLogScroll = 0
}

// CloseBuildLog closes the build log overlay.
func (m *Model) CloseBuildLog() {
	m.buildLogVisible = false
	m.buildLogLoading = false
	m.buildLogLines = nil
	m.buildLogErr = nil
	m.buildLogScroll = 0
}

// --- Version History Rendering (Workers only) ---

// renderVersionHistory renders the version & deployment history table for a Worker.
func (m Model) renderVersionHistory(width int) []string {
	var lines []string

	// Section separator
	sepWidth := width - 3
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(strings.Repeat("─", sepWidth))
	lines = append(lines, "", sep)

	heading := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorOrange).Render(" Version & Deployment History")
	lines = append(lines, heading)

	if m.buildsRestricted {
		hint := theme.DimStyle.Render(" (restricted) Build info unavailable — add a fallback token for this account")
		lines = append(lines, hint)
	}

	if m.versionHistoryLoading {
		lines = append(lines, fmt.Sprintf(" %s %s", m.spinner.View(), theme.DimStyle.Render("Loading version history...")))
		return lines
	}

	if m.versionHistoryErr != nil {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s", m.versionHistoryErr.Error())))
		return lines
	}

	if len(m.versionHistory) == 0 {
		lines = append(lines, theme.DimStyle.Render(" No version history available"))
		return lines
	}

	// Column layout — calculate available widths
	// Fixed columns: version (10), source (12), author (variable), when (10), live marker (6)
	// Message gets the remaining space
	colVersion := 10
	colSource := 12
	colWhen := 10
	colLive := 8
	colAuthor := 16
	colMsg := width - colVersion - colSource - colAuthor - colWhen - colLive - 6 // 6 for padding/spaces
	if colMsg < 10 {
		colMsg = 10
	}

	// Header
	header := fmt.Sprintf(" %-*s %-*s %-*s %-*s %-*s",
		colVersion, "VERSION",
		colSource, "SOURCE",
		colMsg, "MESSAGE",
		colAuthor, "AUTHOR",
		colWhen, "WHEN",
	)
	lines = append(lines, lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(header))

	// Header separator
	lines = append(lines, lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		" "+strings.Repeat("─", width-3)))

	// Rows
	greenPipe := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorGreen).Render("┃")
	dimPipe := "  " // no live marker — just padding

	for i, entry := range m.versionHistory {
		versionStr := entry.ShortID()
		sourceStr := truncateRunes(entry.DisplaySource(), colSource-1)
		msgStr := truncateRunes(entry.DisplayMessage(), colMsg-1)
		authorStr := truncateRunes(entry.DisplayAuthor(), colAuthor-1)
		whenStr := entry.RelativeTime()

		// Live marker
		liveMarker := dimPipe
		if entry.IsLive {
			liveMarker = greenPipe + " "
		}

		row := fmt.Sprintf("%s%-*s %-*s %-*s %-*s %-*s",
			liveMarker,
			colVersion, versionStr,
			colSource, sourceStr,
			colMsg, msgStr,
			colAuthor, authorStr,
			colWhen, whenStr,
		)

		if i == m.versionHistoryCursor && m.focus == FocusDetail {
			// Highlighted row
			style := lipgloss.NewStyle().Background(lipgloss.Color("#333355")).Foreground(lipgloss.Color("#FAFAFA"))
			if entry.IsLive {
				row = greenPipe + " " + style.Render(fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s",
					colVersion, versionStr,
					colSource, sourceStr,
					colMsg, msgStr,
					colAuthor, authorStr,
					colWhen, whenStr,
				))
			} else {
				row = "  " + style.Render(fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s",
					colVersion, versionStr,
					colSource, sourceStr,
					colMsg, msgStr,
					colAuthor, authorStr,
					colWhen, whenStr,
				))
			}
		} else if entry.IsLive {
			// Live row — green text, pad before styling
			greenStyle := lipgloss.NewStyle().Foreground(theme.ColorGreen)
			row = greenPipe + " " +
				greenStyle.Render(padRight(versionStr, colVersion)) + " " +
				greenStyle.Render(padRight(sourceStr, colSource)) + " " +
				greenStyle.Render(padRight(msgStr, colMsg)) + " " +
				greenStyle.Render(padRight(authorStr, colAuthor)) + " " +
				greenStyle.Render(whenStr)
		} else {
			// Normal row — pad before styling to preserve alignment
			sourceStyle := lipgloss.NewStyle().Foreground(theme.ColorGray)
			if entry.RawSource == "wrangler" {
				sourceStyle = lipgloss.NewStyle().Foreground(theme.ColorDarkGray)
			}
			row = "  " +
				theme.ValueStyle.Render(padRight(versionStr, colVersion)) + " " +
				sourceStyle.Render(padRight(sourceStr, colSource)) + " " +
				theme.DimStyle.Render(padRight(msgStr, colMsg)) + " " +
				theme.DimStyle.Render(padRight(authorStr, colAuthor)) + " " +
				theme.DimStyle.Render(whenStr)
		}

		lines = append(lines, row)
	}

	return lines
}

// --- Build Log View (Workers Builds — Phase 2) ---

// updateBuildLog handles key events when the build log overlay is visible.
func (m Model) updateBuildLog(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.CloseBuildLog()
		return m, nil
	case "up", "k":
		if m.buildLogScroll > 0 {
			m.buildLogScroll--
		}
	case "down", "j":
		maxScroll := len(m.buildLogLines) - 10
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.buildLogScroll < maxScroll {
			m.buildLogScroll++
		}
	case "pgup":
		m.buildLogScroll -= 20
		if m.buildLogScroll < 0 {
			m.buildLogScroll = 0
		}
	case "pgdown":
		maxScroll := len(m.buildLogLines) - 10
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.buildLogScroll += 20
		if m.buildLogScroll > maxScroll {
			m.buildLogScroll = maxScroll
		}
	case "home", "g":
		m.buildLogScroll = 0
	case "end", "G":
		maxScroll := len(m.buildLogLines) - 10
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.buildLogScroll = maxScroll
	}
	return m, nil
}

// renderBuildLog renders the build log overlay. It replaces the version history
// and detail content with the build metadata header and scrollable log output.
func (m Model) renderBuildLog(width, height int) []string {
	var lines []string
	entry := m.buildLogEntry

	// Header: build metadata
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorOrange)
	dimStyle := theme.DimStyle
	valStyle := theme.ValueStyle

	lines = append(lines, headerStyle.Render(" Build Log"))
	lines = append(lines, "")

	// Status with color
	statusStr := entry.BuildStatus
	statusStyle := dimStyle
	switch entry.BuildStatus {
	case "success":
		statusStyle = lipgloss.NewStyle().Foreground(theme.ColorGreen)
	case "failure", "failed":
		statusStyle = lipgloss.NewStyle().Foreground(theme.ColorRed)
	case "running":
		statusStyle = lipgloss.NewStyle().Foreground(theme.ColorYellow)
	case "canceled":
		statusStyle = lipgloss.NewStyle().Foreground(theme.ColorGray)
	}
	lines = append(lines, fmt.Sprintf(" %s  %s", dimStyle.Render("Status:"), statusStyle.Render(statusStr)))

	if entry.GitBranch != "" {
		lines = append(lines, fmt.Sprintf(" %s  %s", dimStyle.Render("Branch:"), valStyle.Render(entry.GitBranch)))
	}
	if entry.GitCommit != "" {
		shortCommit := entry.GitCommit
		if len(shortCommit) > 10 {
			shortCommit = shortCommit[:10]
		}
		lines = append(lines, fmt.Sprintf(" %s  %s", dimStyle.Render("Commit:"), valStyle.Render(shortCommit)))
	}
	if entry.CommitMsg != "" {
		lines = append(lines, fmt.Sprintf(" %s %s", dimStyle.Render("Message:"), valStyle.Render(truncateRunes(entry.CommitMsg, width-12))))
	}

	lines = append(lines, fmt.Sprintf(" %s %s", dimStyle.Render("Version:"), valStyle.Render(entry.ShortID())))
	lines = append(lines, fmt.Sprintf(" %s    %s", dimStyle.Render("When:"), dimStyle.Render(entry.RelativeTime())))

	// Separator
	sepWidth := width - 3
	if sepWidth < 0 {
		sepWidth = 0
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(strings.Repeat("─", sepWidth)))

	if m.buildLogLoading {
		lines = append(lines, fmt.Sprintf(" %s %s", m.spinner.View(), dimStyle.Render("Loading build log...")))
		return lines
	}

	if m.buildLogErr != nil {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s", m.buildLogErr.Error())))
		return lines
	}

	if len(m.buildLogLines) == 0 {
		lines = append(lines, dimStyle.Render(" No build log available"))
		return lines
	}

	// Log content with scroll
	headerLines := len(lines)
	availableHeight := height - headerLines - 2
	if availableHeight < 3 {
		availableHeight = 3
	}

	start := m.buildLogScroll
	if start >= len(m.buildLogLines) {
		start = len(m.buildLogLines) - 1
	}
	if start < 0 {
		start = 0
	}
	end := start + availableHeight
	if end > len(m.buildLogLines) {
		end = len(m.buildLogLines)
	}

	logStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
	for _, line := range m.buildLogLines[start:end] {
		if runeWidth(line) > width-2 {
			line = truncateRunes(line, width-2)
		}
		lines = append(lines, " "+logStyle.Render(line))
	}

	// Scroll indicator
	total := len(m.buildLogLines)
	if total > availableHeight {
		pct := 0
		if total > 0 {
			pct = (start * 100) / total
		}
		scrollInfo := fmt.Sprintf(" [%d/%d lines · %d%%]", start+1, total, pct)
		lines = append(lines, dimStyle.Render(scrollInfo))
	}

	// Help bar
	lines = append(lines, "")
	helpStyle := lipgloss.NewStyle().Foreground(theme.ColorDarkGray)
	lines = append(lines, helpStyle.Render(" esc back · j/k scroll · pgup/pgdn page · g/G top/bottom"))

	return lines
}
