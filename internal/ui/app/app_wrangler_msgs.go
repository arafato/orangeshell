package app

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	svc "github.com/oarafat/orangeshell/internal/service"
	uiconfig "github.com/oarafat/orangeshell/internal/ui/config"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/monitoring"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
)

// handleWranglerMsg handles all wrangler-related messages.
// Returns (model, cmd, handled).
func (m *Model) handleWranglerMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	// --- Config and project discovery messages ---

	case uiwrangler.ConfigLoadedMsg:
		m.wrangler.SetConfig(msg.Config, msg.Path, msg.Err)
		// Refresh the monitoring worker tree in case we're on the Monitoring tab
		m.refreshMonitoringWorkerTree()
		// Refresh AI file sources so the context panel picks up new projects
		m.refreshAIFileSources()
		// Sync badges on newly created env boxes
		m.syncAccessBadges()
		m.syncCICDBadges()
		// Trigger deployment fetching for single-project environments
		if msg.Err == nil && msg.Config != nil {
			return *m, m.fetchSingleProjectDeployments(msg.Config), true
		}
		return *m, nil, true

	case uiwrangler.EmptyMenuSelectMsg:
		switch msg.Action {
		case "create_project":
			m.wrangler.ActivateDirBrowser(uiwrangler.DirBrowserModeCreate)
		case "open_project":
			m.wrangler.ActivateDirBrowser(uiwrangler.DirBrowserModeOpen)
		}
		return *m, nil, true

	case uiwrangler.ProjectsDiscoveredMsg:
		m.wrangler.SetProjects(msg.Projects, msg.RootName, msg.RootDir)
		// Refresh the monitoring worker tree
		m.refreshMonitoringWorkerTree()
		// Refresh AI file sources so the context panel picks up new projects
		m.refreshAIFileSources()
		// Sync badges on newly created project boxes
		m.syncAccessBadges()
		m.syncCICDBadges()
		// Trigger deployment fetching for all projects
		return *m, m.fetchAllProjectDeployments(), true

	case uiwrangler.LoadConfigPathMsg:
		if m.wrangler.DirBrowserActiveMode() == uiwrangler.DirBrowserModeCreate {
			// User chose a directory to create a new project in
			m.wrangler.CloseDirBrowser()
			m.showProjectPopup = true
			m.projectPopup = projectpopup.New(nil, msg.Path)
			return *m, nil, true
		}
		// User entered a custom path — scan it for wrangler config
		m.wrangler.SetConfigLoading()
		return *m, tea.Batch(m.discoverProjectsFromDir(msg.Path), m.wrangler.SpinnerInit()), true

	// --- Deployment messages ---

	case uiwrangler.EnvDeploymentLoadedMsg:
		if m.isStaleAccount(msg.AccountID) {
			return *m, nil, true
		}
		// Always update (even on error) so DeploymentFetched gets set and
		// the UI can show "Currently not deployed" instead of nothing.
		m.wrangler.SetEnvDeployment(msg.EnvName, msg.Deployment, msg.Subdomain)
		// Cache the deployment data in the registry for instant restore on account switch-back.
		// Cache both successful responses and errors (worker not found = "not deployed").
		if msg.ScriptName != "" {
			m.registry.SetDeploymentCache(msg.ScriptName, displayToDeploymentInfo(msg.Deployment), msg.Subdomain)
		}
		return *m, nil, true

	case uiwrangler.ProjectDeploymentLoadedMsg:
		if m.isStaleAccount(msg.AccountID) {
			return *m, nil, true
		}
		// Always update so DeploymentFetched gets set
		m.wrangler.SetProjectDeployment(msg.ProjectIndex, msg.EnvName, msg.Deployment, msg.Subdomain)
		// Cache the deployment data in the registry.
		// Cache both successful responses and errors (worker not found = "not deployed").
		if msg.ScriptName != "" {
			m.registry.SetDeploymentCache(msg.ScriptName, displayToDeploymentInfo(msg.Deployment), msg.Subdomain)
		}
		return *m, nil, true

	// --- Tail messages (from wrangler view) ---

	case uiwrangler.TailStartMsg:
		// "t" key pressed in wrangler view — start tailing on Monitoring tab
		if m.client == nil {
			return *m, nil, true
		}
		// Stop any existing tail
		m.stopTail()
		// Start tail via monitoring model and switch to Monitoring tab
		m.tailSource = "monitoring"
		m.monitoring.StartSingleTail(msg.ScriptName)
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		return *m, m.startTailCmd(accountID, msg.ScriptName), true

	case uiwrangler.TailStoppedMsg:
		// "t" key pressed while tail is active — stop it
		m.stopTail()
		return *m, nil, true

	// --- Parallel tail messages ---

	case uiwrangler.ParallelTailStartMsg:
		if m.client == nil {
			return *m, nil, true
		}
		// Stop any existing single tail and parallel tails
		m.stopTail()
		m.stopAllParallelTails()
		// Convert wrangler targets to monitoring targets
		monTargets := make([]monitoring.ParallelTailTarget, len(msg.Scripts))
		for i, t := range msg.Scripts {
			monTargets[i] = monitoring.ParallelTailTarget{ScriptName: t.ScriptName, URL: t.URL}
		}
		// Start parallel tailing on Monitoring tab
		m.monitoring.StartParallelTail(msg.EnvName, monTargets)
		m.parallelTailActive = true
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		var cmds []tea.Cmd
		for _, target := range msg.Scripts {
			cmds = append(cmds, m.startParallelTailSessionCmd(accountID, target.ScriptName))
		}
		return *m, tea.Batch(cmds...), true

	case uiwrangler.ParallelTailExitMsg:
		m.stopAllParallelTails()
		return *m, nil, true

	case parallelTailStartedMsg:
		if !m.parallelTailActive {
			// Stale — parallel tail was stopped while session was connecting
			if msg.Session != nil {
				session := msg.Session
				client := m.client
				go func() {
					ctx := context.Background()
					svc.StopTail(ctx, client.CF, session)
				}()
			}
			return *m, nil, true
		}
		m.parallelTailSessions = append(m.parallelTailSessions, msg.Session)
		m.monitoring.ParallelTailSetConnected(msg.ScriptName)
		m.monitoring.ParallelTailSetSessionID(msg.ScriptName, msg.Session.ID)
		return *m, m.waitForParallelTailLines(msg.ScriptName, msg.Session), true

	case parallelTailLogMsg:
		if !m.parallelTailActive {
			return *m, nil, true
		}
		m.monitoring.ParallelTailAppendLines(msg.ScriptName, msg.Lines)
		m.logExporter.WriteLines(msg.ScriptName, msg.Lines)
		// Find the session to continue polling
		for _, s := range m.parallelTailSessions {
			if s.ScriptName == msg.ScriptName {
				return *m, m.waitForParallelTailLines(msg.ScriptName, s), true
			}
		}
		return *m, nil, true

	case parallelTailErrorMsg:
		if !m.parallelTailActive {
			return *m, nil, true
		}
		m.monitoring.ParallelTailSetError(msg.ScriptName, msg.Err)
		return *m, nil, true

	case parallelTailSessionDoneMsg:
		// Channel closed for one session — nothing to do, pane stays with last lines
		return *m, nil, true

	// --- Navigation and action messages ---

	case uiwrangler.NavigateMsg:
		return *m, m.navigateTo(msg.ServiceName, msg.ResourceID), true

	case uiwrangler.ActionMsg:
		return *m, m.startWranglerCmd(msg.Action, msg.EnvName), true

	case uiwrangler.OpenURLMsg:
		return *m, openURL(msg.URL), true

	// --- Version picker messages ---

	case uiwrangler.VersionsFetchedMsg:
		if msg.Err != nil {
			// Show error and close picker
			m.wrangler.CloseVersionPicker()
			m.err = fmt.Errorf("failed to fetch versions: %w", msg.Err)
			return *m, nil, true
		}
		m.wrangler.SetVersions(msg.Versions)
		return *m, m.wrangler.SpinnerInit(), true

	case uiwrangler.DeployVersionMsg:
		m.wrangler.CloseVersionPicker()
		projectName := m.wrangler.FocusedProjectName()
		configPath := m.wrangler.ConfigPath()
		return *m, m.startWranglerCmdWithArgs("versions deploy", projectName, msg.EnvName, configPath, []string{
			fmt.Sprintf("%s@100", msg.VersionID),
			"-y",
		}), true

	case uiwrangler.GradualDeployMsg:
		m.wrangler.CloseVersionPicker()
		pctB := 100 - msg.PercentageA
		projectName := m.wrangler.FocusedProjectName()
		configPath := m.wrangler.ConfigPath()
		return *m, m.startWranglerCmdWithArgs("versions deploy", projectName, msg.EnvName, configPath, []string{
			fmt.Sprintf("%s@%d", msg.VersionA, msg.PercentageA),
			fmt.Sprintf("%s@%d", msg.VersionB, pctB),
			"-y",
		}), true

	case uiwrangler.VersionPickerCloseMsg:
		m.wrangler.CloseVersionPicker()
		return *m, nil, true

	// --- Binding and config navigation messages ---

	case uiwrangler.DeleteBindingRequestMsg:
		m.showDeletePopup = true
		m.deletePopup = deletepopup.NewBindingDelete(msg.ConfigPath, msg.EnvName, msg.BindingName, msg.BindingType, msg.WorkerName)
		return *m, nil, true

	case uiwrangler.ShowEnvVarsMsg:
		m.syncConfigProjects()
		m.configView.SelectProjectByPath(msg.ConfigPath)
		m.configView.SetCategory(uiconfig.CategoryEnvVars)
		m.activeTab = tabbar.TabConfiguration
		return *m, nil, true

	case uiwrangler.ShowTriggersMsg:
		m.syncConfigProjects()
		m.configView.SelectProjectByPath(msg.ConfigPath)
		m.configView.SetCategory(uiconfig.CategoryTriggers)
		m.activeTab = tabbar.TabConfiguration
		return *m, nil, true

	case uiwrangler.NavigateToBindingMsg:
		m.syncConfigProjects()
		m.configView.SelectProjectByPath(msg.ConfigPath)
		m.configView.SetCategory(uiconfig.CategoryBindings)
		m.configView.SelectBindingByName(msg.EnvName, msg.BindingName)
		m.activeTab = tabbar.TabConfiguration
		return *m, nil, true

	// --- Command output messages ---

	case uiwrangler.CmdOutputMsg:
		if msg.IsDevCmd {
			// Dev server output → monitoring grid + log file (no CmdPane)
			m.handleDevOutput(msg.RunnerKey, msg.Line)
		} else {
			// Command output → CmdPane
			m.handleCmdOutput(msg.RunnerKey, msg.Line)
		}
		// Continue reading from the runner
		return *m, m.waitForRunnerOutput(msg.RunnerKey, msg.IsDevCmd), true

	case uiwrangler.CmdDoneMsg:
		if msg.IsDevCmd {
			return *m, m.handleDevDone(msg.RunnerKey, msg.Result), true
		}
		return *m, m.handleCmdDone(msg.RunnerKey, msg.Result), true
	}

	return *m, nil, false
}
