package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/ui/actions"
	uiconfig "github.com/oarafat/orangeshell/internal/ui/config"
	"github.com/oarafat/orangeshell/internal/ui/helppopup"
	"github.com/oarafat/orangeshell/internal/ui/launcher"
	"github.com/oarafat/orangeshell/internal/ui/search"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
)

// handleOverlayMsg handles messages from search, actions, and launcher overlays.
// Returns (model, cmd, handled).
func (m *Model) handleOverlayMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	// --- Search messages ---

	case search.NavigateMsg:
		m.showSearch = false
		// Navigate to the selected service and resource
		return *m, m.navigateTo(msg.ServiceName, msg.ResourceID), true

	case search.CloseMsg:
		m.showSearch = false
		return *m, nil, true

	// --- Action popup messages ---

	case actions.SelectMsg:
		m.showActions = false
		return *m, m.handleActionSelect(msg.Item), true

	case actions.CloseMsg:
		m.showActions = false
		return *m, nil, true

	// --- Help popup messages ---

	case helppopup.CloseMsg:
		m.showHelpPopup = false
		return *m, nil, true

	// --- Launcher messages ---

	case launcher.LaunchServiceMsg:
		m.showLauncher = false
		if msg.ServiceName == "" {
			// Go home
			m.activeTab = tabbar.TabOperations
			m.viewState = ViewWrangler
			if cmd := m.refreshDeploymentsIfStale(); cmd != nil {
				return *m, cmd, true
			}
			return *m, nil, true
		}
		if msg.ServiceName == "Env Variables" {
			m.syncConfigProjects()
			m.configView.SetCategory(uiconfig.CategoryEnvVars)
			m.activeTab = tabbar.TabConfiguration
			return *m, nil, true
		}
		if msg.ServiceName == "Triggers" {
			m.syncConfigProjects()
			m.configView.SetCategory(uiconfig.CategoryTriggers)
			m.activeTab = tabbar.TabConfiguration
			return *m, nil, true
		}
		return *m, m.navigateToService(msg.ServiceName), true

	case launcher.CloseMsg:
		m.showLauncher = false
		return *m, nil, true
	}

	return *m, nil, false
}
