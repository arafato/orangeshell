package app

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	svc "github.com/oarafat/orangeshell/internal/service"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// discoverProjectsCmd returns a command that discovers wrangler projects in the CWD tree.
// If 0 found: sends ConfigLoadedMsg{nil, "", nil} (empty state)
// If 1+ found: sends ProjectsDiscoveredMsg for project list view
func (m Model) discoverProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		projects := wcfg.DiscoverProjects(".")
		cwd, _ := filepath.Abs(".")
		rootName := filepath.Base(cwd)

		if len(projects) == 0 {
			return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
		}
		return uiwrangler.ProjectsDiscoveredMsg{Projects: projects, RootName: rootName, RootDir: cwd}
	}
}

// discoverProjectsFromDir returns a command that discovers wrangler projects starting
// from a user-specified directory (via the directory browser).
func (m Model) discoverProjectsFromDir(dir string) tea.Cmd {
	return func() tea.Msg {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			absDir = dir
		}
		rootName := filepath.Base(absDir)

		projects := wcfg.DiscoverProjects(absDir)

		if len(projects) == 0 {
			return uiwrangler.ConfigLoadedMsg{Config: nil, Path: "", Err: nil}
		}
		return uiwrangler.ProjectsDiscoveredMsg{Projects: projects, RootName: rootName, RootDir: absDir}
	}
}

// fetchAllProjectDeployments returns a batch command that fetches deployment data
// for every project+environment combination in the monorepo. When force is false,
// fresh cache entries are skipped. When force is true (after mutations / account
// switches), every environment is fetched unconditionally.
func (m Model) fetchAllProjectDeployments(force ...bool) tea.Cmd {
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}

	forceRefresh := len(force) > 0 && force[0]
	accountID := m.registry.ActiveAccountID()
	projectConfigs := m.wrangler.ProjectConfigs()
	var cmds []tea.Cmd

	for i, pc := range projectConfigs {
		if pc.Config == nil {
			continue
		}
		for _, envName := range pc.Config.EnvNames() {
			scriptName := pc.Config.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			if !forceRefresh && !m.registry.IsDeploymentCacheStale(scriptName) {
				continue
			}
			cmds = append(cmds, m.fetchProjectDeployment(workersSvc, accountID, i, envName, scriptName))
		}
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fetchSingleProjectDeployments returns a batch command that fetches deployment data
// for every environment in a single-project wrangler config. When force is false,
// fresh cache entries are skipped. When force is true (after mutations / account
// switches), every environment is fetched unconditionally.
func (m Model) fetchSingleProjectDeployments(cfg *wcfg.WranglerConfig, force ...bool) tea.Cmd {
	workersSvc := m.getWorkersService()
	if workersSvc == nil {
		return nil
	}

	forceRefresh := len(force) > 0 && force[0]
	accountID := m.registry.ActiveAccountID()
	var cmds []tea.Cmd
	for _, envName := range cfg.EnvNames() {
		scriptName := cfg.ResolvedEnvName(envName)
		if scriptName == "" {
			continue
		}
		if !forceRefresh && !m.registry.IsDeploymentCacheStale(scriptName) {
			continue
		}
		cmds = append(cmds, m.fetchEnvDeployment(workersSvc, accountID, envName, scriptName))
	}

	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fetchEnvDeployment returns a command that fetches the active deployment for
// a single env in a single-project config.
func (m Model) fetchEnvDeployment(workersSvc *svc.WorkersService, accountID, envName, scriptName string) tea.Cmd {
	return func() tea.Msg {
		subdomain, _ := workersSvc.GetSubdomain()

		dep, err := workersSvc.GetActiveDeployment(scriptName)
		if err != nil {
			return uiwrangler.EnvDeploymentLoadedMsg{
				AccountID:  accountID,
				EnvName:    envName,
				ScriptName: scriptName,
				Subdomain:  subdomain,
				Err:        err,
			}
		}

		var display *uiwrangler.DeploymentDisplay
		if dep != nil {
			display = &uiwrangler.DeploymentDisplay{}
			for _, v := range dep.Versions {
				shortID := v.VersionID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				display.Versions = append(display.Versions, uiwrangler.VersionSplit{
					ShortID:    shortID,
					Percentage: v.Percentage,
				})
			}
			if subdomain != "" {
				display.URL = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
			}
		}

		return uiwrangler.EnvDeploymentLoadedMsg{
			AccountID:  accountID,
			EnvName:    envName,
			ScriptName: scriptName,
			Deployment: display,
			Subdomain:  subdomain,
		}
	}
}

// displayToDeploymentInfo converts a UI DeploymentDisplay back to a service DeploymentInfo for caching.
// Returns nil if the display is nil or has no versions.
func displayToDeploymentInfo(d *uiwrangler.DeploymentDisplay) *svc.DeploymentInfo {
	if d == nil || len(d.Versions) == 0 {
		return nil
	}
	info := &svc.DeploymentInfo{}
	for _, v := range d.Versions {
		info.Versions = append(info.Versions, svc.DeploymentVersionInfo{
			VersionID:  v.ShortID,
			Percentage: v.Percentage,
		})
	}
	return info
}

// deploymentInfoToDisplay converts a cached DeploymentInfo to a UI DeploymentDisplay.
// Returns nil if the info is nil or has no versions.
func deploymentInfoToDisplay(info *svc.DeploymentInfo, scriptName, subdomain string) *uiwrangler.DeploymentDisplay {
	if info == nil || len(info.Versions) == 0 {
		return nil
	}
	display := &uiwrangler.DeploymentDisplay{}
	for _, v := range info.Versions {
		shortID := v.VersionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		display.Versions = append(display.Versions, uiwrangler.VersionSplit{
			ShortID:    shortID,
			Percentage: v.Percentage,
		})
	}
	if subdomain != "" {
		display.URL = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
	}
	return display
}

// restoreDeploymentsFromCache populates the wrangler UI with cached deployment data
// for the active account. Called on account switch for instant display while background
// refresh fetches fresh data.
func (m *Model) restoreDeploymentsFromCache() {
	caches := m.registry.GetAllDeploymentCaches()
	if len(caches) == 0 {
		return
	}

	if m.wrangler.IsMonorepo() {
		// Restore into ProjectBoxes
		for i, pc := range m.wrangler.ProjectConfigs() {
			if pc.Config == nil {
				continue
			}
			for _, envName := range pc.Config.EnvNames() {
				scriptName := pc.Config.ResolvedEnvName(envName)
				if scriptName == "" {
					continue
				}
				if entry, ok := caches[scriptName]; ok {
					display := deploymentInfoToDisplay(entry.Deployment, scriptName, entry.Subdomain)
					m.wrangler.SetProjectDeployment(i, envName, display, entry.Subdomain)
				}
			}
		}
	} else if cfg := m.wrangler.Config(); cfg != nil {
		// Restore into EnvBoxes
		for _, envName := range cfg.EnvNames() {
			scriptName := cfg.ResolvedEnvName(envName)
			if scriptName == "" {
				continue
			}
			if entry, ok := caches[scriptName]; ok {
				display := deploymentInfoToDisplay(entry.Deployment, scriptName, entry.Subdomain)
				m.wrangler.SetEnvDeployment(envName, display, entry.Subdomain)
			}
		}
	}
}

// fetchProjectDeployment returns a command that fetches the active deployment for
// a single worker script and constructs its workers.dev URL using the cached subdomain.
func (m Model) fetchProjectDeployment(workersSvc *svc.WorkersService, accountID string, projectIdx int, envName, scriptName string) tea.Cmd {
	return func() tea.Msg {
		// Fetch subdomain (cached after first call)
		subdomain, _ := workersSvc.GetSubdomain()

		// Fetch active deployment
		dep, err := workersSvc.GetActiveDeployment(scriptName)
		if err != nil {
			return uiwrangler.ProjectDeploymentLoadedMsg{
				AccountID:    accountID,
				ProjectIndex: projectIdx,
				EnvName:      envName,
				ScriptName:   scriptName,
				Subdomain:    subdomain,
				Err:          err,
			}
		}

		var display *uiwrangler.DeploymentDisplay
		if dep != nil {
			display = &uiwrangler.DeploymentDisplay{}
			for _, v := range dep.Versions {
				shortID := v.VersionID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				display.Versions = append(display.Versions, uiwrangler.VersionSplit{
					ShortID:    shortID,
					Percentage: v.Percentage,
				})
			}
			if subdomain != "" {
				display.URL = fmt.Sprintf("https://%s.%s.workers.dev", scriptName, subdomain)
			}
		}

		return uiwrangler.ProjectDeploymentLoadedMsg{
			AccountID:    accountID,
			ProjectIndex: projectIdx,
			EnvName:      envName,
			ScriptName:   scriptName,
			Deployment:   display,
			Subdomain:    subdomain,
		}
	}
}

// refreshAfterMutation returns commands to refresh deployment data and the Workers
// service list after a mutating wrangler action (deploy, versions deploy) completes.
// Uses forced variants that ignore cache staleness since we know data has changed.
func (m Model) refreshAfterMutation() tea.Cmd {
	var cmds []tea.Cmd
	// Refresh deployment data (env boxes / project cards)
	if m.wrangler.IsMonorepo() {
		if cmd := m.fetchAllProjectDeployments(true); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if cfg := m.wrangler.Config(); cfg != nil {
		if cmd := m.fetchSingleProjectDeployments(cfg, true); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	// Refresh the Workers service list (new worker may have appeared)
	cmds = append(cmds, m.backgroundRefresh("Workers"))
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// refreshDeploymentsIfStale returns commands to refresh deployment data only when
// the deployment cache has stale entries. Used when navigating to the home screen.
func (m Model) refreshDeploymentsIfStale() tea.Cmd {
	if m.client == nil || !m.registry.AnyDeploymentCacheStale() {
		return nil
	}
	if m.wrangler.IsMonorepo() {
		return m.fetchAllProjectDeployments()
	}
	if cfg := m.wrangler.Config(); cfg != nil {
		return m.fetchSingleProjectDeployments(cfg)
	}
	return nil
}
