// Package tabbar implements a horizontal tab bar component with mouse and
// keyboard support via bubblezone. Tabs are rendered as rounded-border boxes,
// with the active tab filled in orange and inactive tabs in dark gray.
package tabbar

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// TabID identifies a top-level tab.
type TabID int

const (
	TabOperations    TabID = iota // Monorepo / project view
	TabMonitoring                 // Live tail grid
	TabResources                  // Service list + detail
	TabConfiguration              // Env vars, triggers, bindings, environments
	TabAI                         // AI-powered log analysis
	tabCount                      // sentinel — must be last
)

// TabCount returns the number of defined tabs.
func TabCount() int { return int(tabCount) }

// Label returns the display label for each tab.
func (t TabID) Label() string {
	switch t {
	case TabOperations:
		return "Operations"
	case TabMonitoring:
		return "Monitoring"
	case TabResources:
		return "Resources"
	case TabConfiguration:
		return "Configuration"
	case TabAI:
		return "AI"
	}
	return "?"
}

// ZoneID returns the bubblezone marker ID for this tab.
func (t TabID) ZoneID() string {
	return fmt.Sprintf("tab-%d", int(t))
}

// All returns all tab IDs in order.
func All() []TabID {
	tabs := make([]TabID, tabCount)
	for i := range tabs {
		tabs[i] = TabID(i)
	}
	return tabs
}

// Styles for tab rendering.
var (
	activeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.ColorOrange).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.ColorOrange).
			Padding(0, 1)

	inactiveStyle = lipgloss.NewStyle().
			Foreground(theme.ColorGray).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.ColorDarkGray).
			Padding(0, 1)

	hoverStyle = lipgloss.NewStyle().
			Foreground(theme.ColorWhite).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.ColorGray).
			Padding(0, 1)
)

// View renders the tab bar. activeTab is the currently selected tab.
// hoverTab is the tab under the mouse cursor (-1 for none).
func View(activeTab TabID, hoverTab TabID) string {
	var tabs []string
	for _, t := range All() {
		label := fmt.Sprintf("%d %s", int(t)+1, t.Label())
		var rendered string
		switch {
		case t == activeTab:
			rendered = activeStyle.Render(label)
		case t == hoverTab:
			rendered = hoverStyle.Render(label)
		default:
			rendered = inactiveStyle.Render(label)
		}
		tabs = append(tabs, zone.Mark(t.ZoneID(), rendered))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}
