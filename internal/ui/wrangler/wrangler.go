package wrangler

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	svc "github.com/oarafat/orangeshell/internal/service"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// versionCacheTTL is how long fetched versions remain valid before re-fetching.
const versionCacheTTL = 30 * time.Second

// NavigateMsg is sent when the user selects a navigable binding in the wrangler view.
// The parent (app.go) handles this to cross-link to the dashboard detail view.
type NavigateMsg struct {
	ServiceName string // "KV", "R2", "D1", "Workers"
	ResourceID  string // namespace_id, bucket_name, database_id, script_name
}

// ConfigLoadedMsg is sent when a wrangler config has been scanned and parsed.
type ConfigLoadedMsg struct {
	Config *wcfg.WranglerConfig
	Path   string
	Err    error
}

// ActionMsg is sent when the user triggers a wrangler action from Ctrl+P.
// The parent (app.go) handles this to start the command runner.
type ActionMsg struct {
	Action  string // "deploy", "rollback", "versions list", "deployments status"
	EnvName string // environment name (empty or "default" for top-level)
}

// CmdOutputMsg carries a line of output from a running wrangler command.
type CmdOutputMsg struct {
	Line wcfg.OutputLine
}

// CmdDoneMsg signals that a wrangler command has finished.
type CmdDoneMsg struct {
	Result wcfg.RunResult
}

// LoadConfigPathMsg is sent when the user enters a directory path to load a wrangler config from.
// The parent (app.go) handles this by scanning the given path.
type LoadConfigPathMsg struct {
	Path string
}

// VersionsFetchedMsg delivers parsed version data from `wrangler versions list --json`.
// The parent (app.go) sends this after a background fetch completes.
type VersionsFetchedMsg struct {
	Versions []wcfg.Version
	Err      error
}

// ProjectsDiscoveredMsg is sent when monorepo discovery finds multiple projects.
type ProjectsDiscoveredMsg struct {
	Projects []wcfg.ProjectInfo
	RootName string // CWD basename (monorepo name)
}

// ProjectDeploymentLoadedMsg delivers deployment data for a single project+env.
type ProjectDeploymentLoadedMsg struct {
	AccountID    string // for staleness check on account switch
	ProjectIndex int
	EnvName      string
	ScriptName   string // worker script name (cache key)
	Deployment   *DeploymentDisplay
	Subdomain    string
	Err          error
}

// EnvDeploymentLoadedMsg delivers deployment data for a single-project env.
type EnvDeploymentLoadedMsg struct {
	AccountID  string // for staleness check on account switch
	EnvName    string
	ScriptName string // worker script name (cache key)
	Deployment *DeploymentDisplay
	Subdomain  string
	Err        error
}

// TailStartMsg requests the app to start tailing a worker from the wrangler view.
type TailStartMsg struct {
	ScriptName string
}

// TailStoppedMsg signals that the wrangler-initiated tail was stopped.
type TailStoppedMsg struct{}

// projectEntry holds data for a single project in the monorepo list.
type projectEntry struct {
	box        ProjectBox
	config     *wcfg.WranglerConfig
	configPath string
}

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

	// Directory browser for loading config from a custom directory
	dirBrowser     DirBrowser
	showDirBrowser bool

	// Command output pane (bottom split)
	cmdPane CmdPane
	spinner spinner.Model

	// Version picker overlay
	showVersionPicker bool
	versionPicker     VersionPicker

	// Version cache (shared between Deploy Version and Gradual Deployment)
	cachedVersions    []wcfg.Version
	versionsFetchedAt time.Time

	// Parallel tail grid (monorepo only)
	parallelTail ParallelTailModel

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
		cmdPane:       NewCmdPane(),
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
		m.envBoxes[i] = NewEnvBox(cfg, name)
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

// IsMonorepo returns true if multiple projects were discovered.
func (m Model) IsMonorepo() bool {
	return m.projects != nil && len(m.projects) > 1
}

// IsOnProjectList returns true if we're in monorepo mode and showing the project list.
func (m Model) IsOnProjectList() bool {
	return m.IsMonorepo() && m.activeProject == -1
}

// RootName returns the monorepo root name (CWD basename).
func (m Model) RootName() string {
	return m.rootName
}

// SetProjects sets up the monorepo project list, parsing each config.
func (m *Model) SetProjects(projects []wcfg.ProjectInfo, rootName string) {
	m.configLoading = false
	m.rootName = rootName

	cwd, _ := filepath.Abs(".")

	m.projects = make([]projectEntry, len(projects))
	for i, p := range projects {
		cfg, err := wcfg.Parse(p.ConfigPath)

		// Compute relative path from CWD
		relPath, _ := filepath.Rel(cwd, p.Dir)
		if relPath == "" {
			relPath = "."
		}

		name := filepath.Base(p.Dir)

		box := ProjectBox{
			Name:              name,
			RelPath:           relPath,
			Config:            cfg,
			Err:               err,
			Deployments:       make(map[string]*DeploymentDisplay),
			DeploymentFetched: make(map[string]bool),
		}

		m.projects[i] = projectEntry{
			box:        box,
			config:     cfg,
			configPath: p.ConfigPath,
		}
	}

	m.projectCursor = 0
	m.projectScrollY = 0
	m.activeProject = -1
}

// SetProjectDeployment updates deployment data for a specific project and environment.
func (m *Model) SetProjectDeployment(projectIndex int, envName string, dep *DeploymentDisplay, subdomain string) {
	if projectIndex < 0 || projectIndex >= len(m.projects) {
		return
	}
	m.projects[projectIndex].box.Deployments[envName] = dep
	if m.projects[projectIndex].box.DeploymentFetched == nil {
		m.projects[projectIndex].box.DeploymentFetched = make(map[string]bool)
	}
	m.projects[projectIndex].box.DeploymentFetched[envName] = true
	if subdomain != "" {
		m.projects[projectIndex].box.Subdomain = subdomain
	}
}

// ProjectCount returns the number of discovered projects (0 in single-project mode).
func (m Model) ProjectCount() int {
	return len(m.projects)
}

// ProjectConfigs returns (config, configPath) pairs for all projects.
// Used by app.go to schedule deployment fetches.
func (m Model) ProjectConfigs() [](struct {
	Config     *wcfg.WranglerConfig
	ConfigPath string
}) {
	result := make([](struct {
		Config     *wcfg.WranglerConfig
		ConfigPath string
	}), len(m.projects))
	for i, p := range m.projects {
		result[i].Config = p.config
		result[i].ConfigPath = p.configPath
	}
	return result
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
func (m *Model) ActivateDirBrowser() {
	m.dirBrowser = NewDirBrowser(".")
	m.showDirBrowser = true
}

// IsDirBrowserActive returns whether the directory browser is currently shown.
func (m Model) IsDirBrowserActive() bool {
	return m.showDirBrowser
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
}

// SetSize updates the view dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.parallelTail.SetSize(w, h)
}

// FocusedEnvName returns the name of the currently focused environment.
func (m Model) FocusedEnvName() string {
	if m.focusedEnv >= 0 && m.focusedEnv < len(m.envNames) {
		return m.envNames[m.focusedEnv]
	}
	return ""
}

// InsideBox returns whether the user is navigating inside an env box.
func (m Model) InsideBox() bool {
	return m.insideBox
}

// CmdRunning returns whether a wrangler command is currently executing.
func (m Model) CmdRunning() bool {
	return m.cmdPane.IsRunning()
}

// RunningAction returns the action string of the currently running command.
// Returns "" if no command is running.
func (m Model) RunningAction() string {
	return m.cmdPane.Action()
}

// StopDevServer marks the dev server as stopped with a clean message.
// The caller (app.go) should also call stopWranglerRunner() to kill the process.
func (m *Model) StopDevServer() {
	m.cmdPane.FinishWithMessage("Stopped", false)
}

// --- Tail delegation methods ---

// StartTail prepares the cmd pane for tail log streaming.
func (m *Model) StartTail(scriptName string) {
	m.cmdPane.StartTail(scriptName)
}

// AppendTailLines adds tail log lines to the cmd pane.
func (m *Model) AppendTailLines(lines []svc.TailLine) {
	for _, line := range lines {
		m.cmdPane.AppendTailLine(line)
	}
}

// SetTailError records a tail error in the cmd pane.
func (m *Model) SetTailError(err error) {
	m.cmdPane.SetTailError(err.Error())
}

// StopTailPane marks the tail as stopped in the cmd pane.
func (m *Model) StopTailPane() {
	m.cmdPane.StopTail()
}

// TailActive returns whether the cmd pane is in tail mode and running.
func (m Model) TailActive() bool {
	return m.cmdPane.IsTail() && m.cmdPane.IsRunning()
}

// TailConnected marks the tail as connected (first data may arrive).
func (m *Model) TailConnected() {
	m.cmdPane.TailConnected()
}

// --- Parallel tail delegation methods ---

// AllEnvNames returns the union of env names across all monorepo projects.
// Returns nil in single-project mode.
func (m Model) AllEnvNames() []string {
	if !m.IsMonorepo() {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	for _, p := range m.projects {
		if p.config == nil {
			continue
		}
		for _, name := range p.config.EnvNames() {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

// StartParallelTail initializes the parallel tail grid for the given env and targets.
func (m *Model) StartParallelTail(envName string, targets []ParallelTailTarget) {
	m.parallelTail.SetSize(m.width, m.height)
	m.parallelTail.Start(envName, targets)
}

// StopParallelTail stops the parallel tail grid display.
func (m *Model) StopParallelTail() {
	m.parallelTail.Stop()
}

// IsParallelTailActive returns whether the parallel tail grid is active.
func (m Model) IsParallelTailActive() bool {
	return m.parallelTail.IsActive()
}

// ParallelTailAppendLines routes lines to the correct parallel tail pane.
func (m *Model) ParallelTailAppendLines(scriptName string, lines []svc.TailLine) {
	m.parallelTail.AppendLines(scriptName, lines)
}

// ParallelTailSetConnected marks a parallel tail pane as connected.
func (m *Model) ParallelTailSetConnected(scriptName string) {
	m.parallelTail.SetConnected(scriptName)
}

// ParallelTailSetError marks a parallel tail pane as errored.
func (m *Model) ParallelTailSetError(scriptName string, err error) {
	m.parallelTail.SetError(scriptName, err.Error())
}

// ParallelTailSetSessionID records the session ID for a parallel tail pane.
func (m *Model) ParallelTailSetSessionID(scriptName, sessionID string) {
	m.parallelTail.SetSessionID(scriptName, sessionID)
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

// StartCommand prepares the cmd pane for a new command execution.
func (m *Model) StartCommand(action, envName string) {
	m.cmdPane.StartCommand(action, envName)
}

// AppendCmdOutput adds a line to the command output pane.
func (m *Model) AppendCmdOutput(line wcfg.OutputLine) {
	m.cmdPane.AppendLine(line.Text, line.IsStderr, line.Timestamp)
}

// FinishCommand marks the current command as done.
func (m *Model) FinishCommand(result wcfg.RunResult) {
	m.cmdPane.Finish(result.ExitCode, result.Err)
}

// SpinnerInit returns the command to start the spinner ticking.
func (m Model) SpinnerInit() tea.Cmd {
	return m.spinner.Tick
}

// IsLoading returns whether the spinner should be running.
func (m Model) IsLoading() bool {
	return m.configLoading || m.cmdPane.IsRunning() || (m.showVersionPicker && m.versionPicker.IsLoading())
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
	case tea.KeyMsg:
		// Handle version picker mode — it takes exclusive key focus
		if m.showVersionPicker {
			var cmd tea.Cmd
			m.versionPicker, cmd = m.versionPicker.Update(msg)
			return m, cmd
		}
		// Handle parallel tail mode — exclusive key focus (only esc, j/k)
		if m.parallelTail.IsActive() {
			var cmd tea.Cmd
			m.parallelTail, cmd = m.parallelTail.Update(msg)
			return m, cmd
		}
		// Handle directory browser mode
		if m.showDirBrowser {
			return m.updateDirBrowser(msg)
		}
		// Handle cmd pane scroll keys when pane is active
		if m.cmdPane.IsActive() {
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

		if m.config == nil {
			return m, nil
		}

		// Handle "t" key for tail toggle (before routing to inner/outer nav).
		// Block when a wrangler command (non-tail) is running.
		if msg.String() == "t" {
			if m.TailActive() {
				// Tail is running — stop it
				return m, func() tea.Msg { return TailStoppedMsg{} }
			}
			// Don't start tail if a non-tail command is running
			if !m.cmdPane.IsRunning() {
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

// updateProjectList handles navigation on the monorepo project list.
func (m Model) updateProjectList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.projectCursor > 0 {
			m.projectCursor--
			m.adjustProjectScroll()
		}
	case "down", "j":
		if m.projectCursor < len(m.projects)-1 {
			m.projectCursor++
			m.adjustProjectScroll()
		}
	case "enter":
		if m.projectCursor >= 0 && m.projectCursor < len(m.projects) {
			return m.drillIntoProject(m.projectCursor)
		}
	}
	return m, nil
}

// drillIntoProject sets the active project and switches to the single-project view.
func (m Model) drillIntoProject(idx int) (Model, tea.Cmd) {
	entry := m.projects[idx]
	m.activeProject = idx

	// Load this project's config into the single-project view fields
	m.config = entry.config
	m.configPath = entry.configPath
	m.configErr = nil
	m.focusedEnv = 0
	m.insideBox = false
	m.scrollY = 0

	if entry.config != nil {
		m.envNames = entry.config.EnvNames()
		m.envBoxes = make([]EnvBox, len(m.envNames))
		for i, name := range m.envNames {
			m.envBoxes[i] = NewEnvBox(entry.config, name)
			// Copy deployment data from the project box into the env box
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
	} else {
		m.envNames = nil
		m.envBoxes = nil
	}

	return m, nil
}

// adjustProjectScroll ensures the focused project is visible in the scroll window.
func (m *Model) adjustProjectScroll() {
	if m.projectCursor < m.projectScrollY {
		m.projectScrollY = m.projectCursor
	}
	// Estimate ~10 lines per project box
	visibleCount := m.height / 10
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.projectCursor >= m.projectScrollY+visibleCount {
		m.projectScrollY = m.projectCursor - visibleCount + 1
	}
	if m.projectScrollY < 0 {
		m.projectScrollY = 0
	}
}

// updateCmdPaneScroll handles scroll keys for the command output pane.
// Returns true if the key was consumed.
func (m *Model) updateCmdPaneScroll(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup":
		m.cmdPane.ScrollUp(5)
		return true
	case "pgdown":
		m.cmdPane.ScrollDown(5)
		return true
	case "end":
		m.cmdPane.ScrollToBottom()
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
		if m.focusedEnv > 0 {
			m.focusedEnv--
			m.adjustScroll()
		}
	case "down", "j":
		if m.focusedEnv < len(m.envBoxes)-1 {
			m.focusedEnv++
			m.adjustScroll()
		}
	case "enter":
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
		// Binding selected — navigate to the binding target
		bnd := box.SelectedBinding()
		if bnd != nil && bnd.NavService() != "" {
			return m, func() tea.Msg {
				return NavigateMsg{
					ServiceName: bnd.NavService(),
					ResourceID:  bnd.ResourceID,
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

// View renders the wrangler view.
func (m Model) View() string {
	contentHeight := m.height - 4 // border + title + separator
	if contentHeight < 1 {
		contentHeight = 1
	}
	boxWidth := m.width - 4 // padding within the detail panel

	// Parallel tail grid takes over the entire view
	if m.parallelTail.IsActive() {
		content := m.parallelTail.View()
		return m.renderBorder(content, contentHeight)
	}

	// Title bar
	titleText := "  Wrangler"
	if m.IsMonorepo() && m.activeProject >= 0 && m.activeProject < len(m.projects) {
		titleText = fmt.Sprintf("  %s / %s", m.rootName, m.projects[m.activeProject].box.Name)
	}
	title := theme.TitleStyle.Render(titleText)
	sepWidth := m.width - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Config error
	if m.configErr != nil {
		body := theme.ErrorStyle.Render(fmt.Sprintf("\n  Error loading config: %s", m.configErr.Error()))
		content := fmt.Sprintf("%s\n%s\n%s", title, sep, body)
		return m.renderBorder(content, contentHeight)
	}

	// Config loading (not shown in monorepo mode since projects handle their own state)
	if m.configLoading && m.config == nil && m.configErr == nil && !m.IsMonorepo() {
		body := fmt.Sprintf("\n  %s %s",
			m.spinner.View(),
			theme.DimStyle.Render("Loading wrangler configuration..."))
		content := fmt.Sprintf("%s\n%s\n%s", title, sep, body)
		return m.renderBorder(content, contentHeight)
	}

	// Directory browser (shown over any state — config loaded or not)
	if m.showDirBrowser {
		content := m.dirBrowser.View(boxWidth, contentHeight)
		return m.renderBorder(content, contentHeight)
	}

	// Monorepo project list view
	if m.IsOnProjectList() {
		return m.viewProjectList(contentHeight, boxWidth, title, sep)
	}

	// No config found
	if m.config == nil {
		body := theme.DimStyle.Render("\n  No wrangler configuration found")
		hint := theme.DimStyle.Render("\n  Place a wrangler.jsonc, wrangler.json, or wrangler.toml\n  in the current directory.")
		helpHint := theme.DimStyle.Render("\n  Press ctrl+p to load from a custom path.")
		content := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", title, sep, body, hint, helpHint)
		return m.renderBorder(content, contentHeight)
	}

	// Calculate layout split:
	// - When a wrangler command is running: command output pane at ~35%
	// - Otherwise: always show the Live Logs tail console at ~40%
	cmdPaneHeight := 0
	tailConsoleHeight := 0
	envPaneHeight := contentHeight

	cmdActive := m.cmdPane.IsActive() && !m.cmdPane.IsTail()
	if cmdActive {
		// Wrangler command output pane
		cmdPaneHeight = contentHeight * 35 / 100
		if cmdPaneHeight < 6 {
			cmdPaneHeight = 6
		}
		envPaneHeight = contentHeight - cmdPaneHeight
		if envPaneHeight < 5 {
			envPaneHeight = 5
		}
	} else {
		// Always-visible tail console
		tailConsoleHeight = contentHeight * 40 / 100
		if tailConsoleHeight < 6 {
			tailConsoleHeight = 6
		}
		envPaneHeight = contentHeight - tailConsoleHeight
		if envPaneHeight < 5 {
			envPaneHeight = 5
		}
	}

	// Config path subtitle
	subtitle := theme.DimStyle.Render(fmt.Sprintf("  %s", m.configPath))

	// Worker name
	workerLine := ""
	if m.config.Name != "" {
		workerLine = fmt.Sprintf("  %s %s",
			theme.LabelStyle.Render("Worker:"),
			theme.ValueStyle.Render(m.config.Name))
	}

	// Build env box views
	var boxViews []string
	for i := range m.envBoxes {
		focused := i == m.focusedEnv
		inside := focused && m.insideBox
		boxView := m.envBoxes[i].View(boxWidth, focused, inside)
		boxViews = append(boxViews, boxView)
	}

	// Help text
	var helpText string
	if m.insideBox {
		helpText = theme.DimStyle.Render("  j/k navigate  |  enter open  |  esc back  |  ctrl+p actions")
	} else {
		helpText = theme.DimStyle.Render("  j/k navigate  |  enter drill into  |  ctrl+p actions  |  tab sidebar")
	}

	// Assemble all env content lines
	var allLines []string
	allLines = append(allLines, title, sep, subtitle)
	if workerLine != "" {
		allLines = append(allLines, workerLine)
	}
	allLines = append(allLines, "") // spacer

	// Add box views (each box is multi-line, split by \n)
	for _, bv := range boxViews {
		boxLines := strings.Split(bv, "\n")
		allLines = append(allLines, boxLines...)
		allLines = append(allLines, "") // spacer between boxes
	}

	allLines = append(allLines, helpText)

	// Apply vertical scrolling to the env section
	visibleHeight := envPaneHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	maxScroll := len(allLines) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	offset := m.scrollY
	if offset > maxScroll {
		offset = maxScroll
	}
	endIdx := offset + visibleHeight
	if endIdx > len(allLines) {
		endIdx = len(allLines)
	}

	visible := allLines[offset:endIdx]

	// Pad env section to exact height
	for len(visible) < envPaneHeight {
		visible = append(visible, "")
	}

	var content string
	if cmdPaneHeight > 0 {
		// Split view: env boxes on top, command output pane on bottom
		envContent := strings.Join(visible, "\n")
		cmdContent := m.cmdPane.View(cmdPaneHeight, m.width-4, m.spinner.View())
		content = envContent + "\n" + cmdContent
	} else if tailConsoleHeight > 0 {
		// Split view: env boxes on top, tail console on bottom
		envContent := strings.Join(visible, "\n")
		tailContent := m.cmdPane.ViewTailConsole(tailConsoleHeight, m.width-4, m.spinner.View())
		content = envContent + "\n" + tailContent
	} else {
		content = strings.Join(visible, "\n")
	}

	return m.renderBorder(content, contentHeight)
}

// viewProjectList renders the monorepo project list view.
func (m Model) viewProjectList(contentHeight, boxWidth int, title, sep string) string {
	// Monorepo title uses the root name
	monoTitle := theme.TitleStyle.Render(fmt.Sprintf("  %s", m.rootName))
	subtitle := theme.DimStyle.Render(fmt.Sprintf("  %d projects", len(m.projects)))

	helpText := theme.DimStyle.Render("  j/k navigate  |  enter drill into  |  ctrl+p actions  |  ctrl+l services")

	// Build project box views
	var allLines []string
	allLines = append(allLines, monoTitle, sep, subtitle, "")

	for i, p := range m.projects {
		focused := i == m.projectCursor
		boxView := p.box.View(boxWidth, focused)
		boxLines := strings.Split(boxView, "\n")
		allLines = append(allLines, boxLines...)
		allLines = append(allLines, "") // spacer between boxes
	}

	allLines = append(allLines, helpText)

	// Apply vertical scrolling
	visibleHeight := contentHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	maxScroll := len(allLines) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	offset := m.projectScrollY
	if offset > maxScroll {
		offset = maxScroll
	}
	endIdx := offset + visibleHeight
	if endIdx > len(allLines) {
		endIdx = len(allLines)
	}

	visible := allLines[offset:endIdx]

	// Pad to exact height
	for len(visible) < contentHeight {
		visible = append(visible, "")
	}

	content := strings.Join(visible, "\n")
	return m.renderBorder(content, contentHeight)
}

// renderBorder wraps content in the detail panel border style.
func (m Model) renderBorder(content string, contentHeight int) string {
	// Truncate to contentHeight lines
	lines := strings.Split(content, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
		content = strings.Join(lines, "\n")
	}

	borderStyle := theme.BorderStyle
	if m.focused {
		borderStyle = theme.ActiveBorderStyle
	}
	return borderStyle.
		Width(m.width - 2).
		Height(contentHeight).
		Render(content)
}
