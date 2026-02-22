// Package helppopup provides a scrollable read-only help overlay.
// Used to display instructional text (e.g. how to create a fallback token).
package helppopup

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// CloseMsg signals that the help popup should be dismissed.
type CloseMsg struct{}

// Model represents the help popup overlay.
type Model struct {
	title  string
	lines  []string // pre-rendered content lines
	scroll int      // scroll offset
}

// New creates a new help popup with the given title and content lines.
func New(title string, lines []string) Model {
	return Model{
		title: title,
		lines: lines,
	}
}

// Update handles key events for the popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+p":
			return m, func() tea.Msg { return CloseMsg{} }
		case "up", "k":
			if m.scroll > 0 {
				m.scroll--
			}
		case "down", "j":
			m.scroll++
		case "pgup":
			m.scroll -= 10
			if m.scroll < 0 {
				m.scroll = 0
			}
		case "pgdown":
			m.scroll += 10
		}
	}
	return m, nil
}

// View renders the help popup as a centered overlay.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 60 {
		popupWidth = 60
	}
	if popupWidth > 100 {
		popupWidth = 100
	}
	innerWidth := popupWidth - 6 // border (2) + padding (4)

	// Title + separator
	title := theme.TitleStyle.Render("  " + m.title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", innerWidth))

	// Visible height for content (leave room for title, sep, help, borders)
	maxVisible := termHeight/2 - 6
	if maxVisible < 5 {
		maxVisible = 5
	}

	// Clamp scroll
	maxScroll := len(m.lines) - maxVisible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}

	// Slice visible lines
	end := m.scroll + maxVisible
	if end > len(m.lines) {
		end = len(m.lines)
	}
	visible := m.lines[m.scroll:end]

	// Scroll indicator
	scrollHint := ""
	if len(m.lines) > maxVisible {
		scrollHint = theme.DimStyle.Render(fmt.Sprintf("  [%d/%d]", m.scroll+1, len(m.lines)))
	}

	help := theme.DimStyle.Render("  esc close  |  j/k scroll") + scrollHint

	var parts []string
	parts = append(parts, title, sep)
	parts = append(parts, visible...)
	parts = append(parts, sep, help)

	content := strings.Join(parts, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)
}
