package sidebar

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// Service represents a Cloudflare service shown in the sidebar.
type Service struct {
	Name string
	Icon string // simple text icon
}

// DefaultServices returns the list of developer platform services.
func DefaultServices() []Service {
	return []Service{
		{Name: "Wrangler", Icon: "⚙"},
		{Name: "Workers", Icon: "W"},
		{Name: "KV", Icon: "K"},
		{Name: "R2", Icon: "R"},
		{Name: "D1", Icon: "D"},
		{Name: "Pages", Icon: "P"},
		{Name: "Queues", Icon: "Q"},
		{Name: "Hyperdrive", Icon: "H"},
	}
}

// Model represents the sidebar panel.
type Model struct {
	services []Service
	cursor   int
	focused  bool
	width    int
	height   int
}

// New creates a new sidebar model.
func New() Model {
	return Model{
		services: DefaultServices(),
		cursor:   0,
		focused:  true,
	}
}

// SetSize updates the sidebar dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetFocused sets whether the sidebar is the focused pane.
func (m *Model) SetFocused(f bool) {
	m.focused = f
}

// Focused returns whether the sidebar is focused.
func (m Model) Focused() bool {
	return m.focused
}

// SelectedService returns the currently selected service name.
func (m Model) SelectedService() string {
	if m.cursor >= 0 && m.cursor < len(m.services) {
		return m.services[m.cursor].Name
	}
	return ""
}

// SelectedIndex returns the cursor position.
func (m Model) SelectedIndex() int {
	return m.cursor
}

// Services returns the sidebar service list.
func (m Model) Services() []Service {
	return m.services
}

// SetCursor sets the cursor to a specific index (clamped to valid range).
func (m *Model) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.services) {
		idx = len(m.services) - 1
	}
	m.cursor = idx
}

// Update handles key events for the sidebar.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.services)-1 {
				m.cursor++
			}
		}
	}

	return m, nil
}

// View renders the sidebar.
func (m Model) View() string {
	borderStyle := theme.BorderStyle
	if m.focused {
		borderStyle = theme.ActiveBorderStyle
	}

	title := theme.TitleStyle.Render("  Services")
	separator := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", repeatChar("─", m.width-4)))

	var items string
	for i, svc := range m.services {
		cursor := "  "
		style := theme.NormalItemStyle
		if i == m.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			style = theme.SelectedItemStyle
		}

		line := fmt.Sprintf("%s%s %s", cursor,
			lipgloss.NewStyle().Foreground(theme.ColorOrangeDim).Render(svc.Icon),
			style.Render(svc.Name))
		items += line + "\n"
	}

	// Calculate available height for content (subtract border + title + separator)
	contentHeight := m.height - 4 // border top/bottom + title + separator
	if contentHeight < 0 {
		contentHeight = 0
	}

	content := fmt.Sprintf("%s\n%s\n%s", title, separator, items)

	return borderStyle.
		Width(m.width - 2). // subtract border chars
		Height(contentHeight).
		Render(content)
}

func repeatChar(ch string, count int) string {
	if count < 0 {
		count = 0
	}
	result := ""
	for i := 0; i < count; i++ {
		result += ch
	}
	return result
}
