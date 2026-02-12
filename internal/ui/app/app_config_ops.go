package app

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/bindings"
	"github.com/oarafat/orangeshell/internal/ui/deletepopup"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/envvars"
	"github.com/oarafat/orangeshell/internal/ui/projectpopup"
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
	m.detail.ClearTail()
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

	m.viewState = ViewServiceList
	m.detail.SetFocused(true)
	m.detail.SetServiceFresh("Env Variables", resources)
	return nil
}

// navigateToTriggersList builds a project list for the "Triggers" resource type
// and shows it in the standard detail list view.
func (m *Model) navigateToTriggersList() tea.Cmd {
	m.stopTail()
	m.detail.ClearTail()
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
	m.envvarsView = envvars.New(configPath, projectName, envName, vars)
	contentHeight := m.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	m.envvarsView.SetSize(m.width, contentHeight)
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
func (m Model) createProjectCmd(name, lang, dir string) tea.Cmd {
	return func() tea.Msg {
		result := wcfg.CreateProject(context.Background(), wcfg.CreateProjectCmd{
			Name: name,
			Lang: lang,
			Dir:  dir,
		})
		return projectpopup.CreateProjectDoneMsg{
			Name:    name,
			Success: result.Success,
			Output:  result.Output,
		}
	}
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
