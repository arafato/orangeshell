package header

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// Model represents the header bar at the top of the TUI.
type Model struct {
	accountName string
	authMethod  config.AuthMethod
	width       int
}

// New creates a new header model.
func New(accountName string, authMethod config.AuthMethod) Model {
	return Model{
		accountName: accountName,
		authMethod:  authMethod,
	}
}

// SetWidth updates the header width.
func (m *Model) SetWidth(w int) {
	m.width = w
}

// SetAccount updates the displayed account name.
func (m *Model) SetAccount(name string) {
	m.accountName = name
}

// View renders the header bar.
func (m Model) View() string {
	authLabel := ""
	switch m.authMethod {
	case config.AuthMethodAPIKey:
		authLabel = "API Key"
	case config.AuthMethodAPIToken:
		authLabel = "API Token"
	case config.AuthMethodOAuth:
		authLabel = "OAuth"
	}

	left := theme.HeaderStyle.Render(" orangeshell ")
	right := ""
	if m.accountName != "" {
		right = lipgloss.NewStyle().
			Foreground(theme.ColorWhite).
			Background(theme.ColorDarkGray).
			Padding(0, 1).
			Render(fmt.Sprintf("%s  |  %s", m.accountName, authLabel))
	}

	// Fill the gap between left and right
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	fill := lipgloss.NewStyle().
		Background(theme.ColorDarkGray).
		Render(fmt.Sprintf("%*s", gap, ""))

	return lipgloss.JoinHorizontal(lipgloss.Top, left, fill, right)
}
