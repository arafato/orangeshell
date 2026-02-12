package launcher

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// LaunchServiceMsg is sent when the user selects a service to navigate to.
type LaunchServiceMsg struct {
	ServiceName string // "Workers", "KV", "R2", etc., or "" for wrangler home
}

// CloseMsg is sent when the launcher should be closed.
type CloseMsg struct{}

type serviceEntry struct {
	Name string
	Icon string
}

// defaultServices are the Cloudflare developer platform services (excluding Wrangler).
var defaultServices = []serviceEntry{
	{Name: "Workers", Icon: "W"},
	{Name: "KV", Icon: "K"},
	{Name: "R2", Icon: "R"},
	{Name: "D1", Icon: "D"},
	{Name: "Pages", Icon: "P"},
	{Name: "Queues", Icon: "Q"},
	{Name: "Hyperdrive", Icon: "H"},
	{Name: "Env Variables", Icon: "E"},
}

// Model represents the service launcher overlay.
type Model struct {
	results   []serviceEntry // filtered results (home entry + matching services)
	query     string
	cursor    int
	width     int
	height    int
	homeLabel string // e.g. "Wrangler Home" or "Home: express-d1-app"
}

// New creates a new service launcher.
// homeLabel should be the wrangler project name (or "" for default).
func New(projectName string) Model {
	homeLabel := "Wrangler Home"
	if projectName != "" {
		homeLabel = fmt.Sprintf("Home: %s", projectName)
	}

	m := Model{
		homeLabel: homeLabel,
	}
	m.filter()
	return m
}

// filter rebuilds the results list based on the current query.
func (m *Model) filter() {
	q := strings.ToLower(m.query)

	m.results = nil

	// Home entry — always first when no query or when it matches
	if q == "" || strings.Contains(strings.ToLower(m.homeLabel), q) || strings.Contains("home wrangler", q) {
		m.results = append(m.results, serviceEntry{Name: "", Icon: "⚙"})
	}

	for _, s := range defaultServices {
		if q == "" || strings.Contains(strings.ToLower(s.Name), q) {
			m.results = append(m.results, s)
		}
	}

	// Clamp cursor
	if m.cursor >= len(m.results) {
		m.cursor = len(m.results) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Update handles key events for the launcher.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+l":
			return m, func() tea.Msg { return CloseMsg{} }

		case "enter":
			if len(m.results) > 0 && m.cursor < len(m.results) {
				entry := m.results[m.cursor]
				return m, func() tea.Msg {
					return LaunchServiceMsg{ServiceName: entry.Name}
				}
			}

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.results)-1 {
				m.cursor++
			}

		case "backspace":
			if len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
				m.filter()
			}

		default:
			// Single printable character — append to query
			if len(msg.String()) == 1 && msg.String() >= " " {
				m.query += msg.String()
				m.filter()
			}
		}
	}
	return m, nil
}

// View renders the launcher as a centered overlay popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth / 3
	if popupWidth < 30 {
		popupWidth = 30
	}
	if popupWidth > 50 {
		popupWidth = 50
	}

	title := theme.TitleStyle.Render("  Resources")
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", popupWidth-4))

	// Input line
	inputLine := fmt.Sprintf("  > %s", m.query)
	if m.query == "" {
		inputLine = "  > " + theme.DimStyle.Render("type to filter...")
	}

	var bodyLines []string

	for i, entry := range m.results {
		cursor := "  "
		if i == m.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		var label string
		if entry.Name == "" {
			// Home entry
			label = m.homeLabel
		} else {
			label = entry.Name
		}

		icon := entry.Icon
		style := theme.ActionItemStyle
		if i == m.cursor {
			style = theme.SelectedItemStyle
		}

		line := fmt.Sprintf("%s  %s  %s",
			cursor,
			theme.DimStyle.Render(icon),
			style.Render(label))
		bodyLines = append(bodyLines, line)
	}

	if len(m.results) == 0 {
		bodyLines = append(bodyLines, theme.DimStyle.Render("  No matches"))
	}

	help := theme.DimStyle.Render("  esc close  |  enter select  |  j/k navigate")

	lines := []string{title, sep, inputLine, sep}
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
