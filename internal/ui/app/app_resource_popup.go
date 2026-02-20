package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/ui/resourcepopup"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// updateResourcePopup forwards messages to the resource popup when it's active.
func (m Model) updateResourcePopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.resourcePopup, cmd = m.resourcePopup.Update(msg)
	return m, cmd
}

// handleResourcePopupMsg handles messages emitted by the resource creation popup.
func (m *Model) handleResourcePopupMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case resourcepopup.CreateResourceMsg:
		return *m, m.createResourcePopupCmd(msg), true

	case resourcepopup.CreateResourceDoneMsg:
		if m.showResourcePopup {
			var cmd tea.Cmd
			m.resourcePopup, cmd = m.resourcePopup.Update(msg)
			return *m, cmd, true
		}
		return *m, nil, false

	case resourcepopup.CloseMsg:
		m.showResourcePopup = false
		return *m, nil, true

	case resourcepopup.DoneMsg:
		m.showResourcePopup = false
		m.setToast(msg.ServiceName + " resource created")

		// Invalidate and refresh the service cache so the new resource appears
		var cmds []tea.Cmd
		cmds = append(cmds, toastTick())
		if msg.ServiceName != "" {
			cmds = append(cmds, m.loadServiceResources(msg.ServiceName))
		}
		return *m, tea.Batch(cmds...), true
	}

	return *m, nil, false
}

// createResourcePopupCmd runs a wrangler CLI command to create a new resource
// using the ExtraArgs from the resource popup.
func (m Model) createResourcePopupCmd(msg resourcepopup.CreateResourceMsg) tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	return func() tea.Msg {
		result := wcfg.CreateResource(context.Background(), wcfg.CreateResourceCmd{
			ResourceType: msg.ResourceType,
			Name:         msg.Name,
			AccountID:    accountID,
			ExtraArgs:    msg.ExtraArgs,
		})
		return resourcepopup.CreateResourceDoneMsg{
			ResourceType: msg.ResourceType,
			Name:         msg.Name,
			Success:      result.Success,
			Output:       result.Output,
			ResourceID:   result.ResourceID,
		}
	}
}
