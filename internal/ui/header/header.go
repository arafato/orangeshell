package header

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// Account holds the minimal info needed for the header tabs.
type Account struct {
	ID   string
	Name string
}

// Model represents the header bar at the top of the TUI.
type Model struct {
	accounts   []Account
	activeIdx  int
	hoverIdx   int // -1 means no hover
	authMethod config.AuthMethod
	width      int
}

// AccountZoneID returns the bubblezone marker ID for a header account tab.
func AccountZoneID(idx int) string {
	return fmt.Sprintf("hdr-acct-%d", idx)
}

// New creates a new header model.
func New(authMethod config.AuthMethod) Model {
	return Model{
		authMethod: authMethod,
		hoverIdx:   -1,
	}
}

// SetWidth updates the header width.
func (m *Model) SetWidth(w int) {
	m.width = w
}

// SetAccounts populates the account tabs and sets the active account.
// activeID is the account ID that should be highlighted.
func (m *Model) SetAccounts(accounts []Account, activeID string) {
	m.accounts = accounts
	m.activeIdx = 0
	for i, acc := range accounts {
		if acc.ID == activeID {
			m.activeIdx = i
			break
		}
	}
}

// ActiveAccountID returns the ID of the currently active account.
func (m Model) ActiveAccountID() string {
	if m.activeIdx >= 0 && m.activeIdx < len(m.accounts) {
		return m.accounts[m.activeIdx].ID
	}
	return ""
}

// ActiveAccountName returns the name of the currently active account.
func (m Model) ActiveAccountName() string {
	if m.activeIdx >= 0 && m.activeIdx < len(m.accounts) {
		return m.accounts[m.activeIdx].Name
	}
	return ""
}

// AccountCount returns the number of accounts.
func (m Model) AccountCount() int {
	return len(m.accounts)
}

// NextAccount moves to the next account tab. Returns true if the account changed.
func (m *Model) NextAccount() bool {
	if len(m.accounts) <= 1 {
		return false
	}
	prev := m.activeIdx
	m.activeIdx = (m.activeIdx + 1) % len(m.accounts)
	return m.activeIdx != prev
}

// PrevAccount moves to the previous account tab. Returns true if the account changed.
func (m *Model) PrevAccount() bool {
	if len(m.accounts) <= 1 {
		return false
	}
	prev := m.activeIdx
	m.activeIdx = (m.activeIdx - 1 + len(m.accounts)) % len(m.accounts)
	return m.activeIdx != prev
}

// SetActiveIndex switches to the account at the given index. Returns true if the account changed.
func (m *Model) SetActiveIndex(idx int) bool {
	if idx < 0 || idx >= len(m.accounts) || idx == m.activeIdx {
		return false
	}
	m.activeIdx = idx
	return true
}

// SetHoverIdx sets which account tab the mouse is hovering over (-1 for none).
func (m *Model) SetHoverIdx(idx int) {
	m.hoverIdx = idx
}

// AccountCount returns the number of accounts.
// (Kept for backward compat — the original AccountCount is duplicated below,
// this is the hover-aware variant that also exposes the count.)

// View renders the header bar with account tabs.
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

	// Build account tabs
	tabs := m.renderTabs()

	// Right side: auth method badge
	right := ""
	if authLabel != "" {
		right = lipgloss.NewStyle().
			Foreground(theme.ColorWhite).
			Background(theme.ColorDarkGray).
			Padding(0, 1).
			Render(authLabel)
	}

	// Fill the gap between left+tabs and right
	leftWidth := lipgloss.Width(left) + lipgloss.Width(tabs)
	rightWidth := lipgloss.Width(right)
	gap := m.width - leftWidth - rightWidth
	if gap < 0 {
		gap = 0
	}
	fill := lipgloss.NewStyle().
		Background(theme.ColorDarkGray).
		Render(fmt.Sprintf("%*s", gap, ""))

	return lipgloss.JoinHorizontal(lipgloss.Top, left, tabs, fill, right)
}

// renderTabs builds the account tab strip with zone markers for mouse support.
func (m Model) renderTabs() string {
	if len(m.accounts) == 0 {
		return ""
	}

	activeTab := lipgloss.NewStyle().
		Foreground(theme.ColorOrange).
		Background(theme.ColorDarkGray).
		Bold(true).
		Padding(0, 1)

	inactiveTab := lipgloss.NewStyle().
		Foreground(theme.ColorGray).
		Background(theme.ColorDarkGray).
		Padding(0, 1)

	hoverTab := lipgloss.NewStyle().
		Foreground(theme.ColorWhite).
		Background(theme.ColorDarkGray).
		Padding(0, 1)

	separator := lipgloss.NewStyle().
		Foreground(theme.ColorDarkGray).
		Background(theme.ColorDarkGray).
		Render("│")

	var parts []string
	for i, acc := range m.accounts {
		if i > 0 {
			parts = append(parts, separator)
		}
		name := acc.Name
		if name == "" {
			// Fallback to truncated ID
			name = acc.ID
			if len(name) > 12 {
				name = name[:12] + "…"
			}
		}
		var rendered string
		switch {
		case i == m.activeIdx:
			rendered = activeTab.Render(name)
		case i == m.hoverIdx:
			rendered = hoverTab.Render(name)
		default:
			rendered = inactiveTab.Render(name)
		}
		parts = append(parts, zone.Mark(AccountZoneID(i), rendered))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}
