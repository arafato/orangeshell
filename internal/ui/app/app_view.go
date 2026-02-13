package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	"github.com/oarafat/orangeshell/version"
)

// View renders the full application.
func (m Model) View() string {
	switch m.phase {
	case PhaseSetup:
		return m.setup.View()
	case PhaseDashboard:
		return m.viewDashboard()
	}
	return ""
}

func (m Model) viewDashboard() string {
	// overlayEntry pairs an "is-active" flag with the view func that renders it.
	type overlayEntry struct {
		active bool
		view   func() string
	}

	w, h := m.width, m.height
	overlays := []overlayEntry{
		{m.wrangler.IsVersionPickerActive(), func() string { return m.wrangler.VersionPickerView(w, h) }},
		{m.showLauncher, func() string { return m.launcher.View(w, h) }},
		{m.showSearch, func() string { return m.search.View(w, h) }},
		{m.showBindings, func() string { return m.bindingsPopup.View(w, h) }},
		{m.showDeployAllPopup, func() string { return m.deployAllPopup.View(w, h) }},
		{m.showEnvPopup, func() string { return m.envPopup.View(w, h) }},
		{m.showDeletePopup, func() string { return m.deletePopup.View(w, h) }},
		{m.showProjectPopup, func() string { return m.projectPopup.View(w, h) }},
		{m.showRemoveProjectPopup, func() string { return m.removeProjectPopup.View(w, h) }},
		{m.showActions, func() string { return m.actionsPopup.View(w, h) }},
	}

	for _, o := range overlays {
		if o.active {
			bg := dimContent(m.renderDashboardContent())
			return overlayCenter(bg, o.view(), w, h)
		}
	}

	return m.renderDashboardContent()
}

// renderDashboardContent renders the normal dashboard (header, content, help, status).
func (m Model) renderDashboardContent() string {
	headerView := m.header.View()

	var content string
	switch m.viewState {
	case ViewWrangler:
		content = m.wrangler.View()
	case ViewServiceList, ViewServiceDetail:
		content = m.detail.View()
	case ViewEnvVars:
		contentHeight := m.height - 2 // header + help bar
		if contentHeight < 1 {
			contentHeight = 1
		}
		content = m.envvarsView.View(m.width, contentHeight)
	case ViewTriggers:
		contentHeight := m.height - 2 // header + help bar
		if contentHeight < 1 {
			contentHeight = 1
		}
		content = m.triggersView.View(m.width, contentHeight)
	}

	helpText := m.renderHelp()

	parts := []string{headerView, content, helpText}
	if m.toastMsg != "" && time.Now().Before(m.toastExpiry) {
		parts = append(parts, theme.SuccessStyle.Render(fmt.Sprintf(" ✓ %s ", m.toastMsg)))
	} else if m.err != nil {
		parts = append(parts, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s ", m.err.Error())))
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// dimContent applies ANSI dim (faint) styling to every line of the rendered string.
// It wraps each line with the SGR dim code (\033[2m) and a full reset at the end.
// This causes the terminal to render all text at reduced brightness.
func dimContent(s string) string {
	const dimOn = "\033[2m"
	const reset = "\033[0m"
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// Wrap the entire line: dim-on at the start, reset at the end.
		// Any inner resets in the line will cancel dimming mid-line, so we
		// also inject dim-on after every reset sequence we find.
		lines[i] = dimOn + strings.ReplaceAll(line, reset, reset+dimOn) + reset
	}
	return strings.Join(lines, "\n")
}

// overlayCenter composites a foreground popup centered on top of a dimmed background.
// Lines outside the popup's Y range show the dimmed background as-is.
// Lines inside the popup's Y range splice the popup content into the dimmed
// background using ANSI-aware string truncation, preserving the dimmed background
// on the left and right sides of the popup.
func overlayCenter(bg, fg string, termWidth, termHeight int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	// Pad or truncate background to exactly termHeight lines
	for len(bgLines) < termHeight {
		bgLines = append(bgLines, "")
	}
	bgLines = bgLines[:termHeight]

	fgHeight := len(fgLines)
	fgWidth := 0
	for _, line := range fgLines {
		if w := lipgloss.Width(line); w > fgWidth {
			fgWidth = w
		}
	}

	startY := (termHeight - fgHeight) / 2
	startX := (termWidth - fgWidth) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	result := make([]string, termHeight)
	for i := 0; i < termHeight; i++ {
		if i >= startY && i < startY+fgHeight {
			fgIdx := i - startY
			// Splice: dimmed-bg-left + popup-line + dimmed-bg-right
			bgLeft := ansi.Truncate(bgLines[i], startX, "")
			bgRight := ansi.TruncateLeft(bgLines[i], startX+fgWidth, "")
			result[i] = bgLeft + fgLines[fgIdx] + bgRight
		} else {
			result[i] = bgLines[i]
		}
	}

	return strings.Join(result, "\n")
}

func (m Model) renderHelp() string {
	type helpEntry struct {
		key  string
		desc string
	}

	var entries []helpEntry

	switch m.viewState {
	case ViewWrangler:
		if m.wrangler.IsParallelTailActive() {
			entries = []helpEntry{
				{"esc", "back"},
				{"j/k", "scroll"},
			}
		} else if m.wrangler.IsEmpty() {
			entries = []helpEntry{
				{"j/k", "navigate"},
				{"enter", "select"},
				{"ctrl+l", "resources"},
				{"[/]", "accounts"},
				{"q", "quit"},
			}
		} else {
			entries = []helpEntry{
				{"ctrl+l", "resources"},
				{"ctrl+k", "search"},
				{"ctrl+p", "actions"},
				{"ctrl+n", "bindings"},
				{"[/]", "accounts"},
			}
			if m.wrangler.HasConfig() && !m.wrangler.IsOnProjectList() {
				entries = append(entries, helpEntry{"t", "tail"})
				if m.wrangler.InsideBox() {
					entries = append(entries, helpEntry{"d", "del binding"})
				} else {
					entries = append(entries, helpEntry{"d", "del env"})
				}
			}
			entries = append(entries, helpEntry{"q", "quit"})
		}
	case ViewEnvVars:
		entries = []helpEntry{
			{"esc", "back"},
			{"enter", "edit/add"},
			{"d", "delete"},
			{"ctrl+h", "home"},
		}
	case ViewTriggers:
		entries = []helpEntry{
			{"esc", "back"},
			{"a", "add"},
			{"d", "delete"},
			{"ctrl+h", "home"},
		}
	case ViewServiceList:
		entries = []helpEntry{
			{"esc", "back"},
			{"ctrl+h", "home"},
			{"ctrl+l", "resources"},
			{"ctrl+k", "search"},
			{"enter", "detail"},
		}
		// Show delete shortcut for services that implement the Deleter interface
		if s := m.registry.Get(m.detail.Service()); s != nil {
			if _, ok := s.(svc.Deleter); ok {
				entries = append(entries, helpEntry{"d", "delete"})
			}
		}
		entries = append(entries, helpEntry{"[/]", "accounts"})
	case ViewServiceDetail:
		entries = []helpEntry{
			{"esc", "back"},
			{"ctrl+h", "home"},
			{"ctrl+p", "actions"},
			{"ctrl+k", "search"},
		}
		if m.detail.IsWorkersDetail() {
			entries = append(entries, helpEntry{"t", "tail"})
		}
		entries = append(entries, helpEntry{"[/]", "accounts"})
	}

	var parts []string
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s %s",
			lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render(e.key),
			theme.DimStyle.Render(e.desc)))
	}

	help := ""
	for i, p := range parts {
		if i > 0 {
			help += theme.DimStyle.Render("  |  ")
		}
		help += p
	}

	// Right-align the version string
	ver := theme.DimStyle.Render(version.GetVersion())
	helpWidth := ansi.StringWidth(help)
	verWidth := ansi.StringWidth(ver)
	gap := m.width - helpWidth - verWidth - 4 // 4 for HelpBarStyle padding
	if gap < 2 {
		gap = 2
	}
	help += strings.Repeat(" ", gap) + ver

	return theme.HelpBarStyle.Render(help)
}
