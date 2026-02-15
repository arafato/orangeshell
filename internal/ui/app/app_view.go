package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	"github.com/oarafat/orangeshell/internal/ui/monitoring"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	"github.com/oarafat/orangeshell/version"
)

// View renders the full application.
// zone.Scan() strips bubblezone markers and records zone positions for mouse hit-testing.
func (m Model) View() string {
	switch m.phase {
	case PhaseSetup:
		return m.setup.View()
	case PhaseDashboard:
		return zone.Scan(m.viewDashboard())
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

// tabBarHeight is the number of terminal lines the tab bar occupies.
// The inactive tabs have a rounded border (top + bottom) adding 2 lines,
// plus the content row itself = 3 lines total.
const tabBarHeight = 3

// renderDashboardContent renders the normal dashboard (header, tab bar, content, help, status).
func (m Model) renderDashboardContent() string {
	headerView := m.header.View()
	tabBarView := tabbar.View(m.activeTab, m.hoverTab)

	content := m.renderTabContent()

	helpText := m.renderHelp()

	parts := []string{headerView, tabBarView, content, helpText}
	if m.toastMsg != "" && time.Now().Before(m.toastExpiry) {
		parts = append(parts, theme.SuccessStyle.Render(fmt.Sprintf(" ✓ %s ", m.toastMsg)))
	} else if m.err != nil {
		parts = append(parts, theme.ErrorStyle.Render(fmt.Sprintf(" Error: %s ", m.err.Error())))
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderTabContent renders the content area for the currently active tab.
func (m Model) renderTabContent() string {
	switch m.activeTab {
	case tabbar.TabOperations:
		return m.renderOperationsTab()
	case tabbar.TabMonitoring:
		return m.renderMonitoringTab()
	case tabbar.TabResources:
		return m.renderResourcesTab()
	case tabbar.TabConfiguration:
		return m.renderConfigurationTab()
	case tabbar.TabAI:
		return m.renderAITab()
	}
	return ""
}

// renderOperationsTab renders the Operations tab content.
func (m Model) renderOperationsTab() string {
	if m.viewState == ViewWrangler {
		content := m.wrangler.View()
		// Pad to full content height so the help bar stays at the bottom
		contentHeight := m.height - 1 - tabBarHeight - 1 // header + tab bar + help bar
		if contentHeight < 1 {
			contentHeight = 1
		}
		lines := strings.Split(content, "\n")
		for len(lines) < contentHeight {
			lines = append(lines, "")
		}
		if len(lines) > contentHeight {
			lines = lines[:contentHeight]
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// renderMonitoringTab renders the Monitoring tab (live tail output).
func (m Model) renderMonitoringTab() string {
	return m.monitoring.View()
}

// renderResourcesTab renders the Resources tab content (service list / detail).
func (m Model) renderResourcesTab() string {
	switch m.viewState {
	case ViewServiceList, ViewServiceDetail:
		return m.detail.View()
	}
	return m.renderPlaceholderTab("Resources", "Press ctrl+l to browse services")
}

// renderConfigurationTab renders the Configuration tab content.
func (m Model) renderConfigurationTab() string {
	contentHeight := m.height - 1 - tabBarHeight - 1 // header + tab bar + help bar
	if contentHeight < 1 {
		contentHeight = 1
	}
	// Legacy views (opened from Operations tab via ShowEnvVarsMsg/ShowTriggersMsg)
	switch m.viewState {
	case ViewEnvVars:
		return m.envvarsView.View(m.width, contentHeight)
	case ViewTriggers:
		return m.triggersView.View(m.width, contentHeight)
	}
	// Unified configuration tab
	return m.configView.View()
}

// renderAITab renders the AI tab content.
func (m Model) renderAITab() string {
	return m.aiTab.View()
}

// renderPlaceholderTab renders a centered placeholder message for tabs that are not yet implemented.
func (m Model) renderPlaceholderTab(title, subtitle string) string {
	contentHeight := m.height - 1 - tabBarHeight - 1 // header + tab bar + help bar
	if contentHeight < 1 {
		contentHeight = 1
	}

	titleText := theme.TitleStyle.Render(title)
	subtitleText := theme.DimStyle.Render(subtitle)

	block := lipgloss.JoinVertical(lipgloss.Center, titleText, "", subtitleText)
	return lipgloss.Place(m.width, contentHeight, lipgloss.Center, lipgloss.Center, block)
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

// helpEntry is a key-description pair for the help bar.
type helpEntry struct {
	key  string
	desc string
}

func (m Model) renderHelp() string {
	// Tab navigation hint — always first.
	entries := []helpEntry{
		{"1-5", "tabs"},
	}

	switch m.activeTab {
	case tabbar.TabOperations:
		entries = append(entries, m.renderOperationsHelp()...)
	case tabbar.TabMonitoring:
		entries = append(entries, m.renderMonitoringHelp()...)
	case tabbar.TabResources:
		entries = append(entries, m.renderResourcesHelp()...)
	case tabbar.TabConfiguration:
		entries = append(entries, m.renderConfigurationHelp()...)
	case tabbar.TabAI:
		entries = append(entries, m.renderAIHelp()...)
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

// renderOperationsHelp returns the context-sensitive help entries for the Operations tab.
func (m Model) renderOperationsHelp() []helpEntry {

	switch m.viewState {
	case ViewWrangler:
		if m.wrangler.IsEmpty() {
			return []helpEntry{
				{"j/k", "navigate"},
				{"enter", "select"},
				{"ctrl+l", "resources"},
				{"[/]", "accounts"},
				{"q", "quit"},
			}
		}
		entries := []helpEntry{
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
		return entries
	}
	return nil
}

// renderResourcesHelp returns the context-sensitive help entries for the Resources tab.
func (m Model) renderResourcesHelp() []helpEntry {
	// Dropdown open — show dropdown-specific help
	if m.detail.DropdownOpen() {
		return []helpEntry{
			{"j/k", "navigate"},
			{"enter", "select"},
			{"esc", "close"},
		}
	}

	switch m.detail.Focus() {
	case detail.FocusList:
		// Preview mode — list pane focused, detail auto-previews on cursor movement
		entries := []helpEntry{
			{"s", "service"},
			{"j/k", "navigate"},
		}
		if m.detail.InDetailView() && m.detail.ActiveServiceMode() == detail.ReadWrite {
			entries = append(entries, helpEntry{"enter", "interact"})
		}
		if s := m.registry.Get(m.detail.Service()); s != nil {
			if _, ok := s.(svc.Deleter); ok {
				entries = append(entries, helpEntry{"d", "delete"})
			}
		}
		entries = append(entries, helpEntry{"ctrl+k", "search"}, helpEntry{"[/]", "accounts"}, helpEntry{"q", "quit"})
		return entries
	case detail.FocusDetail:
		// Interactive mode — detail pane focused for scrolling/editing
		entries := []helpEntry{
			{"esc", "back"},
			{"j/k", "scroll"},
		}
		if m.detail.IsWorkersDetail() {
			entries = append(entries, helpEntry{"t", "tail"})
		}
		entries = append(entries, helpEntry{"ctrl+k", "search"}, helpEntry{"[/]", "accounts"}, helpEntry{"q", "quit"})
		return entries
	}

	// Placeholder state (no service loaded)
	return []helpEntry{
		{"s", "service"},
		{"ctrl+l", "resources"},
		{"ctrl+k", "search"},
		{"[/]", "accounts"},
		{"q", "quit"},
	}
}

// renderConfigurationHelp returns the context-sensitive help entries for the Configuration tab.
func (m Model) renderConfigurationHelp() []helpEntry {
	// Legacy views
	switch m.viewState {
	case ViewEnvVars:
		return []helpEntry{
			{"esc", "back"},
			{"enter", "edit/add"},
			{"d", "delete"},
			{"ctrl+h", "home"},
		}
	case ViewTriggers:
		return []helpEntry{
			{"esc", "back"},
			{"a", "add"},
			{"d", "delete"},
			{"ctrl+h", "home"},
		}
	}
	// Unified config tab — delegate to config model
	cfgHelp := m.configView.HelpEntries()
	entries := make([]helpEntry, len(cfgHelp))
	for i, h := range cfgHelp {
		entries[i] = helpEntry{key: h.Key, desc: h.Desc}
	}
	return entries
}

// renderMonitoringHelp returns the context-sensitive help entries for the Monitoring tab.
func (m Model) renderMonitoringHelp() []helpEntry {
	if !m.monitoring.HasWorkerTree() && m.monitoring.GridPaneCount() == 0 {
		// No workers available — show generic help
		return []helpEntry{
			{"ctrl+l", "resources"},
			{"ctrl+k", "search"},
			{"[/]", "accounts"},
			{"q", "quit"},
		}
	}

	switch m.monitoring.Focus() {
	case monitoring.FocusLeft:
		entries := []helpEntry{
			{"j/k", "navigate"},
			{"a", "add"},
			{"d", "remove"},
		}
		if m.monitoring.CursorOnDev() {
			entries = append(entries, helpEntry{"c", "cron trigger"})
		}
		entries = append(entries,
			helpEntry{"tab", "grid"},
			helpEntry{"esc", "back"},
			helpEntry{"ctrl+h", "home"},
		)
		return entries
	case monitoring.FocusRight:
		if m.monitoring.GridPaneCount() == 0 {
			return []helpEntry{
				{"tab", "workers"},
				{"esc", "back"},
				{"ctrl+h", "home"},
			}
		}
		return []helpEntry{
			{"h/j/k/l", "navigate"},
			{"t", "toggle"},
			{"ctrl+t", "toggle all"},
			{"tab", "workers"},
			{"esc", "back"},
			{"ctrl+h", "home"},
		}
	}
	return nil
}

// renderAIHelp returns the context-sensitive help entries for the AI tab.
func (m Model) renderAIHelp() []helpEntry {
	aiHelp := m.aiTab.HelpEntries()
	entries := make([]helpEntry, len(aiHelp))
	for i, h := range aiHelp {
		entries[i] = helpEntry{key: h.Key, desc: h.Desc}
	}
	return entries
}
