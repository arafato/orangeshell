package projectpopup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Steps ---

type step int

const (
	stepName     step = iota // Enter project name
	stepLang                 // Pick language
	stepCreating             // Running C3 CLI
	stepResult               // Show success/error
)

// --- Messages emitted by this component ---

// CreateProjectMsg requests the app to run C3 to create a new project.
type CreateProjectMsg struct {
	Name string
	Lang string
	Dir  string // parent directory where the project will be created
}

// CreateProjectDoneMsg delivers the result of project creation.
type CreateProjectDoneMsg struct {
	Name    string
	Success bool
	Output  string
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals the project was created. The app should rescan the directory.
type DoneMsg struct {
	Dir string // parent directory where the project was created
}

// --- Language entry ---

type langEntry struct {
	Code  string // "ts", "js", "python"
	Label string // "TypeScript"
	Desc  string // Short description
}

var defaultLangs = []langEntry{
	{Code: "ts", Label: "TypeScript", Desc: "Recommended, strongly typed"},
	{Code: "js", Label: "JavaScript", Desc: "Plain JavaScript"},
	{Code: "python", Label: "Python", Desc: "Python Workers"},
}

// --- Model ---

// Model is the create-project popup state.
type Model struct {
	step step

	// Text input (name step)
	nameInput textinput.Model
	inputErr  string

	// Existing project directory names (for conflict detection)
	existingNames []string

	// Root directory of the monorepo (where the new project will be created)
	rootDir string

	// Language selection
	langCursor int

	// State
	projectName string // set after name step
	projectLang string // set after lang step

	// Spinner (creating step)
	spinner spinner.Model

	// Result
	resultMsg   string
	resultIsErr bool
}

// New creates a new create-project popup.
// existingNames is the list of existing project directory names for conflict detection.
// rootDir is the monorepo root directory where the new project subdirectory will be created.
func New(existingNames []string, rootDir string) Model {
	ni := textinput.New()
	ni.Placeholder = "my-worker"
	ni.CharLimit = 63
	ni.Width = 40
	ni.Prompt = "  "
	ni.PromptStyle = theme.SelectedItemStyle
	ni.TextStyle = theme.ValueStyle
	ni.PlaceholderStyle = theme.DimStyle
	ni.Focus()

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)

	return Model{
		step:          stepName,
		nameInput:     ni,
		existingNames: existingNames,
		rootDir:       rootDir,
		spinner:       s,
	}
}

// --- Validation ---

var validProjectNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

func isValidProjectName(name string) bool {
	return validProjectNameRe.MatchString(name)
}

func (m Model) nameExists(name string) bool {
	for _, existing := range m.existingNames {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
}

// --- Update ---

// Update handles messages for the create-project popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case CreateProjectDoneMsg:
		return m.handleCreateDone(msg)
	case spinner.TickMsg:
		if m.step == stepCreating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		switch m.step {
		case stepName:
			return m.updateName(msg)
		case stepLang:
			return m.updateLang(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}

	// Forward non-key messages to text input (cursor blink, etc.)
	if m.step == stepName {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

// --- Step: Name ---

func (m Model) updateName(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+p":
		return m, func() tea.Msg { return CloseMsg{} }
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.inputErr = "Name cannot be empty"
			return m, nil
		}
		if !isValidProjectName(name) {
			m.inputErr = "Must start with a letter, only letters/digits/hyphens/underscores"
			return m, nil
		}
		if m.nameExists(name) {
			m.inputErr = fmt.Sprintf("Project %q already exists", name)
			return m, nil
		}

		m.projectName = name
		m.step = stepLang
		m.langCursor = 0 // default to TypeScript
		return m, nil
	}

	// Forward to text input
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	m.inputErr = ""
	return m, cmd
}

// --- Step: Language ---

func (m Model) updateLang(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Go back to name step
		m.step = stepName
		return m, m.nameInput.Focus()
	case "up", "k":
		if m.langCursor > 0 {
			m.langCursor--
		}
	case "down", "j":
		if m.langCursor < len(defaultLangs)-1 {
			m.langCursor++
		}
	case "enter":
		m.projectLang = defaultLangs[m.langCursor].Code
		m.step = stepCreating
		return m, tea.Batch(
			m.spinner.Tick,
			func() tea.Msg {
				return CreateProjectMsg{
					Name: m.projectName,
					Lang: m.projectLang,
					Dir:  m.rootDir,
				}
			},
		)
	}
	return m, nil
}

// --- Step: Create done ---

func (m Model) handleCreateDone(msg CreateProjectDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if !msg.Success {
		// Show truncated output on error
		output := msg.Output
		if len(output) > 500 {
			output = output[len(output)-500:]
		}
		m.resultMsg = fmt.Sprintf("Failed to create project:\n%s", output)
		m.resultIsErr = true
		return m, nil
	}

	m.resultMsg = fmt.Sprintf("Project %q created", msg.Name)
	m.resultIsErr = false
	return m, nil
}

// --- Step: Result ---

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "ctrl+p":
		if !m.resultIsErr {
			dir := m.rootDir
			return m, func() tea.Msg { return DoneMsg{Dir: dir} }
		}
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

// --- View ---

// View renders the create-project popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 70 {
		popupWidth = 70
	}

	title := "  Create Project"
	titleLine := theme.TitleStyle.Render(title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepName:
		body = m.viewName()
		help = "  esc close  |  enter confirm"
	case stepLang:
		body = m.viewLang()
		help = "  esc back  |  enter select  |  j/k navigate"
	case stepCreating:
		body = m.viewCreating()
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

func (m Model) viewName() string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render("  Enter name for the new project:"))
	lines = append(lines, theme.DimStyle.Render("  This will also be the directory name."))
	lines = append(lines, "")
	lines = append(lines, m.nameInput.View())

	if m.inputErr != "" {
		lines = append(lines, theme.ErrorStyle.Render("  "+m.inputErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewLang() string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Select language for %q:", m.projectName)))
	lines = append(lines, "")

	for i, lang := range defaultLangs {
		cursor := "  "
		if i == m.langCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}
		label := theme.ActionItemStyle.Render(fmt.Sprintf("%-14s", lang.Label))
		desc := theme.ActionDescStyle.Render(lang.Desc)
		lines = append(lines, fmt.Sprintf("%s  %s  %s", cursor, label, desc))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewCreating() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("  %s %s", m.spinner.View(), theme.DimStyle.Render(fmt.Sprintf("Creating project %q...", m.projectName))))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Running npm create cloudflare@latest"))
	lines = append(lines, theme.DimStyle.Render("  This may take a minute."))
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
		lines = append(lines, "")
		lines = append(lines, theme.DimStyle.Render("  Use Ctrl+N to add bindings and Ctrl+P to add environments."))
	}
	return strings.Join(lines, "\n")
}

// IsCreating returns true when the popup is in the creating step (spinner active).
func (m Model) IsCreating() bool {
	return m.step == stepCreating
}
