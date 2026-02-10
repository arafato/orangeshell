package search

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// NavigateMsg is sent when the user selects a result and wants to navigate to it.
type NavigateMsg struct {
	ServiceName string
	ResourceID  string
}

// CloseMsg is sent when the search overlay should be closed without navigation.
type CloseMsg struct{}

// Model represents the fuzzy search popup overlay.
type Model struct {
	query      string
	items      []service.Resource // all searchable items
	results    []service.Resource // filtered results
	cursor     int
	width      int
	height     int
	maxResults int
	fetching   int // number of services still being fetched in background
}

// New creates a new search overlay model.
func New() Model {
	return Model{
		maxResults: 12,
	}
}

// SetSize updates the overlay dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetItems updates the searchable items pool and re-filters.
func (m *Model) SetItems(items []service.Resource) {
	m.items = items
	m.filter()
}

// SetFetching sets how many services are still being fetched.
func (m *Model) SetFetching(n int) {
	m.fetching = n
}

// DecrementFetching reduces the pending fetch count by one.
func (m *Model) DecrementFetching() {
	if m.fetching > 0 {
		m.fetching--
	}
}

// Fetching returns whether background fetches are still in progress.
func (m Model) Fetching() bool {
	return m.fetching > 0
}

// Reset clears the search state for a fresh opening.
func (m *Model) Reset() {
	m.query = ""
	m.cursor = 0
	m.filter()
}

// Update handles key events for the search overlay.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+k":
			return m, func() tea.Msg { return CloseMsg{} }
		case "enter":
			if len(m.results) > 0 && m.cursor < len(m.results) {
				r := m.results[m.cursor]
				return m, func() tea.Msg {
					return NavigateMsg{
						ServiceName: r.ServiceType,
						ResourceID:  r.ID,
					}
				}
			}
		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "ctrl+n":
			if m.cursor < len(m.results)-1 {
				m.cursor++
			}
		case "backspace":
			if len(m.query) > 0 {
				m.query = m.query[:len(m.query)-1]
				m.cursor = 0
				m.filter()
			}
		default:
			if len(msg.String()) == 1 {
				m.query += msg.String()
				m.cursor = 0
				m.filter()
			}
		}
	}
	return m, nil
}

// filter applies fuzzy matching to the items based on the current query.
func (m *Model) filter() {
	if m.query == "" {
		// Empty query: show nothing until the user types
		m.results = nil
		return
	}

	query := strings.ToLower(m.query)
	var matched []service.Resource

	for _, item := range m.items {
		name := strings.ToLower(item.Name)
		if strings.Contains(name, query) {
			matched = append(matched, item)
			if len(matched) >= m.maxResults {
				break
			}
		}
	}

	m.results = matched
}

// View renders the search overlay as a centered popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth / 2
	if popupWidth < 40 {
		popupWidth = 40
	}
	if popupWidth > 80 {
		popupWidth = 80
	}

	// Build the popup content
	title := theme.TitleStyle.Render("  Search")

	// Input field
	inputStyle := lipgloss.NewStyle().
		Foreground(theme.ColorWhite).
		Bold(true)
	promptStyle := lipgloss.NewStyle().
		Foreground(theme.ColorOrange).
		Bold(true)

	input := fmt.Sprintf("%s %s%s",
		promptStyle.Render(">"),
		inputStyle.Render(m.query),
		theme.SelectedItemStyle.Render("_"))

	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", popupWidth-4))

	// Results
	var resultLines []string
	if m.query == "" {
		// Empty query: show hint
		if m.fetching > 0 {
			resultLines = append(resultLines, theme.DimStyle.Render("  Type to search...  ↻ loading services"))
		} else {
			resultLines = append(resultLines, theme.DimStyle.Render("  Type to search..."))
		}
	} else if len(m.results) == 0 && m.fetching > 0 {
		resultLines = append(resultLines, theme.DimStyle.Render("  Searching...  ↻ loading services"))
	} else if len(m.results) == 0 {
		resultLines = append(resultLines, theme.DimStyle.Render("  No matches"))
	} else {
		for i, r := range m.results {
			cursor := "  "
			nameStyle := theme.NormalItemStyle
			if i == m.cursor {
				cursor = theme.SelectedItemStyle.Render("> ")
				nameStyle = theme.SelectedItemStyle
			}

			tag := lipgloss.NewStyle().
				Foreground(theme.ColorOrangeDim).
				Render(fmt.Sprintf("[%s]", r.ServiceType))

			line := fmt.Sprintf("%s%s %s", cursor, tag, nameStyle.Render(r.Name))
			resultLines = append(resultLines, line)
		}
		if m.fetching > 0 {
			resultLines = append(resultLines, theme.DimStyle.Render("  ↻ loading more services..."))
		}
	}

	help := theme.DimStyle.Render("  esc close  |  enter select  |  up/down navigate")

	lines := []string{title, input, sep}
	lines = append(lines, resultLines...)
	lines = append(lines, sep, help)

	content := strings.Join(lines, "\n")

	popup := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	// Center the popup on screen
	return lipgloss.Place(termWidth, termHeight,
		lipgloss.Center, lipgloss.Center,
		popup,
		lipgloss.WithWhitespaceChars(" "),
	)
}
