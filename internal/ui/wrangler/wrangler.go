package wrangler

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

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

	// Directory browser for loading config from a custom directory
	dirBrowser     DirBrowser
	showDirBrowser bool

	// Command output pane (bottom split)
	cmdPane CmdPane
	spinner spinner.Model

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

// HasConfig returns whether a wrangler config is loaded.
func (m Model) HasConfig() bool {
	return m.config != nil
}

// ConfigPath returns the path to the loaded config file.
func (m Model) ConfigPath() string {
	return m.configPath
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
func (m *Model) SetConfigLoading() {
	m.config = nil
	m.configPath = ""
	m.configErr = nil
	m.configLoading = true
	m.envNames = nil
	m.envBoxes = nil
	m.showDirBrowser = false
}

// SetSize updates the view dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Config returns the loaded wrangler config (may be nil).
func (m Model) Config() *wcfg.WranglerConfig {
	return m.config
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
	return m.configLoading || m.cmdPane.IsRunning()
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
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
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
		if m.config == nil {
			return m, nil
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

	// Title bar
	title := theme.TitleStyle.Render("  Wrangler")
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

	// Config loading
	if m.configLoading && m.config == nil && m.configErr == nil {
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

	// No config found
	if m.config == nil {
		body := theme.DimStyle.Render("\n  No wrangler configuration found")
		hint := theme.DimStyle.Render("\n  Place a wrangler.jsonc, wrangler.json, or wrangler.toml\n  in the current directory.")
		helpHint := theme.DimStyle.Render("\n  Press ctrl+p to load from a custom path.")
		content := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", title, sep, body, hint, helpHint)
		return m.renderBorder(content, contentHeight)
	}

	// Calculate layout split: if cmd pane is active, give it ~35% of height
	cmdPaneHeight := 0
	envPaneHeight := contentHeight
	if m.cmdPane.IsActive() {
		cmdPaneHeight = contentHeight * 35 / 100
		if cmdPaneHeight < 6 {
			cmdPaneHeight = 6
		}
		envPaneHeight = contentHeight - cmdPaneHeight
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
		// Split view: env boxes on top, cmd pane on bottom
		envContent := strings.Join(visible, "\n")
		cmdContent := m.cmdPane.View(cmdPaneHeight, m.width-4, m.spinner.View())
		content = envContent + "\n" + cmdContent
	} else {
		content = strings.Join(visible, "\n")
	}

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
