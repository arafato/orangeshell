package wrangler

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// Model is the Bubble Tea model for the Wrangler project view.
type Model struct {
	config        *wcfg.WranglerConfig
	configPath    string
	configErr     error
	configLoading bool // true while the config is being scanned at startup

	envNames   []string // ordered: "default" first, then named envs
	envBoxes   []EnvBox // one per env
	focusedEnv int      // outer cursor: which env box is focused
	insideBox  bool     // true when navigating inside an env box

	focused bool // true when the right pane (wrangler view) is focused

	// Monorepo support
	projects       []projectEntry // nil = single project mode
	projectCursor  int            // which project is focused in the list
	projectScrollY int            // vertical scroll offset for project list
	activeProject  int            // index of drilled-in project, -1 = on project list
	rootName       string         // CWD basename (monorepo name)
	rootDir        string         // absolute path to the monorepo root directory

	// Empty state menu (shown when no config found)
	emptyMenuCursor int // 0 = create project, 1 = browse directory

	// Directory browser for loading config from a custom directory
	dirBrowser     DirBrowser
	showDirBrowser bool
	dirBrowserMode DirBrowserMode // what the dir browser was opened for

	// Command output pane (bottom split) — points to the active cmdRunner's pane.
	// nil when the focused project/env has no running or recently completed command.
	activeCmdPane *CmdPane
	spinner       spinner.Model

	// Version picker overlay
	showVersionPicker bool
	versionPicker     VersionPicker

	// Version cache (shared between Deploy Version and Gradual Deployment)
	cachedVersions    []wcfg.Version
	versionsFetchedAt time.Time

	width   int
	height  int
	scrollY int // vertical scroll offset for the env box list
}

// New creates a new empty Wrangler view model.
func New() Model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)
	return Model{
		configLoading: true, // loading until SetConfig is called
		spinner:       s,
		activeProject: -1,
	}
}

// SetConfig sets the parsed wrangler configuration and rebuilds the env boxes.
func (m *Model) SetConfig(cfg *wcfg.WranglerConfig, path string, err error) {
	m.config = cfg
	m.configPath = path
	m.configErr = err
	m.configLoading = false
	m.focusedEnv = 0
	m.insideBox = false
	m.scrollY = 0

	if cfg == nil {
		m.envNames = nil
		m.envBoxes = nil
		return
	}

	m.envNames = cfg.EnvNames()
	m.envBoxes = make([]EnvBox, len(m.envNames))
	for i, name := range m.envNames {
		m.envBoxes[i] = NewEnvBox(cfg, name, i)
	}
}

// HasConfig returns whether a wrangler config is loaded (or any project has one in monorepo mode).
func (m Model) HasConfig() bool {
	if m.IsMonorepo() {
		if m.activeProject >= 0 && m.activeProject < len(m.projects) {
			return m.projects[m.activeProject].config != nil
		}
		// On project list — return true if any project has a config
		for _, p := range m.projects {
			if p.config != nil {
				return true
			}
		}
		return false
	}
	return m.config != nil
}

// ConfigPath returns the path to the loaded config file.
// In monorepo mode, returns the active project's config path.
func (m Model) ConfigPath() string {
	if m.IsMonorepo() && m.activeProject >= 0 && m.activeProject < len(m.projects) {
		return m.projects[m.activeProject].configPath
	}
	return m.configPath
}

// IsMonorepo returns true if the project list mode is active (1 or more projects discovered).
func (m Model) IsMonorepo() bool {
	return len(m.projects) > 0
}

// IsOnProjectList returns true if we're in monorepo mode and showing the project list.
func (m Model) IsOnProjectList() bool {
	return m.IsMonorepo() && m.activeProject == -1
}

// IsEmpty returns true when no wrangler config was found and the empty-state menu should show.
func (m Model) IsEmpty() bool {
	return !m.configLoading && m.config == nil && m.configErr == nil && !m.IsMonorepo() && !m.showDirBrowser
}

// SelectedProjectConfigPath returns the config path of the currently selected
// project on the monorepo project list. Returns "" if not on the project list
// or if the selected project has no config.
func (m Model) SelectedProjectConfigPath() string {
	if !m.IsOnProjectList() {
		return ""
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor].configPath
	}
	return ""
}

// SelectedProjectConfig returns the parsed config of the currently selected
// project on the monorepo project list. Returns nil if not applicable.
func (m Model) SelectedProjectConfig() *wcfg.WranglerConfig {
	if !m.IsOnProjectList() {
		return nil
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor].config
	}
	return nil
}

// SelectedProjectRelPath returns the relative path of the currently selected
// project on the monorepo project list. Returns "" if not applicable.
func (m Model) SelectedProjectRelPath() string {
	if !m.IsOnProjectList() {
		return ""
	}
	if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor].box.RelPath
	}
	return ""
}

// RootName returns the monorepo root name (CWD basename).
func (m Model) RootName() string {
	return m.rootName
}

// RootDir returns the absolute path to the monorepo root directory.
func (m Model) RootDir() string {
	return m.rootDir
}

// ProjectCount returns the number of discovered projects (0 in single-project mode).
func (m Model) ProjectCount() int {
	return len(m.projects)
}

// Config returns the loaded wrangler config (may be nil).
// In monorepo mode, returns the active project's config.
func (m Model) Config() *wcfg.WranglerConfig {
	if m.IsMonorepo() && m.activeProject >= 0 && m.activeProject < len(m.projects) {
		return m.projects[m.activeProject].config
	}
	return m.config
}

// SetFocused sets whether this view is the focused pane.
func (m *Model) SetFocused(f bool) {
	m.focused = f
}

// ActivateDirBrowser opens the directory browser starting from CWD.
func (m *Model) ActivateDirBrowser(mode DirBrowserMode) {
	m.dirBrowser = NewDirBrowser(".")
	m.dirBrowser.SetMode(mode)
	m.showDirBrowser = true
	m.dirBrowserMode = mode
}

// IsDirBrowserActive returns whether the directory browser is currently shown.
func (m Model) IsDirBrowserActive() bool {
	return m.showDirBrowser
}

// CloseDirBrowser closes the directory browser without triggering any action.
func (m *Model) CloseDirBrowser() {
	m.showDirBrowser = false
}

// DirBrowserActiveMode returns the mode the directory browser was opened in.
func (m Model) DirBrowserActiveMode() DirBrowserMode {
	return m.dirBrowserMode
}

// SetConfigLoading resets the view to a loading state (e.g. when re-scanning a new path).
// Clears both single-project and monorepo state so a fresh discovery can take over.
func (m *Model) SetConfigLoading() {
	m.config = nil
	m.configPath = ""
	m.configErr = nil
	m.configLoading = true
	m.envNames = nil
	m.envBoxes = nil
	m.showDirBrowser = false

	// Clear monorepo state
	m.projects = nil
	m.projectCursor = 0
	m.projectScrollY = 0
	m.activeProject = -1
	m.rootName = ""
	m.rootDir = ""
}

// SetSize updates the view dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// FocusedEnvName returns the name of the currently focused environment.
func (m Model) FocusedEnvName() string {
	if m.focusedEnv >= 0 && m.focusedEnv < len(m.envNames) {
		return m.envNames[m.focusedEnv]
	}
	return ""
}

// ReloadConfig re-parses a config file and refreshes the UI state.
// In monorepo mode, finds the matching project by config path and updates it.
// In single-project mode, replaces the config and rebuilds env boxes.
func (m *Model) ReloadConfig(configPath string, cfg *wcfg.WranglerConfig) {
	if m.IsMonorepo() {
		// Find the project with this config path and update its config
		for i, p := range m.projects {
			if p.configPath == configPath {
				m.projects[i].config = cfg
				m.projects[i].box.Config = cfg
				break
			}
		}
		// If we're drilled into this project, also update the env boxes
		if m.activeProject >= 0 && m.activeProject < len(m.projects) {
			entry := m.projects[m.activeProject]
			if entry.configPath == configPath && cfg != nil {
				m.config = cfg
				m.envNames = cfg.EnvNames()
				m.envBoxes = make([]EnvBox, len(m.envNames))
				for i, name := range m.envNames {
					m.envBoxes[i] = NewEnvBox(cfg, name, i)
					// Copy deployment data from the project box
					if dep, ok := entry.box.Deployments[name]; ok {
						m.envBoxes[i].Deployment = dep
					}
					if entry.box.DeploymentFetched[name] {
						m.envBoxes[i].DeploymentFetched = true
					}
					if entry.box.Subdomain != "" {
						m.envBoxes[i].Subdomain = entry.box.Subdomain
					}
				}
			}
		}
		return
	}

	// Single project mode
	m.config = cfg
	m.configPath = configPath
	if cfg != nil {
		m.envNames = cfg.EnvNames()
		m.envBoxes = make([]EnvBox, len(m.envNames))
		for i, name := range m.envNames {
			m.envBoxes[i] = NewEnvBox(cfg, name, i)
		}
	}
}

// InsideBox returns whether the user is navigating inside an env box.
func (m Model) InsideBox() bool {
	return m.insideBox
}

// CmdRunning returns whether a wrangler command is currently executing.
// Checks the active CmdPane for the focused project/env.
func (m Model) CmdRunning() bool {
	if m.activeCmdPane != nil {
		return m.activeCmdPane.IsRunning()
	}
	return false
}

// RunningAction returns the action string of the currently running command.
// Returns "" if no command is running.
func (m Model) RunningAction() string {
	if m.activeCmdPane != nil {
		return m.activeCmdPane.Action()
	}
	return ""
}

// SetActiveCmdPane sets the CmdPane to display for the currently focused project/env.
// Pass nil to hide the command pane.
func (m *Model) SetActiveCmdPane(pane *CmdPane) {
	m.activeCmdPane = pane
}

// FocusedScriptName returns the resolved script name for the currently focused environment.
// Used when starting wrangler dev to identify which worker is being dev'd.
func (m Model) FocusedScriptName() string {
	cfg := m.Config()
	if cfg == nil {
		return ""
	}
	envName := m.FocusedEnvName()
	return cfg.ResolvedEnvName(envName)
}

// FocusedProjectName returns the project name for the currently focused project.
func (m Model) FocusedProjectName() string {
	if m.IsMonorepo() && m.activeProject >= 0 && m.activeProject < len(m.projects) {
		return m.projects[m.activeProject].box.Name
	}
	if m.config != nil {
		return m.config.Name
	}
	return ""
}

// EnvBoxCount returns the number of env boxes in the current view.
func (m Model) EnvBoxCount() int {
	return len(m.envBoxes)
}

// EnvBoxAt returns a pointer to the env box at the given index, or nil if out of range.
func (m *Model) EnvBoxAt(i int) *EnvBox {
	if i < 0 || i >= len(m.envBoxes) {
		return nil
	}
	return &m.envBoxes[i]
}

// UpdateDevBadges updates the dev badge data on all project boxes in the monorepo list.
// The badgeFn function is called for each project/env pair to get the badge data.
func (m *Model) UpdateDevBadges(badgeFn func(projectName, envName string) DevBadge) {
	for i := range m.projects {
		entry := &m.projects[i]
		if entry.config == nil {
			continue
		}
		if entry.box.DevBadges == nil {
			entry.box.DevBadges = make(map[string]DevBadge)
		}
		// Clear old badges for this project
		for k := range entry.box.DevBadges {
			delete(entry.box.DevBadges, k)
		}
		// Set new badges
		for _, envName := range entry.config.EnvNames() {
			badge := badgeFn(entry.box.Name, envName)
			if badge.Status != "" {
				entry.box.DevBadges[envName] = badge
			}
		}
	}
}

// UpdateAccessBadges updates the access badge data on all project boxes in the monorepo list.
// The badgeFn function is called for each project/env pair to get the access status.
func (m *Model) UpdateAccessBadges(badgeFn func(workerName string) bool) {
	for i := range m.projects {
		entry := &m.projects[i]
		if entry.config == nil {
			continue
		}
		if entry.box.AccessBadges == nil {
			entry.box.AccessBadges = make(map[string]bool)
		}
		// Clear old badges
		for k := range entry.box.AccessBadges {
			delete(entry.box.AccessBadges, k)
		}
		// Set new badges
		for _, envName := range entry.config.EnvNames() {
			workerName := entry.config.ResolvedEnvName(envName)
			if workerName != "" && badgeFn(workerName) {
				entry.box.AccessBadges[envName] = true
			}
		}
	}
}

// SetEnvDeployment updates deployment/subdomain data on the matching EnvBox.
func (m *Model) SetEnvDeployment(envName string, dep *DeploymentDisplay, subdomain string) {
	for i := range m.envBoxes {
		if m.envBoxes[i].EnvName == envName {
			m.envBoxes[i].Deployment = dep
			m.envBoxes[i].DeploymentFetched = true
			if subdomain != "" {
				m.envBoxes[i].Subdomain = subdomain
			}
			return
		}
	}
}

// ClearDeployments wipes all deployment/subdomain data from EnvBoxes and ProjectBoxes.
// Called on account switch so stale data from the previous account is not displayed.
func (m *Model) ClearDeployments() {
	for i := range m.envBoxes {
		m.envBoxes[i].Deployment = nil
		m.envBoxes[i].Subdomain = ""
		m.envBoxes[i].DeploymentFetched = false
	}
	for i := range m.projects {
		m.projects[i].box.Deployments = make(map[string]*DeploymentDisplay)
		m.projects[i].box.DeploymentFetched = make(map[string]bool)
		m.projects[i].box.Subdomain = ""
	}
}

// FocusedWorkerName returns the worker name for the currently focused environment.
// Returns "" if no config or no worker name is resolved.
func (m Model) FocusedWorkerName() string {
	if m.config == nil {
		return ""
	}
	envName := m.FocusedEnvName()
	if envName == "" {
		return ""
	}
	return m.config.ResolvedEnvName(envName)
}

// NOTE: StartCommand, AppendCmdOutput, FinishCommand have been removed.
// The app layer now writes directly to the cmdRunner's CmdPane and sets
// the active pane via SetActiveCmdPane().

// SpinnerInit returns the command to start the spinner ticking.
func (m Model) SpinnerInit() tea.Cmd {
	return m.spinner.Tick
}

// IsLoading returns whether the spinner should be running.
func (m Model) IsLoading() bool {
	return m.configLoading || m.CmdRunning() || (m.showVersionPicker && m.versionPicker.IsLoading())
}

// ShowVersionPicker opens the version picker overlay in the given mode.
// If cached versions are fresh, they are used immediately. Otherwise the picker
// starts in loading state and the parent (app.go) is responsible for fetching.
func (m *Model) ShowVersionPicker(mode PickerMode, envName string) bool {
	m.versionPicker = NewVersionPicker(mode, envName)
	m.showVersionPicker = true

	if m.hasValidVersionCache() {
		m.versionPicker.SetVersions(m.cachedVersions)
		return true // versions available immediately
	}
	return false // caller must fetch versions
}

// SetVersions delivers fetched versions to the picker and caches them.
func (m *Model) SetVersions(versions []wcfg.Version) {
	m.cachedVersions = versions
	m.versionsFetchedAt = time.Now()
	if m.showVersionPicker {
		m.versionPicker.SetVersions(versions)
	}
}

// CloseVersionPicker hides the version picker overlay.
func (m *Model) CloseVersionPicker() {
	m.showVersionPicker = false
}

// IsVersionPickerActive returns whether the version picker overlay is shown.
func (m Model) IsVersionPickerActive() bool {
	return m.showVersionPicker
}

// VersionPickerView renders the version picker overlay for the app to composite.
func (m Model) VersionPickerView(termWidth, termHeight int) string {
	return m.versionPicker.View(termWidth, termHeight, m.spinner.View())
}

// ClearVersionCache invalidates the cached versions.
func (m *Model) ClearVersionCache() {
	m.cachedVersions = nil
	m.versionsFetchedAt = time.Time{}
}

// hasValidVersionCache returns true if the version cache is non-empty and within TTL.
func (m Model) hasValidVersionCache() bool {
	return len(m.cachedVersions) > 0 && time.Since(m.versionsFetchedAt) < versionCacheTTL
}

// UpdateSpinner forwards a spinner tick and returns the updated cmd.
func (m *Model) UpdateSpinner(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return cmd
}

// Update handles key events for the wrangler view.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	// Handle internal messages from the directory browser
	switch msg.(type) {
	case dirBrowserCloseMsg:
		m.showDirBrowser = false
		return m, nil
	case VersionPickerCloseMsg:
		m.showVersionPicker = false
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
			return m, nil
		}

		// --- Monorepo project list view ---
		if m.IsOnProjectList() {
			// Check URL clicks in project boxes first (more specific zones)
			for i, entry := range m.projects {
				if entry.box.Config == nil {
					continue
				}
				for _, envName := range entry.box.Config.EnvNames() {
					if z := zone.Get(ProjectBoxURLZoneID(i, envName)); z != nil && z.InBounds(msg) {
						workerName := entry.box.Config.ResolvedEnvName(envName)
						if workerName != "" && entry.box.Subdomain != "" {
							url := fmt.Sprintf("https://%s.%s.workers.dev", workerName, entry.box.Subdomain)
							return m, func() tea.Msg { return OpenURLMsg{URL: url} }
						}
						return m, nil
					}
				}
			}
			// Check project box clicks
			n := len(m.projects)
			for i := 0; i < n; i++ {
				if z := zone.Get(ProjectBoxZoneID(i)); z != nil && z.InBounds(msg) {
					if i == m.projectCursor {
						return m.drillIntoProject(i)
					}
					m.projectCursor = i
					m.adjustProjectScroll()
					return m, nil
				}
			}
			return m, nil
		}

		// --- Single project / env box view ---
		// Check URL clicks in env boxes first (more specific zones)
		for i := range m.envBoxes {
			if z := zone.Get(EnvBoxURLZoneID(i)); z != nil && z.InBounds(msg) {
				box := &m.envBoxes[i]
				if box.WorkerName != "" && box.Subdomain != "" {
					url := fmt.Sprintf("https://%s.%s.workers.dev", box.WorkerName, box.Subdomain)
					return m, func() tea.Msg { return OpenURLMsg{URL: url} }
				}
				return m, nil
			}
		}
		// Check env box clicks (select / enter)
		for i := range m.envBoxes {
			if z := zone.Get(EnvBoxZoneID(i)); z != nil && z.InBounds(msg) {
				if i == m.focusedEnv && !m.insideBox {
					// Already focused — enter the box
					m.insideBox = true
					return m, nil
				}
				// Select this env box
				m.focusedEnv = i
				m.insideBox = false
				m.adjustScroll()
				return m, nil
			}
		}
		return m, nil
	case tea.KeyMsg:
		// Handle version picker mode — it takes exclusive key focus
		if m.showVersionPicker {
			var cmd tea.Cmd
			m.versionPicker, cmd = m.versionPicker.Update(msg)
			return m, cmd
		}
		// Handle directory browser mode
		if m.showDirBrowser {
			return m.updateDirBrowser(msg)
		}
		// Handle cmd pane scroll keys when pane is active
		if m.activeCmdPane != nil && m.activeCmdPane.IsActive() {
			if handled := m.updateCmdPaneScroll(msg); handled {
				return m, nil
			}
		}

		// Monorepo project list mode
		if m.IsOnProjectList() {
			return m.updateProjectList(msg)
		}

		// If inside a monorepo project and pressing Esc at the outer env level,
		// return to the project list instead of doing nothing.
		if m.IsMonorepo() && m.activeProject >= 0 && !m.insideBox {
			if msg.String() == "esc" || msg.String() == "backspace" {
				m.activeProject = -1
				// Restore monorepo project's config to nil so single-project view doesn't show
				m.config = nil
				m.configPath = ""
				m.envNames = nil
				m.envBoxes = nil
				return m, nil
			}
		}

		// Empty state menu (no config found, no monorepo)
		if m.IsEmpty() {
			switch msg.String() {
			case "up", "k":
				if m.emptyMenuCursor > 0 {
					m.emptyMenuCursor--
				}
			case "down", "j":
				if m.emptyMenuCursor < 1 {
					m.emptyMenuCursor++
				}
			case "enter":
				switch m.emptyMenuCursor {
				case 0:
					return m, func() tea.Msg {
						return EmptyMenuSelectMsg{Action: "create_project"}
					}
				case 1:
					return m, func() tea.Msg {
						return EmptyMenuSelectMsg{Action: "open_project"}
					}
				}
			}
			return m, nil
		}

		if m.config == nil {
			return m, nil
		}

		// Handle "t" key — emit TailStartMsg for the app to route to Monitoring tab.
		// Block when a wrangler command is running.
		if msg.String() == "t" {
			if !m.CmdRunning() {
				workerName := m.FocusedWorkerName()
				if workerName != "" {
					return m, func() tea.Msg { return TailStartMsg{ScriptName: workerName} }
				}
			}
		}

		if m.insideBox {
			return m.updateInside(msg)
		}
		return m.updateOuter(msg)
	}
	return m, nil
}

// updateCmdPaneScroll handles scroll keys for the command output pane.
// Returns true if the key was consumed.
func (m *Model) updateCmdPaneScroll(msg tea.KeyMsg) bool {
	if m.activeCmdPane == nil {
		return false
	}
	switch msg.String() {
	case "pgup":
		m.activeCmdPane.ScrollUp(5)
		return true
	case "pgdown":
		m.activeCmdPane.ScrollDown(5)
		return true
	case "end":
		m.activeCmdPane.ScrollToBottom()
		return true
	}
	return false
}

// updateDirBrowser handles key events while the directory browser is active.
func (m Model) updateDirBrowser(msg tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.dirBrowser, cmd = m.dirBrowser.Update(msg)
	return m, cmd
}

// updateOuter handles navigation between env boxes.
func (m Model) updateOuter(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.focusedEnv > -1 {
			m.focusedEnv--
			m.adjustScroll()
		}
	case "down", "j":
		if m.focusedEnv < len(m.envBoxes)-1 {
			m.focusedEnv++
			m.adjustScroll()
		}
	case "enter":
		if m.focusedEnv == -1 {
			// Triggers item — open the triggers view
			configPath := m.configPath
			projectName := ""
			if m.config != nil {
				projectName = m.config.Name
			}
			return m, func() tea.Msg {
				return ShowTriggersMsg{
					ConfigPath:  configPath,
					ProjectName: projectName,
				}
			}
		}
		if len(m.envBoxes) > 0 && m.focusedEnv < len(m.envBoxes) {
			box := &m.envBoxes[m.focusedEnv]
			if box.ItemCount() > 0 {
				m.insideBox = true
				box.SetCursor(0)
			}
		}
	}
	return m, nil
}

// updateInside handles navigation inside an env box (over bindings and worker name).
func (m Model) updateInside(msg tea.KeyMsg) (Model, tea.Cmd) {
	box := &m.envBoxes[m.focusedEnv]

	switch msg.String() {
	case "up", "k":
		box.CursorUp()
	case "down", "j":
		box.CursorDown()
	case "esc", "backspace":
		m.insideBox = false
	case "enter":
		// Worker name selected — navigate to the Worker detail
		if box.IsWorkerSelected() {
			workerName := box.WorkerName
			return m, func() tea.Msg {
				return NavigateMsg{
					ServiceName: "Workers",
					ResourceID:  workerName,
				}
			}
		}
		// Env vars item selected — open the env vars view
		if box.IsEnvVarsSelected() {
			configPath := m.configPath
			envName := box.EnvName
			projectName := ""
			if m.config != nil {
				projectName = m.config.Name
			}
			return m, func() tea.Msg {
				return ShowEnvVarsMsg{
					ConfigPath:  configPath,
					EnvName:     envName,
					ProjectName: projectName,
				}
			}
		}
		// Binding selected — navigate to the binding target
		bnd := box.SelectedBinding()
		if bnd != nil {
			if bnd.NavService() != "" {
				// Old types (KV, R2, D1, Service, Queue) → Resources tab
				return m, func() tea.Msg {
					return NavigateMsg{
						ServiceName: bnd.NavService(),
						ResourceID:  bnd.ResourceID,
					}
				}
			}
			// New types (AI, Vectorize, Workflow, etc.) → Configuration tab Bindings
			configPath := m.configPath
			envName := box.EnvName
			bindingName := bnd.Name
			return m, func() tea.Msg {
				return NavigateToBindingMsg{
					ConfigPath:  configPath,
					EnvName:     envName,
					BindingName: bindingName,
				}
			}
		}
	case "d":
		// Delete the focused binding
		bnd := box.SelectedBinding()
		if bnd != nil {
			configPath := m.configPath
			envName := box.EnvName
			workerName := box.WorkerName
			return m, func() tea.Msg {
				return DeleteBindingRequestMsg{
					ConfigPath:  configPath,
					EnvName:     envName,
					BindingName: bnd.Name,
					BindingType: bnd.Type,
					WorkerName:  workerName,
				}
			}
		}
	}
	return m, nil
}

// adjustScroll ensures the focused env box is visible within the scroll window.
// Since env boxes have variable heights, we use a simple heuristic: each env
// box occupies roughly 1 "slot" for scroll purposes. The view rendering handles
// the actual line-level clipping.
func (m *Model) adjustScroll() {
	if m.focusedEnv < m.scrollY {
		m.scrollY = m.focusedEnv
	}
	// Ensure the focused env is not below the visible area.
	// Estimate ~8 lines per env box as a rough visible count.
	visibleCount := m.height / 8
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.focusedEnv >= m.scrollY+visibleCount {
		m.scrollY = m.focusedEnv - visibleCount + 1
	}
	if m.scrollY < 0 {
		m.scrollY = 0
	}
}
