package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/bindings"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/envvars"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/removeprojectpopup"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	uitriggers "github.com/oarafat/orangeshell/internal/ui/triggers"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Binding popup helpers ---

// updateBindings forwards messages to the bindings popup when it's active.
func (m Model) updateBindings(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.bindingsPopup, cmd = m.bindingsPopup.Update(msg)
	return m, cmd
}

// listResourcesCmd fetches existing resources for the binding popup picker.
func (m Model) listResourcesCmd(resourceType string) tea.Cmd {
	return func() tea.Msg {
		var items []bindings.ResourceItem

		s := m.registry.Get(resourceTypeToServiceName(resourceType))
		if s == nil {
			return bindings.ResourcesLoadedMsg{
				ResourceType: resourceType,
				Err:          fmt.Errorf("service not available"),
			}
		}

		resources, err := s.List()
		if err != nil {
			return bindings.ResourcesLoadedMsg{
				ResourceType: resourceType,
				Err:          err,
			}
		}

		for _, r := range resources {
			items = append(items, bindings.ResourceItem{
				ID:   r.ID,
				Name: r.Name,
			})
		}

		return bindings.ResourcesLoadedMsg{
			ResourceType: resourceType,
			Items:        items,
		}
	}
}

// createResourceCmd runs a wrangler CLI command to create a new resource.
func (m Model) createResourceCmd(resourceType, name string) tea.Cmd {
	accountID := m.registry.ActiveAccountID()
	return func() tea.Msg {
		result := wcfg.CreateResource(context.Background(), wcfg.CreateResourceCmd{
			ResourceType: resourceType,
			Name:         name,
			AccountID:    accountID,
		})
		return bindings.CreateResourceDoneMsg{
			ResourceType: resourceType,
			Name:         name,
			Success:      result.Success,
			Output:       result.Output,
			ResourceID:   result.ResourceID,
		}
	}
}

// writeBindingCmd writes a binding definition into the wrangler config file.
func (m Model) writeBindingCmd(configPath, envName string, binding wcfg.BindingDef) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.AddBinding(configPath, envName, binding)
		if err != nil {
			return bindings.WriteBindingDoneMsg{Success: false, Err: err}
		}
		return bindings.WriteBindingDoneMsg{Success: true}
	}
}

// --- Add environment popup helpers ---

// updateEnvPopup forwards messages to the env popup when it's active.
func (m Model) updateEnvPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.envPopup, cmd = m.envPopup.Update(msg)
	return m, cmd
}

// createEnvCmd writes a new empty environment section into the wrangler config file.
func (m Model) createEnvCmd(configPath, envName string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.AddEnvironment(configPath, envName)
		return envpopup.CreateEnvDoneMsg{
			EnvName: envName,
			Err:     err,
		}
	}
}

// deleteEnvCmd removes an environment section from the wrangler config file.
func (m Model) deleteEnvCmd(configPath, envName string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.DeleteEnvironment(configPath, envName)
		return envpopup.DeleteEnvDoneMsg{
			EnvName: envName,
			Err:     err,
		}
	}
}

// --- Delete resource popup helpers ---

// updateDeployAllPopup forwards messages to the deploy all popup when it's active.
func (m Model) updateDeployAllPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.deployAllPopup, cmd = m.deployAllPopup.Update(msg)
	return m, cmd
}

// updateDeletePopup forwards messages to the delete resource popup when it's active.
func (m Model) updateDeletePopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.deletePopup, cmd = m.deletePopup.Update(msg)
	return m, cmd
}

// deleteResourceCmd calls the service's Delete method via the Deleter interface.
func (m Model) deleteResourceCmd(serviceName, resourceID string) tea.Cmd {
	s := m.registry.Get(serviceName)
	if s == nil {
		return func() tea.Msg {
			return deletepopup.DeleteDoneMsg{
				ServiceName: serviceName,
				Err:         fmt.Errorf("service %s not available", serviceName),
			}
		}
	}
	deleter, ok := s.(svc.Deleter)
	if !ok {
		return func() tea.Msg {
			return deletepopup.DeleteDoneMsg{
				ServiceName: serviceName,
				Err:         fmt.Errorf("service %s does not support deletion", serviceName),
			}
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := deleter.Delete(ctx, resourceID)
		return deletepopup.DeleteDoneMsg{
			ServiceName: serviceName,
			Err:         err,
		}
	}
}

// removeBindingCmd removes a binding from the local wrangler config file.
func (m Model) removeBindingCmd(configPath, envName, bindingName, bindingType string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.RemoveBinding(configPath, envName, bindingName, bindingType)
		return deletepopup.DeleteBindingDoneMsg{Err: err}
	}
}

// --- Environment variables view helpers ---

// updateEnvVars forwards messages to the envvars view when it's active.
func (m Model) updateEnvVars(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.envvarsView, cmd = m.envvarsView.Update(msg)
	return m, cmd
}

// navigateToEnvVarsList builds a project list for the "Env Variables" resource type
// and shows it in the standard detail list view. Each project is a list item;
// pressing enter on one opens the envvars detail view for that project.
func (m *Model) navigateToEnvVarsList() tea.Cmd {
	m.stopTail()
	m.detail.ClearD1()

	projects := m.wrangler.ProjectConfigs()
	resources := make([]svc.Resource, 0, len(projects))
	for _, p := range projects {
		if p.Config == nil {
			continue
		}
		// Count total vars across all environments
		totalVars := 0
		envCount := 0
		for _, envName := range p.Config.EnvNames() {
			vars := p.Config.EnvVars(envName)
			if len(vars) > 0 {
				envCount++
			}
			totalVars += len(vars)
		}

		summary := "no variables defined"
		if totalVars > 0 {
			summary = fmt.Sprintf("%d variable(s) across %d environment(s)", totalVars, envCount)
		}

		resources = append(resources, svc.Resource{
			ID:          p.ConfigPath,
			Name:        p.Config.Name,
			ServiceType: "Env Variables",
			Summary:     summary,
		})
	}

	m.activeTab = tabbar.TabConfiguration
	m.viewState = ViewServiceList
	m.detail.SetFocused(true)
	m.detail.SetServiceFresh("Env Variables", resources)
	return nil
}

// navigateToTriggersList builds a project list for the "Triggers" resource type
// and shows it in the standard detail list view.
func (m *Model) navigateToTriggersList() tea.Cmd {
	m.stopTail()
	m.detail.ClearD1()

	projects := m.wrangler.ProjectConfigs()
	resources := make([]svc.Resource, 0, len(projects))
	for _, p := range projects {
		if p.Config == nil {
			continue
		}
		cronCount := len(p.Config.CronTriggers())
		summary := "no cron triggers defined"
		if cronCount > 0 {
			summary = fmt.Sprintf("%d cron trigger(s)", cronCount)
		}

		resources = append(resources, svc.Resource{
			ID:          p.ConfigPath,
			Name:        p.Config.Name,
			ServiceType: "Triggers",
			Summary:     summary,
		})
	}

	m.activeTab = tabbar.TabConfiguration
	m.viewState = ViewServiceList
	m.detail.SetFocused(true)
	m.detail.SetServiceFresh("Triggers", resources)
	return nil
}

// openEnvVarsView collects env vars from the config and opens the envvars view.
// If envName is empty or "default", shows all envs. Otherwise shows only the specified env.
func (m *Model) openEnvVarsView(configPath, envName, projectName string) tea.Cmd {
	cfg, err := wcfg.Parse(configPath)
	if err != nil {
		m.setToast(fmt.Sprintf("Failed to read config: %v", err))
		return toastTick()
	}

	vars := m.buildEnvVarsList(configPath, cfg)
	m.envvarsView = envvars.New(configPath, projectName, envName, cfg.EnvNames(), vars)
	contentHeight := m.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	m.envvarsView.SetSize(m.width, contentHeight)
	m.activeTab = tabbar.TabConfiguration
	m.viewState = ViewEnvVars
	return nil
}

// buildEnvVarsList collects all env vars from a wrangler config into a flat list.
func (m Model) buildEnvVarsList(configPath string, cfg *wcfg.WranglerConfig) []envvars.EnvVar {
	if cfg == nil {
		return nil
	}

	projectName := cfg.Name
	var result []envvars.EnvVar

	for _, envName := range cfg.EnvNames() {
		vars := cfg.EnvVars(envName)
		for name, value := range vars {
			result = append(result, envvars.EnvVar{
				EnvName:     envName,
				Name:        name,
				Value:       value,
				ConfigPath:  configPath,
				ProjectName: projectName,
			})
		}
	}

	return result
}

// setVarCmd writes an env var into the wrangler config file.
func (m Model) setVarCmd(configPath, envName, varName, value string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.SetVar(configPath, envName, varName, value)
		return envvars.SetVarDoneMsg{Err: err}
	}
}

// removeVarCmd removes an env var from the wrangler config file.
func (m Model) removeVarCmd(configPath, envName, varName string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.RemoveVar(configPath, envName, varName)
		return envvars.DeleteVarDoneMsg{Err: err}
	}
}

// --- Triggers helpers ---

// openTriggersView opens the cron triggers view for a given config file.
func (m *Model) openTriggersView(configPath, projectName string) tea.Cmd {
	cfg, err := wcfg.Parse(configPath)
	if err != nil {
		m.setToast(fmt.Sprintf("Failed to read config: %v", err))
		return toastTick()
	}

	m.triggersView = uitriggers.New(configPath, projectName, cfg.CronTriggers())
	contentHeight := m.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	m.triggersView.SetSize(m.width, contentHeight)
	m.activeTab = tabbar.TabConfiguration
	m.viewState = ViewTriggers
	return nil
}

// updateTriggers forwards messages to the triggers view.
func (m Model) updateTriggers(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.triggersView, cmd = m.triggersView.Update(msg)
	return m, cmd
}

// addCronCmd adds a cron trigger to the wrangler config file.
func (m Model) addCronCmd(configPath, cron string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.AddCron(configPath, cron)
		return uitriggers.AddCronDoneMsg{Err: err}
	}
}

// removeCronCmd removes a cron trigger from the wrangler config file.
func (m Model) removeCronCmd(configPath, cron string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.RemoveCron(configPath, cron)
		return uitriggers.DeleteCronDoneMsg{Err: err}
	}
}

// --- Create project popup helpers ---

// updateProjectPopup forwards messages to the project popup when it's active.
func (m Model) updateProjectPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.projectPopup, cmd = m.projectPopup.Update(msg)
	return m, cmd
}

func (m Model) updateRemoveProjectPopup(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.removeProjectPopup, cmd = m.removeProjectPopup.Update(msg)
	return m, cmd
}

// createProjectCmd runs C3 to scaffold a new Worker project.
// If template is set, creates from a Cloudflare template; otherwise uses lang.
func (m Model) createProjectCmd(name, lang, template, dir string) tea.Cmd {
	return func() tea.Msg {
		var result wcfg.CreateProjectResult
		if template != "" {
			result = wcfg.CreateProjectFromTemplate(context.Background(), wcfg.CreateFromTemplateCmd{
				Name:         name,
				TemplateName: template,
				Dir:          dir,
			})
		} else {
			result = wcfg.CreateProject(context.Background(), wcfg.CreateProjectCmd{
				Name: name,
				Lang: lang,
				Dir:  dir,
			})
		}

		var logPath string
		if !result.Success {
			logPath = writeCreateLog(name, []byte(result.Output))
		}

		return projectpopup.CreateProjectDoneMsg{
			Name:    name,
			Success: result.Success,
			Output:  result.Output,
			LogPath: logPath,
		}
	}
}

// fetchTemplatesCmd fetches the template list from the cloudflare/templates GitHub repo.
func (m Model) fetchTemplatesCmd() tea.Cmd {
	return func() tea.Msg {
		templates, err := wcfg.FetchTemplates(context.Background())
		return projectpopup.FetchTemplatesDoneMsg{
			Templates: templates,
			Err:       err,
		}
	}
}

// writeCreateLog writes project creation error output to ~/.orangeshell/logs/.
// Returns empty string if writing fails (non-fatal).
func writeCreateLog(projectName string, output []byte) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	logsDir := filepath.Join(home, ".orangeshell", "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return ""
	}
	ts := time.Now().Format("20060102T150405")
	filename := fmt.Sprintf("create-%s-%s.log", projectName, ts)
	logPath := filepath.Join(logsDir, filename)
	if err := os.WriteFile(logPath, output, 0644); err != nil {
		return ""
	}
	return logPath
}

// resourceTypeToServiceName maps a binding resource type to its service name in the registry.
func resourceTypeToServiceName(resourceType string) string {
	switch resourceType {
	case "d1":
		return "D1"
	case "kv":
		return "KV"
	case "r2":
		return "R2"
	case "queue":
		return "Queues"
	default:
		return resourceType
	}
}

// --- On-demand refresh helpers ---

// isMutatingAction returns true if the wrangler action modifies live deployment state.
func isMutatingAction(action string) bool {
	switch action {
	case "deploy", "versions deploy", "delete":
		return true
	}
	return false
}

// --- Message handler methods (Tier 3) ---
// Each returns (Model, tea.Cmd, bool) where bool == true means "handled".

// handleDeletePopupMsg handles all delete-resource popup messages.
func (m *Model) handleDeletePopupMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case deletepopup.CloseMsg:
		m.showDeletePopup = false
		m.pendingDeleteReq = nil
		return *m, nil, true

	case deletepopup.DeleteMsg:
		return *m, m.deleteResourceCmd(msg.ServiceName, msg.ResourceID), true

	case deletepopup.DeleteDoneMsg:
		var cmd tea.Cmd
		m.deletePopup, cmd = m.deletePopup.Update(msg)
		return *m, cmd, true

	case deletepopup.DeleteBindingMsg:
		return *m, m.removeBindingCmd(msg.ConfigPath, msg.EnvName, msg.BindingName, msg.BindingType), true

	case deletepopup.DeleteBindingDoneMsg:
		var cmd tea.Cmd
		m.deletePopup, cmd = m.deletePopup.Update(msg)
		return *m, cmd, true

	case deletepopup.DoneMsg:
		m.showDeletePopup = false
		if msg.ConfigPath != "" {
			// Binding delete — re-parse the config to refresh the UI
			m.reloadWranglerConfig(msg.ConfigPath, "Binding removed")
			// Invalidate the binding index so the next delete attempt rebuilds
			// it fresh from the API (the deployed state may have changed if the
			// user redeployed after removing the binding locally).
			m.registry.SetBindingIndex(nil)
			return *m, toastTick(), true
		}
		// Resource delete — optimistic cache removal + background refresh
		m.setToast("Resource deleted")
		if entry := m.registry.GetCache(msg.ServiceName); entry != nil {
			filtered := make([]svc.Resource, 0, len(entry.Resources))
			for _, r := range entry.Resources {
				if r.ID != msg.ResourceID {
					filtered = append(filtered, r)
				}
			}
			m.registry.SetCache(msg.ServiceName, filtered)
			if m.detail.Service() == msg.ServiceName {
				m.detail.RefreshResources(filtered)
			}
		}
		return *m, tea.Batch(
			m.backgroundRefresh(msg.ServiceName),
			toastTick(),
		), true
	}
	return *m, nil, false
}

// handleBindingsMsg handles all binding popup messages.
func (m *Model) handleBindingsMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case bindings.CloseMsg:
		m.showBindings = false
		return *m, nil, true

	case bindings.ListResourcesMsg:
		return *m, m.listResourcesCmd(msg.ResourceType), true

	case bindings.ResourcesLoadedMsg:
		m.bindingsPopup, _ = m.bindingsPopup.Update(msg)
		return *m, nil, true

	case bindings.CreateResourceMsg:
		return *m, m.createResourceCmd(msg.ResourceType, msg.Name), true

	case bindings.CreateResourceDoneMsg:
		m.bindingsPopup, _ = m.bindingsPopup.Update(msg)
		return *m, nil, true

	case bindings.WriteBindingMsg:
		return *m, m.writeBindingCmd(msg.ConfigPath, msg.EnvName, msg.Binding), true

	case bindings.WriteBindingDoneMsg:
		var cmd tea.Cmd
		m.bindingsPopup, cmd = m.bindingsPopup.Update(msg)
		return *m, cmd, true

	case bindings.DoneMsg:
		m.showBindings = false
		if msg.ConfigPath != "" {
			m.reloadWranglerConfig(msg.ConfigPath, "Binding added")
		}
		// If a resource was created, refresh the corresponding service cache
		if msg.ResourceType != "" {
			if svcName := resourceTypeToServiceName(msg.ResourceType); svcName != "" {
				return *m, m.backgroundRefresh(svcName), true
			}
		}
		return *m, nil, true
	}
	return *m, nil, false
}

// handleEnvPopupMsg handles all add/delete environment popup messages.
func (m *Model) handleEnvPopupMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case envpopup.CloseMsg:
		m.showEnvPopup = false
		return *m, nil, true

	case envpopup.CreateEnvMsg:
		return *m, m.createEnvCmd(msg.ConfigPath, msg.EnvName), true

	case envpopup.CreateEnvDoneMsg:
		var cmd tea.Cmd
		m.envPopup, cmd = m.envPopup.Update(msg)
		return *m, cmd, true

	case envpopup.DeleteEnvMsg:
		return *m, m.deleteEnvCmd(msg.ConfigPath, msg.EnvName), true

	case envpopup.DeleteEnvDoneMsg:
		var cmd tea.Cmd
		m.envPopup, cmd = m.envPopup.Update(msg)
		return *m, cmd, true

	case envpopup.DoneMsg:
		m.showEnvPopup = false
		if msg.ConfigPath != "" {
			toastMsg := "Environment added"
			if m.envPopup.IsDeleteMode() {
				toastMsg = "Environment deleted"
			}
			m.reloadWranglerConfig(msg.ConfigPath, toastMsg)
		}
		return *m, nil, true
	}
	return *m, nil, false
}

// handleProjectPopupMsg handles all create-project popup messages.
func (m *Model) handleProjectPopupMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case projectpopup.CloseMsg:
		m.showProjectPopup = false
		return *m, nil, true

	case projectpopup.CreateProjectMsg:
		return *m, m.createProjectCmd(msg.Name, msg.Lang, msg.Template, msg.Dir), true

	case projectpopup.FetchTemplatesMsg:
		return *m, m.fetchTemplatesCmd(), true

	case projectpopup.FetchTemplatesDoneMsg:
		var cmd tea.Cmd
		m.projectPopup, cmd = m.projectPopup.Update(msg)
		return *m, cmd, true

	case projectpopup.CreateProjectDoneMsg:
		var cmd tea.Cmd
		m.projectPopup, cmd = m.projectPopup.Update(msg)
		return *m, cmd, true

	case projectpopup.DoneMsg:
		m.showProjectPopup = false
		m.setToast("Project created")
		// Rescan to pick up the new project. Prefer the directory the project
		// was created in, then the monorepo root, then fall back to CWD.
		var rescanCmd tea.Cmd
		if msg.Dir != "" {
			rescanCmd = m.discoverProjectsFromDir(msg.Dir)
		} else if rootDir := m.wrangler.RootDir(); rootDir != "" {
			rescanCmd = m.discoverProjectsFromDir(rootDir)
		} else if m.scanDir != "" {
			rescanCmd = m.discoverProjectsFromDir(m.scanDir)
		}
		return *m, tea.Batch(rescanCmd, toastTick()), true
	}
	return *m, nil, false
}

// handleRemoveProjectMsg handles all remove-project popup messages.
func (m *Model) handleRemoveProjectMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case removeprojectpopup.CloseMsg:
		m.showRemoveProjectPopup = false
		return *m, nil, true

	case removeprojectpopup.RemoveProjectMsg:
		dirPath := msg.DirPath
		return *m, func() tea.Msg {
			err := os.RemoveAll(dirPath)
			return removeprojectpopup.RemoveProjectDoneMsg{Err: err}
		}, true

	case removeprojectpopup.RemoveProjectDoneMsg:
		var cmd tea.Cmd
		m.removeProjectPopup, cmd = m.removeProjectPopup.Update(msg)
		return *m, cmd, true

	case removeprojectpopup.DoneMsg:
		m.showRemoveProjectPopup = false
		m.setToast("Project removed")
		var rescanCmd tea.Cmd
		if rootDir := m.wrangler.RootDir(); rootDir != "" {
			rescanCmd = m.discoverProjectsFromDir(rootDir)
		} else if m.scanDir != "" {
			rescanCmd = m.discoverProjectsFromDir(m.scanDir)
		}
		return *m, tea.Batch(rescanCmd, toastTick()), true
	}
	return *m, nil, false
}

// handleEnvVarsMsg handles all environment variables view messages.
func (m *Model) handleEnvVarsMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case envvars.CloseMsg:
		if m.envVarsFromResourceList {
			// Return to the Env Variables project list on the Configuration tab
			m.envVarsFromResourceList = false
			m.activeTab = tabbar.TabConfiguration
			m.viewState = ViewServiceList
		} else {
			// Opened from wrangler — return to Operations
			m.activeTab = tabbar.TabOperations
			m.viewState = ViewWrangler
		}
		return *m, nil, true

	case envvars.SetVarMsg:
		return *m, m.setVarCmd(msg.ConfigPath, msg.EnvName, msg.VarName, msg.Value), true

	case envvars.DeleteVarMsg:
		return *m, m.removeVarCmd(msg.ConfigPath, msg.EnvName, msg.VarName), true

	case envvars.SetVarDoneMsg:
		var cmd tea.Cmd
		m.envvarsView, cmd = m.envvarsView.Update(msg)
		return *m, cmd, true

	case envvars.DeleteVarDoneMsg:
		var cmd tea.Cmd
		m.envvarsView, cmd = m.envvarsView.Update(msg)
		return *m, cmd, true

	case envvars.DoneMsg:
		if msg.ConfigPath != "" {
			if cfg := m.reloadWranglerConfig(msg.ConfigPath, "Variable saved. Deploy to apply."); cfg != nil {
				// Rebuild the envvars list with fresh data
				vars := m.buildEnvVarsList(msg.ConfigPath, cfg)
				m.envvarsView.SetVars(vars)
			}
		}
		return *m, toastTick(), true
	}
	return *m, nil, false
}

// handleTriggersMsg handles all cron triggers view messages.
func (m *Model) handleTriggersMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uitriggers.CloseMsg:
		if m.triggersFromResourceList {
			// Return to the Triggers project list on the Configuration tab
			m.triggersFromResourceList = false
			m.activeTab = tabbar.TabConfiguration
			m.viewState = ViewServiceList
		} else {
			// Opened from wrangler — return to Operations
			m.activeTab = tabbar.TabOperations
			m.viewState = ViewWrangler
		}
		return *m, nil, true

	case uitriggers.AddCronMsg:
		return *m, m.addCronCmd(msg.ConfigPath, msg.Cron), true

	case uitriggers.DeleteCronMsg:
		return *m, m.removeCronCmd(msg.ConfigPath, msg.Cron), true

	case uitriggers.AddCronDoneMsg:
		var cmd tea.Cmd
		m.triggersView, cmd = m.triggersView.Update(msg)
		return *m, cmd, true

	case uitriggers.DeleteCronDoneMsg:
		var cmd tea.Cmd
		m.triggersView, cmd = m.triggersView.Update(msg)
		return *m, cmd, true

	case uitriggers.DoneMsg:
		if msg.ConfigPath != "" {
			if cfg := m.reloadWranglerConfig(msg.ConfigPath, "Trigger saved. Deploy to apply."); cfg != nil {
				m.triggersView.SetCrons(cfg.CronTriggers())
			}
		}
		return *m, toastTick(), true
	}
	return *m, nil, false
}
