package app

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/ui/cicdpopup"
)

// handleCICDMsg handles all CI/CD wizard popup messages.
// Returns (model, cmd, handled).
func (m *Model) handleCICDMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case cicdpopup.CloseMsg:
		m.showCICDPopup = false
		return *m, nil, true

	case cicdpopup.CheckInstallMsg:
		return *m, m.checkCICDInstallCmd(msg), true

	case cicdpopup.CheckInstallDoneMsg:
		if msg.Err != nil && !api.IsAuthError(msg.Err) {
			// Non-auth error — set the dashboard URL before forwarding
			accountID := m.registry.ActiveAccountID()
			url := fmt.Sprintf("https://dash.cloudflare.com/%s/workers/builds", accountID)
			m.cicdPopup.SetDashboardURL(url)
		}
		var cmd tea.Cmd
		m.cicdPopup, cmd = m.cicdPopup.Update(msg)
		return *m, cmd, true

	case cicdpopup.SetupCICDMsg:
		return *m, m.setupCICDCmd(msg), true

	case cicdpopup.SetupCICDDoneMsg:
		var cmd tea.Cmd
		m.cicdPopup, cmd = m.cicdPopup.Update(msg)
		return *m, cmd, true

	case cicdpopup.DoneMsg:
		m.showCICDPopup = false
		m.setToast("CI/CD connected for " + msg.ScriptName)
		return *m, toastTick(), true
	}
	return *m, nil, false
}

// checkCICDInstallCmd calls GetConfigAutofill to check if the GitHub/GitLab
// installation exists and to fetch auto-detected build configuration.
func (m *Model) checkCICDInstallCmd(msg cicdpopup.CheckInstallMsg) tea.Cmd {
	client := m.getBuildsClient()
	if client == nil {
		return func() tea.Msg {
			return cicdpopup.CheckInstallDoneMsg{
				Err: fmt.Errorf("no API credentials available"),
			}
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		autofill, err := client.GetConfigAutofill(ctx, msg.Provider, msg.ProviderAccountID, msg.RepoID, msg.Branch, msg.RootDir)
		return cicdpopup.CheckInstallDoneMsg{
			Autofill: autofill,
			Err:      err,
		}
	}
}

// setupCICDCmd performs the full CI/CD setup:
// 1. Create/update repo connection (PUT)
// 2. Create the trigger (POST)
func (m *Model) setupCICDCmd(msg cicdpopup.SetupCICDMsg) tea.Cmd {
	client := m.getBuildsClient()
	if client == nil {
		return func() tea.Msg {
			return cicdpopup.SetupCICDDoneMsg{
				Err: fmt.Errorf("no API credentials available"),
			}
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Step 1: Create/update repo connection
		conn, err := client.PutRepoConnection(ctx, api.RepoConnectionRequest{
			ProviderAccountID:   msg.ProviderAccountID,
			ProviderAccountName: msg.ProviderAccountID, // use owner as display name
			ProviderType:        msg.Provider,
			RepoID:              msg.RepoName,
			RepoName:            msg.RepoName,
		})
		if err != nil {
			return cicdpopup.SetupCICDDoneMsg{Err: fmt.Errorf("creating repo connection: %w", err)}
		}

		// Step 2: Create trigger
		trigger, err := client.CreateTrigger(ctx, api.TriggerCreateRequest{
			TriggerName:    msg.TriggerName,
			ScriptID:       msg.ScriptName,
			RepoConnUUID:   conn.UUID,
			BranchIncludes: msg.BranchIncludes,
			BranchExcludes: msg.BranchExcludes,
			PathIncludes:   msg.PathIncludes,
			PathExcludes:   msg.PathExcludes,
			BuildCommand:   msg.BuildCommand,
			DeployCommand:  msg.DeployCommand,
			RootDirectory:  msg.RootDirectory,
			BuildCaching:   msg.BuildCaching,
		})
		if err != nil {
			return cicdpopup.SetupCICDDoneMsg{Err: fmt.Errorf("creating trigger: %w", err)}
		}

		return cicdpopup.SetupCICDDoneMsg{Trigger: trigger}
	}
}
