package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/api"
	svc "github.com/oarafat/orangeshell/internal/service"
	uiconfig "github.com/oarafat/orangeshell/internal/ui/config"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
	"github.com/oarafat/orangeshell/internal/ui/removeprojectpopup"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

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

// --- Environment variables / Triggers navigation helpers ---

// navigateToEnvVars opens the Configuration tab with Env Variables category selected
// for the given config path.
func (m *Model) navigateToEnvVars(configPath string) tea.Cmd {
	m.syncConfigProjects()
	m.configView.SelectProjectByPath(configPath)
	m.configView.SetCategory(uiconfig.CategoryEnvVars)
	m.activeTab = tabbar.TabConfiguration
	return nil
}

// navigateToTriggers opens the Configuration tab with Triggers category selected
// for the given config path.
func (m *Model) navigateToTriggers(configPath string) tea.Cmd {
	m.syncConfigProjects()
	m.configView.SelectProjectByPath(configPath)
	m.configView.SetCategory(uiconfig.CategoryTriggers)
	m.activeTab = tabbar.TabConfiguration
	return nil
}

// setVarCmd writes an env var into the wrangler config file.
func (m Model) setVarCmd(configPath, envName, varName, value string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.SetVar(configPath, envName, varName, value)
		return uiconfig.SetVarDoneMsg{Err: err}
	}
}

// removeVarCmd removes an env var from the wrangler config file.
func (m Model) removeVarCmd(configPath, envName, varName string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.RemoveVar(configPath, envName, varName)
		return uiconfig.DeleteVarDoneMsg{Err: err}
	}
}

// addCronCmd adds a cron trigger to the wrangler config file.
func (m Model) addCronCmd(configPath, cron string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.AddCron(configPath, cron)
		return uiconfig.AddCronDoneMsg{Err: err}
	}
}

// removeCronCmd removes a cron trigger from the wrangler config file.
func (m Model) removeCronCmd(configPath, cron string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.RemoveCron(configPath, cron)
		return uiconfig.DeleteCronDoneMsg{Err: err}
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

// registryServiceName maps a binding picker resource type to its service name
// in the registry. Returns "" if the type is not backed by the registry (i.e.
// uses the raw HTTP ResourceListClient instead).
func registryServiceName(resourceType string) string {
	switch resourceType {
	case "d1":
		return "D1"
	case "kv":
		return "KV"
	case "r2":
		return "R2"
	case "queue":
		return "Queues"
	case "service":
		return "Workers"
	}
	return ""
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

// handleEnvPopupMsg handles all add/delete environment popup messages.
func (m *Model) handleEnvPopupMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case envpopup.CloseMsg:
		m.showEnvPopup = false
		return *m, nil, true

	case envpopup.CreateEnvMsg:
		return *m, m.createEnvCmd(msg.ConfigPath, msg.EnvName), true

	case envpopup.CreateEnvDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to create environment: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Environment added. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		// Also forward to legacy popup
		var cmd tea.Cmd
		m.envPopup, cmd = m.envPopup.Update(msg)
		return *m, cmd, true

	case envpopup.DeleteEnvMsg:
		return *m, m.deleteEnvCmd(msg.ConfigPath, msg.EnvName), true

	case envpopup.DeleteEnvDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to delete environment: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Environment deleted. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		// Also forward to legacy popup
		var cmd2 tea.Cmd
		m.envPopup, cmd2 = m.envPopup.Update(msg)
		return *m, cmd2, true

	case envpopup.DoneMsg:
		m.showEnvPopup = false
		if msg.ConfigPath != "" {
			toastMsg := "Environment added"
			if m.envPopup.IsDeleteMode() {
				toastMsg = "Environment deleted"
			}
			m.reloadWranglerConfig(msg.ConfigPath, toastMsg)
			m.configView.ReloadConfig()
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

// handleEnvVarsMsg handles all environment variables messages from the config tab.
func (m *Model) handleEnvVarsMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uiconfig.SetVarMsg:
		return *m, m.setVarCmd(msg.ConfigPath, msg.EnvName, msg.VarName, msg.Value), true

	case uiconfig.DeleteVarMsg:
		return *m, m.removeVarCmd(msg.ConfigPath, msg.EnvName, msg.VarName), true

	case uiconfig.SetVarDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to set var: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Variable saved. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		return *m, nil, true

	case uiconfig.DeleteVarDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to delete var: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Variable deleted. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		return *m, nil, true
	}
	return *m, nil, false
}

// handleTriggersMsg handles all cron triggers messages from the config tab.
func (m *Model) handleTriggersMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uiconfig.AddCronMsg:
		return *m, m.addCronCmd(msg.ConfigPath, msg.Cron), true

	case uiconfig.DeleteCronMsg:
		return *m, m.removeCronCmd(msg.ConfigPath, msg.Cron), true

	case uiconfig.AddCronDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to add trigger: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Trigger added. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		return *m, nil, true

	case uiconfig.DeleteCronDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to delete trigger: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Trigger deleted. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		return *m, nil, true
	}
	return *m, nil, false
}

// removeBindingForConfigCmd removes a binding and returns uiconfig.DeleteBindingDoneMsg.
func (m Model) removeBindingForConfigCmd(configPath, envName, bindingName, bindingType string) tea.Cmd {
	return func() tea.Msg {
		err := wcfg.RemoveBinding(configPath, envName, bindingName, bindingType)
		return uiconfig.DeleteBindingDoneMsg{Err: err}
	}
}

// --- Configuration tab (unified model) helpers ---

// syncConfigProjects builds the project list from the wrangler model and
// updates the config view. Call this before switching to the Configuration tab.
func (m *Model) syncConfigProjects() {
	projects := m.wrangler.ProjectConfigs()
	entries := make([]uiconfig.ProjectEntry, 0, len(projects))
	for _, p := range projects {
		if p.Config == nil {
			continue
		}
		entries = append(entries, uiconfig.ProjectEntry{
			Name:       p.Config.Name,
			ConfigPath: p.ConfigPath,
			Config:     p.Config,
		})
	}
	m.configView.SetProjects(entries)
}

// handleConfigViewMsg handles messages emitted by the config view's categories.
func (m *Model) handleConfigViewMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	// --- Bindings: delete ---
	case uiconfig.DeleteBindingMsg:
		return *m, m.removeBindingForConfigCmd(msg.ConfigPath, msg.EnvName, msg.BindingName, msg.BindingType), true

	case uiconfig.DeleteBindingDoneMsg:
		if msg.Err != nil {
			m.setToast(fmt.Sprintf("Failed to remove binding: %v", msg.Err))
			m.configView.ReloadConfig()
			return *m, toastTick(), true
		}
		m.reloadWranglerConfig(m.configView.ConfigPath(), "Binding removed")
		m.configView.ReloadConfig()
		m.registry.SetBindingIndex(nil)
		return *m, toastTick(), true

	// --- Navigate to resource (from bindings) ---
	case uiconfig.NavigateToResourceMsg:
		return *m, m.navigateTo(msg.ServiceName, msg.ResourceID), true

	// --- Inline binding add: write directly ---
	case uiconfig.WriteDirectBindingMsg:
		return *m, m.writeDirectBindingCmd(msg.ConfigPath, msg.EnvName, msg.BindingDef), true

	case uiconfig.WriteDirectBindingDoneMsg:
		if msg.Err != nil {
			m.configView.SetError(fmt.Sprintf("Failed to add binding: %v", msg.Err))
		} else {
			configPath := m.configView.ConfigPath()
			if configPath != "" {
				m.reloadWranglerConfig(configPath, "Binding added. Deploy to apply.")
				m.configView.ReloadConfig()
				return *m, toastTick(), true
			}
		}
		return *m, nil, true

	// --- Inline binding add: list resources ---
	case uiconfig.ListBindingResourcesMsg:
		return *m, m.listBindingResourcesCmd(msg.ResourceType), true

	case uiconfig.BindingResourcesLoadedMsg:
		m.configView.SetBindingResources(msg.Items, msg.Err)
		return *m, nil, true
	}
	return *m, nil, false
}

// writeDirectBindingCmd writes a binding definition into the wrangler config file.
// Used by the inline binding form/picker flow.
func (m Model) writeDirectBindingCmd(configPath, envName string, bindingDef interface{}) tea.Cmd {
	def, ok := bindingDef.(wcfg.BindingDef)
	if !ok {
		return func() tea.Msg {
			return uiconfig.WriteDirectBindingDoneMsg{Err: fmt.Errorf("invalid binding definition type")}
		}
	}
	return func() tea.Msg {
		err := wcfg.AddBinding(configPath, envName, def)
		return uiconfig.WriteDirectBindingDoneMsg{Err: err}
	}
}

// listBindingResourcesCmd fetches resources for the inline binding picker.
func (m Model) listBindingResourcesCmd(resourceType string) tea.Cmd {
	// For "workflow" type, scan local source files for Workflow classes
	if resourceType == "workflow" {
		configPath := m.configView.ConfigPath()
		var mainEntry string
		if cfg := m.configView.Config(); cfg != nil {
			mainEntry = cfg.Main
		}
		return func() tea.Msg {
			if configPath == "" {
				return uiconfig.BindingResourcesLoadedMsg{
					ResourceType: resourceType,
					Err:          fmt.Errorf("no project selected"),
				}
			}
			projectDir := filepath.Dir(configPath)
			classes := wcfg.ScanWorkflowClasses(projectDir, mainEntry)
			var items []uiconfig.BindingResourceItem
			for _, className := range classes {
				items = append(items, uiconfig.BindingResourceItem{
					ID:   className,
					Name: className,
				})
			}
			return uiconfig.BindingResourcesLoadedMsg{
				ResourceType: resourceType,
				Items:        items,
			}
		}
	}

	// For types backed by the service registry, use it directly
	if svcName := registryServiceName(resourceType); svcName != "" {
		return func() tea.Msg {
			s := m.registry.Get(svcName)
			if s == nil {
				return uiconfig.BindingResourcesLoadedMsg{
					ResourceType: resourceType,
					Err:          fmt.Errorf("%s service not available", svcName),
				}
			}
			resources, err := s.List()
			if err != nil {
				return uiconfig.BindingResourcesLoadedMsg{
					ResourceType: resourceType,
					Err:          err,
				}
			}
			var items []uiconfig.BindingResourceItem
			for _, r := range resources {
				items = append(items, uiconfig.BindingResourceItem{
					ID:   r.ID,
					Name: r.Name,
				})
			}
			return uiconfig.BindingResourcesLoadedMsg{
				ResourceType: resourceType,
				Items:        items,
			}
		}
	}

	// For other types, use the raw HTTP resource list client
	rlc := m.newResourceListClient()
	if rlc == nil {
		return func() tea.Msg {
			return uiconfig.BindingResourcesLoadedMsg{
				ResourceType: resourceType,
				Err:          fmt.Errorf("API credentials not available"),
			}
		}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var apiItems []api.ResourceItem
		var err error

		switch resourceType {
		case "vectorize":
			apiItems, err = rlc.ListVectorizeIndexes(ctx)
		case "hyperdrive":
			apiItems, err = rlc.ListHyperdriveConfigs(ctx)
		case "mtls_certificate":
			apiItems, err = rlc.ListMTLSCertificates(ctx)
		default:
			err = fmt.Errorf("unsupported resource type: %s", resourceType)
		}

		if err != nil {
			return uiconfig.BindingResourcesLoadedMsg{
				ResourceType: resourceType,
				Err:          err,
			}
		}

		items := make([]uiconfig.BindingResourceItem, len(apiItems))
		for i, r := range apiItems {
			items[i] = uiconfig.BindingResourceItem{ID: r.ID, Name: r.Name}
		}
		return uiconfig.BindingResourcesLoadedMsg{
			ResourceType: resourceType,
			Items:        items,
		}
	}
}

// newResourceListClient creates a resource list client from the current auth config.
// The Global API Key (from env vars) is tried first since it has the broadest permissions
// and works for all APIs (some reject OAuth and scoped tokens).
// Fallback chain: (1) env var API Key, (2) primary auth, (3) fallback token, (4) OAuth token.
func (m Model) newResourceListClient() *api.ResourceListClient {
	accountID := m.registry.ActiveAccountID()
	if accountID == "" {
		return nil
	}

	// Use the primary auth method's credentials. Do NOT use API Key from env
	// vars as a shortcut — the API Key may be scoped to a different account
	// and would cause auth failures on account switch.
	switch m.cfg.AuthMethod {
	case "apikey":
		return api.NewResourceListClientWithCreds(accountID, m.cfg.Email, m.cfg.APIKey, "")
	case "apitoken":
		return api.NewResourceListClientWithCreds(accountID, "", "", m.cfg.APIToken)
	case "oauth":
		// 1. Try dedicated per-account fallback token from config
		if ft := m.cfg.FallbackTokenFor(accountID); ft != "" {
			return api.NewResourceListClientWithCreds(accountID, "", "", ft)
		}
		// 2. Last resort: OAuth token (may 403 for some restricted endpoints)
		token := m.cfg.OAuthAccessToken
		if token == "" {
			return nil
		}
		return api.NewResourceListClientWithCreds(accountID, "", "", token)
	}
	return nil
}
