package app

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/ui/actions"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/removeprojectpopup"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// buildMonorepoActionsPopup creates a minimal action popup for the monorepo project list.
// Only shows the "Load Wrangler Configuration..." action since per-project actions
// require drilling into a specific project first.
func (m Model) buildMonorepoActionsPopup() actions.Model {
	title := fmt.Sprintf("Monorepo — %s", m.wrangler.RootName())
	var items []actions.Item

	// Monitoring section: single entry that opens an environment sub-popup
	envNames := m.wrangler.AllEnvNames()
	if len(envNames) > 0 && m.client != nil {
		if m.parallelTailActive && m.monitoring.IsParallelTailActive() {
			items = append(items, actions.Item{
				Label:       "Stop Parallel Tail",
				Description: "Stop all live log streams",
				Section:     "Monitoring",
				Action:      "parallel_tail_stop",
			})
		} else {
			items = append(items, actions.Item{
				Label:       "Parallel Tail...",
				Description: "Stream all live logs from an environment",
				Section:     "Monitoring",
				Action:      "parallel_tail",
			})
		}
	}

	// Commands section: Deploy All
	if len(envNames) > 0 && m.client != nil && !m.showDeployAllPopup {
		items = append(items, actions.Item{
			Label:       "Deploy All...",
			Description: "Deploy all projects for an environment",
			Section:     "Commands",
			Action:      "deploy_all",
		})
	}

	// Create project action
	items = append(items, actions.Item{
		Label:       "Create Project",
		Description: "Scaffold a new Worker project in this monorepo",
		Section:     "Configuration",
		Action:      "create_project",
	})

	// Remove project action (only when a project is selected)
	if m.wrangler.SelectedProjectConfig() != nil {
		items = append(items, actions.Item{
			Label:       "Remove Project",
			Description: "Delete project directory from disk",
			Section:     "Configuration",
			Action:      "remove_project",
		})
	}

	// Configuration actions (if the selected project has a config)
	if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
		items = append(items, actions.Item{
			Label:       "Environment Variables",
			Description: "View and edit environment variables",
			Section:     "Configuration",
			Action:      "show_env_vars",
		})
		items = append(items, actions.Item{
			Label:       "Add Environment",
			Description: "Add a new environment to the selected project",
			Section:     "Configuration",
			Action:      "add_environment",
		})
		// Only show delete if there are named environments (not just "default")
		if len(cfg.EnvNames()) > 1 {
			items = append(items, actions.Item{
				Label:       "Delete Environment",
				Description: "Remove an environment from the selected project",
				Section:     "Configuration",
				Action:      "delete_environment",
			})
		}
	}

	items = append(items, actions.Item{
		Label:       "Load Wrangler Configuration...",
		Description: "Browse for a wrangler project directory",
		Section:     "Configuration",
		Action:      "wrangler_load_config",
	})
	return actions.New(title, items)
}

// buildParallelTailEnvPopup creates a sub-popup listing environments for parallel tailing.
func (m Model) buildParallelTailEnvPopup() actions.Model {
	title := "Parallel Tail — Select Environment"
	var items []actions.Item
	for _, envName := range m.wrangler.AllEnvNames() {
		// Count how many workers actually define this environment
		count := 0
		for _, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			if !pc.Config.HasEnv(envName) {
				continue
			}
			if scriptName := pc.Config.ResolvedEnvName(envName); scriptName != "" {
				count++
			}
		}
		items = append(items, actions.Item{
			Label:       envName,
			Description: fmt.Sprintf("%d workers", count),
			Section:     "Environments",
			Action:      "parallel_tail_env_" + envName,
		})
	}
	return actions.New(title, items)
}

// buildDeployAllEnvPopup creates a sub-popup listing environments for Deploy All.
func (m Model) buildDeployAllEnvPopup() actions.Model {
	title := "Deploy All — Select Environment"
	var items []actions.Item
	for _, envName := range m.wrangler.AllEnvNames() {
		count := 0
		for _, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			if !pc.Config.HasEnv(envName) {
				continue
			}
			count++
		}
		items = append(items, actions.Item{
			Label:       envName,
			Description: fmt.Sprintf("%d projects", count),
			Section:     "Environments",
			Action:      "deploy_all_env_" + envName,
		})
	}
	return actions.New(title, items)
}

// buildWranglerActionsPopup creates the action popup for the wrangler view.
// Always includes "Load Wrangler Configuration..." and conditionally includes
// command/binding items when a config is loaded.
func (m Model) buildWranglerActionsPopup() actions.Model {
	envName := m.wrangler.FocusedEnvName()
	title := "Wrangler"
	if m.wrangler.HasConfig() && envName != "" {
		title = fmt.Sprintf("Wrangler — %s", envName)
	}

	var items []actions.Item

	// Wrangler commands section (only when config is loaded)
	if m.wrangler.HasConfig() {
		// Navigate to the worker in the dashboard
		workerName := m.wrangler.Config().ResolvedEnvName(envName)
		if workerName != "" {
			items = append(items, actions.Item{
				Label:       fmt.Sprintf("View Worker: %s", workerName),
				Description: "Open worker in the dashboard",
				Section:     "Navigation",
				NavService:  "Workers",
				NavResource: workerName,
			})
		}

		cmdRunning := m.wrangler.CmdRunning()
		wranglerActions := []string{"deploy", "versions list", "deployments status"}
		for _, action := range wranglerActions {
			items = append(items, actions.Item{
				Label:       wcfg.CommandLabel(action),
				Description: wcfg.CommandDescription(action),
				Section:     "Commands",
				Action:      "wrangler_" + action,
				Disabled:    cmdRunning,
			})
		}

		// Dev server: show "Stop Dev Server" when running, otherwise show the two dev modes
		runningAction := m.wrangler.RunningAction()
		if wcfg.IsDevAction(runningAction) {
			items = append(items, actions.Item{
				Label:       "Stop Dev Server",
				Description: "Stop the running dev server",
				Section:     "Commands",
				Action:      "wrangler_stop_dev",
			})
		} else {
			devActions := []string{"dev", "dev --remote"}
			for _, action := range devActions {
				items = append(items, actions.Item{
					Label:       wcfg.CommandLabel(action),
					Description: wcfg.CommandDescription(action),
					Section:     "Commands",
					Action:      "wrangler_" + action,
					Disabled:    cmdRunning,
				})
			}
		}

		// Delete worker action
		items = append(items, actions.Item{
			Label:       "Delete",
			Description: "Delete the deployed worker for this environment",
			Section:     "Commands",
			Action:      "wrangler_delete",
			Disabled:    cmdRunning,
		})

		// Version deployment actions
		items = append(items, actions.Item{
			Label:       "Deploy Version...",
			Description: "Select a version to deploy at 100%",
			Section:     "Versions",
			Action:      "wrangler_deploy_version",
			Disabled:    cmdRunning,
		})
		items = append(items, actions.Item{
			Label:       "Gradual Deployment...",
			Description: "Split traffic between two versions",
			Section:     "Versions",
			Action:      "wrangler_gradual_deploy",
			Disabled:    cmdRunning,
		})

		// Monitoring section
		if workerName != "" {
			tailLabel := "Tail Logs"
			tailDesc := fmt.Sprintf("Stream live logs from %s", workerName)
			if m.tailSession != nil && m.monitoring.SingleTailActive() {
				tailLabel = "Stop Tail Logs"
				tailDesc = "Stop the live log stream"
			}
			items = append(items, actions.Item{
				Label:       tailLabel,
				Description: tailDesc,
				Section:     "Monitoring",
				Action:      "wrangler_tail_toggle",
				Disabled:    cmdRunning && !m.monitoring.SingleTailActive(),
			})
		}

		// Bindings section (from the focused env box, if inside)
		if m.wrangler.InsideBox() {
			envName := m.wrangler.FocusedEnvName()
			bindings := m.wrangler.Config().EnvBindings(envName)
			if len(bindings) > 0 {
				for _, b := range bindings {
					items = append(items, actions.Item{
						Label:       b.Name,
						Description: b.TypeLabel(),
						Section:     "Bindings",
						NavService:  b.NavService(),
						NavResource: b.ResourceID,
						Disabled:    b.NavService() == "",
					})
				}
			}
		}
	}

	// Configuration section actions (only when config is loaded)
	if m.wrangler.HasConfig() {
		items = append(items, actions.Item{
			Label:       "Triggers",
			Description: "View and edit cron triggers",
			Section:     "Configuration",
			Action:      "show_triggers",
		})
		items = append(items, actions.Item{
			Label:       "Environment Variables",
			Description: "View and edit environment variables",
			Section:     "Configuration",
			Action:      "show_env_vars",
		})
		items = append(items, actions.Item{
			Label:       "Add Environment",
			Description: "Add a new environment to the config",
			Section:     "Configuration",
			Action:      "add_environment",
		})
		// Only show delete if there are named environments (not just "default")
		if cfg := m.wrangler.Config(); cfg != nil && len(cfg.EnvNames()) > 1 {
			items = append(items, actions.Item{
				Label:       "Delete Environment",
				Description: "Remove an environment from the config",
				Section:     "Configuration",
				Action:      "delete_environment",
			})
		}
	}

	// Always include the load/switch configuration action
	items = append(items, actions.Item{
		Label:       "Load Wrangler Configuration...",
		Description: "Browse for a wrangler project directory",
		Section:     "Configuration",
		Action:      "wrangler_load_config",
	})

	return actions.New(title, items)
}

// --- Action popup helpers ---

// updateActions forwards messages to the action popup when it's active.
func (m Model) updateActions(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.actionsPopup, cmd = m.actionsPopup.Update(msg)
	return m, cmd
}

// buildActionsPopup creates the action popup for the current detail view.
func (m Model) buildActionsPopup() actions.Model {
	serviceName := m.detail.Service()
	resourceName := m.detail.CurrentDetailName()
	title := fmt.Sprintf("%s — Actions", resourceName)

	var items []actions.Item

	switch serviceName {
	case "Workers":
		items = m.buildWorkerActions()
	case "KV", "R2", "D1":
		items = m.buildBoundWorkersActions()
	}

	return actions.New(title, items)
}

// buildWorkerActions builds the action items for a Worker detail view.
func (m Model) buildWorkerActions() []actions.Item {
	var items []actions.Item

	// Actions section
	tailLabel := "Start tail logs"
	if m.monitoring.SingleTailActive() {
		tailLabel = "Stop tail logs"
	}
	items = append(items, actions.Item{
		Label:   tailLabel,
		Section: "Actions",
		Action:  "tail_toggle",
	})

	// Bindings section
	rd := m.detail.ResourceDetail()
	if rd != nil && len(rd.Bindings) > 0 {
		for _, b := range rd.Bindings {
			items = append(items, actions.Item{
				Label:       b.Name,
				Description: b.TypeDisplay,
				Section:     "Bindings",
				NavService:  b.NavService,
				NavResource: b.NavResource,
				Disabled:    b.NavService == "",
			})
		}
	}

	return items
}

// buildBoundWorkersActions builds the action items for KV/R2/D1 detail views,
// showing a "Workers" section with navigable links to Workers that bind to this resource.
func (m Model) buildBoundWorkersActions() []actions.Item {
	var items []actions.Item

	rd := m.detail.ResourceDetail()
	if rd == nil {
		return items
	}

	// The Bindings field was populated by enrichDetailWithBoundWorkers
	// with reverse references of type "worker_ref"
	for _, b := range rd.Bindings {
		if b.NavService == "Workers" {
			items = append(items, actions.Item{
				Label:       b.Name,
				Description: b.TypeDisplay,
				Section:     "Workers",
				NavService:  b.NavService,
				NavResource: b.NavResource,
			})
		}
	}

	return items
}

// handleActionSelect dispatches the selected action from the popup.
func (m *Model) handleActionSelect(item actions.Item) tea.Cmd {
	// Navigation to a bound resource
	if item.NavService != "" && item.NavResource != "" {
		return m.navigateTo(item.NavService, item.NavResource)
	}

	// Wrangler load config action
	if item.Action == "wrangler_load_config" {
		m.wrangler.ActivateDirBrowser(uiwrangler.DirBrowserModeOpen)
		return nil
	}

	// Triggers action
	if item.Action == "show_triggers" {
		configPath, projectName := m.resolveActiveProjectConfig()
		if configPath == "" {
			return nil
		}
		m.triggersFromResourceList = false
		return m.openTriggersView(configPath, projectName)
	}

	if item.Action == "show_env_vars" {
		configPath, projectName := m.resolveActiveProjectConfig()
		if configPath == "" {
			return nil
		}
		m.envVarsFromResourceList = false
		return m.openEnvVarsView(configPath, "", projectName)
	}

	// Create project action
	if item.Action == "create_project" {
		m.showProjectPopup = true
		m.projectPopup = projectpopup.New(m.wrangler.ProjectDirNames(), m.wrangler.RootDir())
		return nil
	}

	// Remove project action
	if item.Action == "remove_project" {
		cfg := m.wrangler.SelectedProjectConfig()
		if cfg == nil {
			return nil
		}
		projectName := cfg.Name
		relPath := m.wrangler.SelectedProjectRelPath()
		dirPath := filepath.Dir(m.wrangler.SelectedProjectConfigPath())
		m.showRemoveProjectPopup = true
		m.removeProjectPopup = removeprojectpopup.New(projectName, relPath, dirPath)
		return nil
	}

	if item.Action == "add_environment" {
		var configPath string
		var workerName string
		var existingEnvs []string

		if m.wrangler.IsOnProjectList() {
			// Monorepo project list: use the selected project
			configPath = m.wrangler.SelectedProjectConfigPath()
			if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
				workerName = cfg.Name
				existingEnvs = cfg.EnvNames()
			}
		} else if m.wrangler.HasConfig() {
			// Drilled into a project or single-project mode
			configPath = m.wrangler.ConfigPath()
			cfg := m.wrangler.Config()
			workerName = cfg.Name
			existingEnvs = cfg.EnvNames()
		}

		if configPath == "" {
			return nil
		}

		m.showEnvPopup = true
		m.envPopup = envpopup.New(configPath, workerName, existingEnvs)
		return nil
	}

	// Delete environment action
	if item.Action == "delete_environment" {
		var configPath string
		var workerName string
		var namedEnvs []string

		if m.wrangler.IsOnProjectList() {
			configPath = m.wrangler.SelectedProjectConfigPath()
			if cfg := m.wrangler.SelectedProjectConfig(); cfg != nil {
				workerName = cfg.Name
				for _, e := range cfg.EnvNames() {
					if e != "default" {
						namedEnvs = append(namedEnvs, e)
					}
				}
			}
		} else if m.wrangler.HasConfig() {
			configPath = m.wrangler.ConfigPath()
			cfg := m.wrangler.Config()
			workerName = cfg.Name
			for _, e := range cfg.EnvNames() {
				if e != "default" {
					namedEnvs = append(namedEnvs, e)
				}
			}
		}

		if configPath == "" || len(namedEnvs) == 0 {
			return nil
		}

		m.showEnvPopup = true
		m.envPopup = envpopup.NewDelete(configPath, workerName, namedEnvs)
		return nil
	}

	// Parallel tail: open environment sub-popup
	if item.Action == "parallel_tail" {
		m.showActions = true
		m.actionsPopup = m.buildParallelTailEnvPopup()
		return nil
	}

	// Parallel tail: stop all sessions
	if item.Action == "parallel_tail_stop" {
		m.stopAllParallelTails()
		return nil
	}

	// Parallel tail: start tailing for selected environment
	if strings.HasPrefix(item.Action, "parallel_tail_env_") {
		envName := strings.TrimPrefix(item.Action, "parallel_tail_env_")
		var targets []uiwrangler.ParallelTailTarget
		caches := m.registry.GetAllDeploymentCaches()
		for _, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			// Only include workers that actually define this environment
			if !pc.Config.HasEnv(envName) {
				continue
			}
			scriptName := pc.Config.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			url := ""
			if entry, ok := caches[scriptName]; ok && entry.Subdomain != "" {
				url = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, entry.Subdomain)
			}
			targets = append(targets, uiwrangler.ParallelTailTarget{
				ScriptName: scriptName,
				URL:        url,
			})
		}
		if len(targets) == 0 {
			return nil
		}
		return func() tea.Msg {
			return uiwrangler.ParallelTailStartMsg{EnvName: envName, Scripts: targets}
		}
	}

	// Deploy All: open environment sub-popup
	if item.Action == "deploy_all" {
		m.showActions = true
		m.actionsPopup = m.buildDeployAllEnvPopup()
		return nil
	}

	// Deploy All: start deploying for selected environment
	if strings.HasPrefix(item.Action, "deploy_all_env_") {
		envName := strings.TrimPrefix(item.Action, "deploy_all_env_")
		return m.startDeployAll(envName)
	}

	// Wrangler version picker actions (must be checked before generic wrangler_ prefix)
	if item.Action == "wrangler_deploy_version" {
		envName := m.wrangler.FocusedEnvName()
		return m.openVersionPicker(uiwrangler.PickerModeDeploy, envName)
	}
	if item.Action == "wrangler_gradual_deploy" {
		envName := m.wrangler.FocusedEnvName()
		return m.openVersionPicker(uiwrangler.PickerModeGradual, envName)
	}

	// Wrangler tail toggle (must be checked before generic wrangler_ prefix)
	if item.Action == "wrangler_tail_toggle" {
		if m.tailSession != nil {
			// Stop any active tail
			m.stopTail()
			return nil
		}
		// Start tailing the focused env's worker
		envName := m.wrangler.FocusedEnvName()
		cfg := m.wrangler.Config()
		if cfg == nil || m.client == nil {
			return nil
		}
		workerName := cfg.ResolvedEnvName(envName)
		if workerName == "" {
			return nil
		}
		// Stop any running wrangler command first
		m.stopWranglerRunner()
		// Start tail on Monitoring tab
		m.tailSource = "monitoring"
		m.monitoring.StartSingleTail(workerName)
		m.activeTab = tabbar.TabMonitoring
		accountID := m.registry.ActiveAccountID()
		return m.startTailCmd(accountID, workerName)
	}

	// Wrangler dev server actions
	if item.Action == "wrangler_dev" || item.Action == "wrangler_dev --remote" {
		action := strings.TrimPrefix(item.Action, "wrangler_")
		envName := m.wrangler.FocusedEnvName()
		return m.startWranglerCmdWithArgs(action, envName, []string{"--show-interactive-dev-session=false"})
	}
	if item.Action == "wrangler_stop_dev" {
		m.wrangler.StopDevServer()
		m.stopWranglerRunner()
		return nil
	}

	if item.Action == "wrangler_delete" {
		envName := m.wrangler.FocusedEnvName()
		return m.startWranglerCmdWithArgs("delete", envName, []string{"--force"})
	}

	// Wrangler command actions
	if strings.HasPrefix(item.Action, "wrangler_") {
		action := strings.TrimPrefix(item.Action, "wrangler_")
		envName := m.wrangler.FocusedEnvName()
		return func() tea.Msg {
			return uiwrangler.ActionMsg{Action: action, EnvName: envName}
		}
	}

	// Named actions
	switch item.Action {
	case "tail_toggle":
		if m.monitoring.SingleTailActive() || m.tailSession != nil {
			// Stop tailing
			m.stopTail()
			return nil
		}
		// Start tailing
		if m.detail.IsWorkersDetail() && m.client != nil {
			m.tailSource = "monitoring"
			scriptName := m.detail.CurrentDetailName()
			m.monitoring.StartSingleTail(scriptName)
			m.activeTab = tabbar.TabMonitoring
			accountID := m.registry.ActiveAccountID()
			return m.startTailCmd(accountID, scriptName)
		}
	}

	return nil
}
