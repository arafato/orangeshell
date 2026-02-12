package envpopup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Mode ---

// Mode controls whether the popup is for adding or deleting an environment.
type Mode int

const (
	ModeAdd    Mode = iota // Create a new environment
	ModeDelete             // Delete an existing environment
)

// --- Steps ---

type step int

const (
	// Add mode steps
	stepInput    step = iota // Enter environment name
	stepCreating             // Writing config (async)

	// Delete mode steps
	stepSelectEnv // Pick from list of environments (delete via actions popup)
	stepConfirm   // Confirm deletion (yes/no)
	stepDeleting  // Deleting from config (async)

	// Shared
	stepResult // Show success/error
)

// --- Messages emitted by this component ---

// CreateEnvMsg requests the app to write a new environment to the config.
type CreateEnvMsg struct {
	ConfigPath string
	EnvName    string
}

// CreateEnvDoneMsg delivers the result of environment creation.
type CreateEnvDoneMsg struct {
	EnvName string
	Err     error
}

// DeleteEnvMsg requests the app to remove an environment from the config.
type DeleteEnvMsg struct {
	ConfigPath string
	EnvName    string
}

// DeleteEnvDoneMsg delivers the result of environment deletion.
type DeleteEnvDoneMsg struct {
	EnvName string
	Err     error
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals the operation completed (success). The app should re-parse
// the config to refresh the UI.
type DoneMsg struct {
	ConfigPath string
}

// --- Model ---

// Model is the environment popup state (add or delete mode).
type Model struct {
	mode Mode
	step step

	configPath string // path to wrangler config file
	workerName string // display name (for title)

	// Text input (add mode)
	nameInput textinput.Model
	inputErr  string // validation error

	// Existing env names (for duplicate detection in add mode, and for the list in delete mode)
	existingEnvs []string

	// Environment selection (delete mode via actions popup)
	envCursor int

	// Confirm step (delete mode)
	confirmCursor int // 0 = No, 1 = Yes

	// Result
	resultMsg   string
	resultIsErr bool
	envName     string // the target env name
}

// --- Constructors ---

// New creates a new add-environment popup.
// configPath is the wrangler config to modify.
// workerName is shown in the title (can be empty).
// existingEnvs is the list of already-defined environment names for duplicate detection.
func New(configPath, workerName string, existingEnvs []string) Model {
	ni := textinput.New()
	ni.Placeholder = "staging"
	ni.CharLimit = 63
	ni.Width = 40
	ni.Prompt = "  "
	ni.PromptStyle = theme.SelectedItemStyle
	ni.TextStyle = theme.ValueStyle
	ni.PlaceholderStyle = theme.DimStyle
	ni.Focus()

	return Model{
		mode:         ModeAdd,
		step:         stepInput,
		configPath:   configPath,
		workerName:   workerName,
		nameInput:    ni,
		existingEnvs: existingEnvs,
	}
}

// NewDelete creates a delete-environment popup that shows a list of environments to choose from.
// namedEnvs should only contain named environments (not "default").
func NewDelete(configPath, workerName string, namedEnvs []string) Model {
	return Model{
		mode:         ModeDelete,
		step:         stepSelectEnv,
		configPath:   configPath,
		workerName:   workerName,
		existingEnvs: namedEnvs,
	}
}

// NewDeleteConfirm creates a delete-environment popup that goes directly to the confirmation step.
// Used by the 'd' shortcut when the focused environment is already known.
func NewDeleteConfirm(configPath, workerName, envName string) Model {
	return Model{
		mode:       ModeDelete,
		step:       stepConfirm,
		configPath: configPath,
		workerName: workerName,
		envName:    envName,
	}
}

// IsDeleteMode returns true if the popup is in delete mode.
func (m Model) IsDeleteMode() bool {
	return m.mode == ModeDelete
}

// --- Validation ---

// validEnvName checks that the name matches [a-zA-Z][a-zA-Z0-9_]*
var validEnvNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

func isValidEnvName(name string) bool {
	return validEnvNameRe.MatchString(name)
}

func (m Model) envExists(name string) bool {
	for _, existing := range m.existingEnvs {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
}

// namedEnvs returns the existing envs excluding "default".
func (m Model) namedEnvs() []string {
	var result []string
	for _, e := range m.existingEnvs {
		if e != "default" {
			result = append(result, e)
		}
	}
	return result
}

// --- Update ---

// Update handles messages for the environment popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case CreateEnvDoneMsg:
		return m.handleCreateDone(msg)
	case DeleteEnvDoneMsg:
		return m.handleDeleteDone(msg)
	case tea.KeyMsg:
		switch m.step {
		case stepInput:
			return m.updateInput(msg)
		case stepSelectEnv:
			return m.updateSelectEnv(msg)
		case stepConfirm:
			return m.updateConfirm(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}

	// Forward non-key messages to text input (cursor blink, etc.)
	if m.step == stepInput {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

// --- Add mode: Input step ---

func (m Model) updateInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+p":
		return m, func() tea.Msg { return CloseMsg{} }
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.inputErr = "Name cannot be empty"
			return m, nil
		}
		if !isValidEnvName(name) {
			m.inputErr = "Must start with a letter, only letters/digits/underscores"
			return m, nil
		}
		if name == "default" {
			m.inputErr = "\"default\" is reserved for the top-level config"
			return m, nil
		}
		if m.envExists(name) {
			m.inputErr = fmt.Sprintf("Environment %q already exists", name)
			return m, nil
		}

		m.step = stepCreating
		m.envName = name
		return m, func() tea.Msg {
			return CreateEnvMsg{
				ConfigPath: m.configPath,
				EnvName:    name,
			}
		}
	}

	// Forward to text input
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	m.inputErr = ""
	return m, cmd
}

func (m Model) handleCreateDone(msg CreateEnvDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if msg.Err != nil {
		m.resultMsg = fmt.Sprintf("Failed to create environment: %v", msg.Err)
		m.resultIsErr = true
		return m, nil
	}

	m.resultMsg = fmt.Sprintf("Environment %q created", msg.EnvName)
	m.resultIsErr = false
	return m, nil
}

// --- Delete mode: Select env step ---

func (m Model) updateSelectEnv(msg tea.KeyMsg) (Model, tea.Cmd) {
	envs := m.namedEnvs()
	switch msg.String() {
	case "esc", "ctrl+p":
		return m, func() tea.Msg { return CloseMsg{} }
	case "up", "k":
		if m.envCursor > 0 {
			m.envCursor--
		}
	case "down", "j":
		if m.envCursor < len(envs)-1 {
			m.envCursor++
		}
	case "enter":
		if len(envs) == 0 {
			return m, nil
		}
		m.envName = envs[m.envCursor]
		m.step = stepConfirm
		m.confirmCursor = 0 // default to "No"
	}
	return m, nil
}

// --- Delete mode: Confirm step ---

func (m Model) updateConfirm(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.mode == ModeDelete && m.step == stepConfirm {
			// If we came from the env list, go back; if direct confirm (shortcut), close
			if len(m.existingEnvs) > 0 {
				m.step = stepSelectEnv
				return m, nil
			}
		}
		return m, func() tea.Msg { return CloseMsg{} }
	case "left", "h":
		if m.confirmCursor > 0 {
			m.confirmCursor--
		}
	case "right", "l":
		if m.confirmCursor < 1 {
			m.confirmCursor++
		}
	case "enter":
		if m.confirmCursor == 0 {
			// "No" selected — cancel
			return m, func() tea.Msg { return CloseMsg{} }
		}
		// "Yes" selected — proceed with deletion
		m.step = stepDeleting
		return m, func() tea.Msg {
			return DeleteEnvMsg{
				ConfigPath: m.configPath,
				EnvName:    m.envName,
			}
		}
	}
	return m, nil
}

func (m Model) handleDeleteDone(msg DeleteEnvDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if msg.Err != nil {
		m.resultMsg = fmt.Sprintf("Failed to delete environment: %v", msg.Err)
		m.resultIsErr = true
		return m, nil
	}

	m.resultMsg = fmt.Sprintf("Environment %q deleted", msg.EnvName)
	m.resultIsErr = false
	return m, nil
}

// --- Shared: Result step ---

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "ctrl+p":
		if !m.resultIsErr {
			return m, func() tea.Msg { return DoneMsg{ConfigPath: m.configPath} }
		}
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

// --- View ---

// View renders the environment popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 70 {
		popupWidth = 70
	}

	var title string
	switch m.mode {
	case ModeAdd:
		title = "  Add Environment"
		if m.workerName != "" {
			title = fmt.Sprintf("  Add Environment — %s", m.workerName)
		}
	case ModeDelete:
		title = "  Delete Environment"
		if m.workerName != "" {
			title = fmt.Sprintf("  Delete Environment — %s", m.workerName)
		}
	}

	titleLine := theme.TitleStyle.Render(title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepInput:
		body = m.viewInput()
		help = "  esc close  |  enter confirm"
	case stepCreating:
		body = theme.DimStyle.Render(fmt.Sprintf("  Creating environment %q...", m.envName))
		help = ""
	case stepSelectEnv:
		body = m.viewSelectEnv()
		help = "  esc close  |  enter select  |  j/k navigate"
	case stepConfirm:
		body = m.viewConfirm()
		help = "  esc back  |  enter confirm  |  h/l select"
	case stepDeleting:
		body = theme.DimStyle.Render(fmt.Sprintf("  Deleting environment %q...", m.envName))
		help = ""
	case stepResult:
		body = m.viewResult()
		help = "  esc close  |  enter close"
	}

	helpLine := theme.DimStyle.Render(help)

	var lines []string
	lines = append(lines, titleLine, sep)
	lines = append(lines, body)
	if help != "" {
		lines = append(lines, sep, helpLine)
	}

	content := strings.Join(lines, "\n")

	popup := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	return popup
}

// --- Sub-views ---

func (m Model) viewInput() string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render("  Enter name for the new environment:"))
	lines = append(lines, "")
	lines = append(lines, m.nameInput.View())

	if m.inputErr != "" {
		lines = append(lines, theme.ErrorStyle.Render("  "+m.inputErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewSelectEnv() string {
	envs := m.namedEnvs()

	var lines []string
	lines = append(lines, theme.DimStyle.Render("  Select environment to delete:"))
	lines = append(lines, "")

	if len(envs) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No named environments found."))
		return strings.Join(lines, "\n")
	}

	for i, env := range envs {
		cursor := "  "
		if i == m.envCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}
		label := theme.ActionItemStyle.Render(env)
		lines = append(lines, fmt.Sprintf("%s  %s", cursor, label))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewConfirm() string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Confirm deletion of %q?", m.envName)))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  This will remove the environment and all its bindings."))
	lines = append(lines, "")

	noStyle := theme.DimStyle
	yesStyle := theme.DimStyle
	if m.confirmCursor == 0 {
		noStyle = lipgloss.NewStyle().
			Background(theme.ColorDarkGray).
			Foreground(theme.ColorWhite).
			Padding(0, 2)
	} else {
		yesStyle = lipgloss.NewStyle().
			Background(theme.ColorOrange).
			Foreground(theme.ColorBg).
			Padding(0, 2)
	}

	noBtn := noStyle.Render("No")
	yesBtn := yesStyle.Render("Yes")
	lines = append(lines, fmt.Sprintf("  %s   %s", noBtn, yesBtn))

	return strings.Join(lines, "\n")
}

func (m Model) viewResult() string {
	var lines []string
	if m.resultIsErr {
		for _, line := range strings.Split(m.resultMsg, "\n") {
			lines = append(lines, theme.ErrorStyle.Render("  "+line))
		}
	} else {
		lines = append(lines, theme.SuccessStyle.Render("  "+m.resultMsg))
		if m.mode == ModeAdd {
			lines = append(lines, "")
			lines = append(lines, theme.DimStyle.Render("  Use Ctrl+N to add bindings to this environment."))
		}
	}
	return strings.Join(lines, "\n")
}
