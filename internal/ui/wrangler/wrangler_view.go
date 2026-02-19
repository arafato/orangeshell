package wrangler

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// View renders the wrangler view.
func (m Model) View() string {
	contentHeight := m.height - 2 // border top + bottom
	if contentHeight < 1 {
		contentHeight = 1
	}
	boxWidth := m.width - 4 // padding within the detail panel

	// Title bar
	titleText := "  Wrangler"
	if m.IsMonorepo() && m.activeProject >= 0 && m.activeProject < len(m.projects) {
		titleText = fmt.Sprintf("  %s / %s", m.rootName, m.projects[m.activeProject].box.Name)
	}
	title := theme.TitleStyle.Render(titleText)
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Config error
	if m.configErr != nil {
		body := theme.ErrorStyle.Render(fmt.Sprintf("\n  Error loading config: %s", m.configErr.Error()))
		content := fmt.Sprintf("%s\n%s\n%s", title, sep, body)
		return m.renderBorder(content, contentHeight)
	}

	// Config loading (not shown in monorepo mode since projects handle their own state)
	if m.configLoading && m.config == nil && m.configErr == nil && !m.IsMonorepo() {
		body := fmt.Sprintf("\n  %s %s",
			m.spinner.View(),
			theme.DimStyle.Render("Loading wrangler configuration..."))
		content := fmt.Sprintf("%s\n%s\n%s", title, sep, body)
		return m.renderBorder(content, contentHeight)
	}

	// Directory browser (shown over any state — config loaded or not)
	if m.showDirBrowser {
		content := m.dirBrowser.View(boxWidth, contentHeight)
		return m.renderBorder(content, contentHeight)
	}

	// Monorepo project list view
	if m.IsOnProjectList() {
		return m.viewProjectList(contentHeight, boxWidth, title, sep)
	}

	// No config found — show interactive menu
	if m.config == nil {
		body := theme.DimStyle.Render("\n  No wrangler configuration found.\n")

		options := []string{
			"Create a new project...",
			"Open an existing project...",
		}
		var menu string
		for i, opt := range options {
			if i == m.emptyMenuCursor {
				menu += fmt.Sprintf("  %s %s\n",
					lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render(">"),
					lipgloss.NewStyle().Foreground(theme.ColorOrange).Render(opt))
			} else {
				menu += fmt.Sprintf("    %s\n", theme.DimStyle.Render(opt))
			}
		}

		content := fmt.Sprintf("%s\n%s\n%s\n%s", title, sep, body, menu)
		return m.renderBorder(content, contentHeight)
	}

	// Calculate layout split:
	// - When a wrangler command is running: command output pane at ~35%
	// - Otherwise: env boxes get full height (no bottom pane)
	cmdPaneHeight := 0
	envPaneHeight := contentHeight

	if m.activeCmdPane != nil && m.activeCmdPane.IsActive() {
		// Wrangler command output pane
		cmdPaneHeight = contentHeight * 35 / 100
		if cmdPaneHeight < 6 {
			cmdPaneHeight = 6
		}
		envPaneHeight = contentHeight - cmdPaneHeight
		if envPaneHeight < 5 {
			envPaneHeight = 5
		}
	}

	// Config path subtitle
	subtitle := theme.DimStyle.Render(fmt.Sprintf("  %s", m.configPath))

	// Worker name
	workerLine := ""
	if m.config.Name != "" {
		workerLine = fmt.Sprintf("  %s %s",
			theme.LabelStyle.Render("Worker:"),
			theme.ValueStyle.Render(m.config.Name))
	}

	// Build env box views
	var boxViews []string
	for i := range m.envBoxes {
		focused := i == m.focusedEnv
		inside := focused && m.insideBox
		boxView := zone.Mark(EnvBoxZoneID(i), m.envBoxes[i].View(boxWidth, focused, inside))
		boxViews = append(boxViews, boxView)
	}

	// Help text
	var helpText string
	if m.insideBox {
		helpText = theme.DimStyle.Render("  j/k navigate  |  enter open  |  esc back  |  ctrl+p actions")
	} else {
		helpText = theme.DimStyle.Render("  j/k navigate  |  enter drill into  |  ctrl+p actions  |  tab sidebar")
	}

	// Assemble all env content lines
	var allLines []string
	allLines = append(allLines, title, sep, subtitle)
	if workerLine != "" {
		allLines = append(allLines, workerLine)
	}
	allLines = append(allLines, "") // spacer

	// Triggers item (project-level, above env boxes)
	{
		cronCount := 0
		if m.config != nil {
			cronCount = len(m.config.CronTriggers())
		}
		triggerCursor := "  "
		triggerStyle := theme.NormalItemStyle
		if !m.insideBox && m.focusedEnv == -1 {
			triggerCursor = theme.SelectedItemStyle.Render("> ")
			triggerStyle = theme.SelectedItemStyle
		}
		triggerLabel := triggerStyle.Render(fmt.Sprintf("Triggers (%d)", cronCount))
		navArrow := " " + theme.ActionNavArrowStyle.Render("\u2192") // →
		allLines = append(allLines, fmt.Sprintf("%s%s%s", triggerCursor, triggerLabel, navArrow))
		allLines = append(allLines, "") // spacer
	}

	// Add box views (each box is multi-line, split by \n)
	for _, bv := range boxViews {
		boxLines := strings.Split(bv, "\n")
		allLines = append(allLines, boxLines...)
		allLines = append(allLines, "") // spacer between boxes
	}

	allLines = append(allLines, helpText)

	// Apply vertical scrolling to the env section
	visibleHeight := envPaneHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	maxScroll := len(allLines) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	offset := m.scrollY
	if offset > maxScroll {
		offset = maxScroll
	}
	endIdx := offset + visibleHeight
	if endIdx > len(allLines) {
		endIdx = len(allLines)
	}

	visible := allLines[offset:endIdx]

	// Pad env section to exact height
	for len(visible) < envPaneHeight {
		visible = append(visible, "")
	}

	var content string
	if cmdPaneHeight > 0 {
		// Split view: env boxes on top, command output pane on bottom
		envContent := strings.Join(visible, "\n")
		cmdContent := m.activeCmdPane.View(cmdPaneHeight, m.width-4, m.spinner.View())
		content = envContent + "\n" + cmdContent
	} else {
		content = strings.Join(visible, "\n")
	}

	return m.renderBorder(content, contentHeight)
}

// renderBorder wraps content in the detail panel border style.
func (m Model) renderBorder(content string, contentHeight int) string {
	// Truncate to contentHeight lines
	lines := strings.Split(content, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
		content = strings.Join(lines, "\n")
	}

	borderStyle := theme.BorderStyle
	if m.focused {
		borderStyle = theme.ActiveBorderStyle
	}
	return borderStyle.
		Width(m.width - 2).
		Height(contentHeight).
		Render(content)
}
