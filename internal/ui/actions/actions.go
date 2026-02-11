package actions

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// Item represents a single entry in the action popup.
type Item struct {
	Label       string // Display name (e.g. "Tail logs", "MY_KV")
	Description string // Right-side text (e.g. "KV Namespace")
	Section     string // Section header this belongs to ("Actions", "Bindings")
	Action      string // Action identifier (e.g. "tail_toggle"); empty for navigation items
	NavService  string // Target service for navigation (empty if non-navigable)
	NavResource string // Target resource ID for navigation
	Disabled    bool   // True for non-navigable bindings (rendered dimmed, not selectable)
}

// isSelectable returns true if the cursor can land on this item.
func (it Item) isSelectable() bool {
	return !it.Disabled
}

// SelectMsg is sent when the user selects an action or binding.
type SelectMsg struct {
	Item Item
}

// CloseMsg is sent when the popup should be closed.
type CloseMsg struct{}

// Model represents the action popup overlay.
type Model struct {
	title    string
	items    []Item   // all items in display order
	sections []string // unique section names in order, for rendering headers
	cursor   int      // index into items (points to a selectable item)
	width    int
	height   int
}

// New creates a new action popup with the given title and items.
func New(title string, items []Item) Model {
	m := Model{
		title: title,
		items: items,
	}

	// Extract unique sections in order
	seen := make(map[string]bool)
	for _, it := range items {
		if it.Section != "" && !seen[it.Section] {
			seen[it.Section] = true
			m.sections = append(m.sections, it.Section)
		}
	}

	// Set cursor to the first selectable item
	m.cursor = m.nextSelectable(-1)

	return m
}

// nextSelectable returns the index of the next selectable item after idx.
// Returns idx if no selectable item is found.
func (m Model) nextSelectable(idx int) int {
	for i := idx + 1; i < len(m.items); i++ {
		if m.items[i].isSelectable() {
			return i
		}
	}
	return idx
}

// prevSelectable returns the index of the previous selectable item before idx.
// Returns idx if no selectable item is found.
func (m Model) prevSelectable(idx int) int {
	for i := idx - 1; i >= 0; i-- {
		if m.items[i].isSelectable() {
			return i
		}
	}
	return idx
}

// Update handles key events for the popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+p":
			return m, func() tea.Msg { return CloseMsg{} }
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].isSelectable() {
				item := m.items[m.cursor]
				return m, func() tea.Msg { return SelectMsg{Item: item} }
			}
		case "up", "k":
			m.cursor = m.prevSelectable(m.cursor)
		case "down", "j":
			m.cursor = m.nextSelectable(m.cursor)
		}
	}
	return m, nil
}

// View renders the action popup as a centered overlay.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 90 {
		popupWidth = 90
	}

	// Build content
	title := theme.TitleStyle.Render("  " + m.title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", popupWidth-4))

	var bodyLines []string

	// Calculate max label width for alignment
	maxLabelWidth := 0
	for _, it := range m.items {
		if len(it.Label) > maxLabelWidth {
			maxLabelWidth = len(it.Label)
		}
	}
	if maxLabelWidth > 32 {
		maxLabelWidth = 32
	}

	// Render items grouped by section
	for _, section := range m.sections {
		bodyLines = append(bodyLines, theme.ActionSectionStyle.Render("  "+section))

		for i, it := range m.items {
			if it.Section != section {
				continue
			}

			cursor := "  "
			if i == m.cursor {
				cursor = theme.SelectedItemStyle.Render("> ")
			}

			label := fmt.Sprintf("%-*s", maxLabelWidth, it.Label)
			desc := it.Description

			var line string
			if it.Disabled {
				line = fmt.Sprintf("%s  %s  %s",
					cursor,
					theme.ActionDisabledStyle.Render(label),
					theme.ActionDisabledStyle.Render(desc))
			} else if it.NavService != "" {
				// Navigable binding — show arrow indicator
				line = fmt.Sprintf("%s  %s  %s  %s",
					cursor,
					theme.ActionItemStyle.Render(label),
					theme.ActionDescStyle.Render(desc),
					theme.ActionNavArrowStyle.Render("→"))
			} else {
				// Regular action
				line = fmt.Sprintf("%s  %s  %s",
					cursor,
					theme.ActionItemStyle.Render(label),
					theme.ActionDescStyle.Render(desc))
			}
			bodyLines = append(bodyLines, line)
		}

		bodyLines = append(bodyLines, "") // blank line between sections
	}

	// Remove trailing blank line
	if len(bodyLines) > 0 && bodyLines[len(bodyLines)-1] == "" {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}

	// Handle empty state
	if len(m.items) == 0 {
		bodyLines = append(bodyLines, theme.DimStyle.Render("  No actions available"))
	}

	help := theme.DimStyle.Render("  esc close  |  enter select  |  j/k navigate")

	lines := []string{title, sep}
	lines = append(lines, bodyLines...)
	lines = append(lines, sep, help)

	content := strings.Join(lines, "\n")

	popup := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	return popup
}
