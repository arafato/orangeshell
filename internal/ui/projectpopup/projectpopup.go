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
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Steps ---

type step int

const (
	stepName              step = iota // Enter project name
	stepLang                          // Pick language or "From Template"
	stepFetchingTemplates             // Spinner while fetching template list
	stepTemplate                      // Template selection with filter
	stepCreating                      // Running C3 CLI
	stepResult                        // Show success/error
)

// Maximum number of template entries visible at once in the scrollable list.
const maxVisibleTemplates = 10

// --- Messages emitted by this component ---

// CreateProjectMsg requests the app to run C3 to create a new project.
type CreateProjectMsg struct {
	Name     string
	Lang     string // set for lang-based creation ("ts", "js", "python")
	Template string // set for template-based creation (e.g. "vite-react-template")
	Dir      string // parent directory where the project will be created
}

// CreateProjectDoneMsg delivers the result of project creation.
type CreateProjectDoneMsg struct {
	Name    string
	Success bool
	Output  string
	LogPath string // path to error log file (set by app on failure)
}

// FetchTemplatesMsg requests the app to fetch the template list from GitHub.
type FetchTemplatesMsg struct{}

// FetchTemplatesDoneMsg delivers the fetched template list.
type FetchTemplatesDoneMsg struct {
	Templates []wcfg.TemplateInfo
	Err       error
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals the project was created. The app should rescan the directory.
type DoneMsg struct {
	Dir string // parent directory where the project was created
}

// --- Language entry ---

type langEntry struct {
	Code  string // "ts", "js", "python", "template"
	Label string // "TypeScript"
	Desc  string // Short description
}

var defaultLangs = []langEntry{
	{Code: "ts", Label: "TypeScript", Desc: "Recommended, strongly typed"},
	{Code: "js", Label: "JavaScript", Desc: "Plain JavaScript"},
	{Code: "python", Label: "Python", Desc: "Python Workers"},
	{Code: "template", Label: "From Template", Desc: "Use a Cloudflare template"},
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

	// Template selection
	templates             []wcfg.TemplateInfo // cached after first fetch
	filteredTemplates     []wcfg.TemplateInfo // after applying filter
	templateCursor        int
	templateScrollOffset  int // index of first visible template entry
	templateFilter        textinput.Model
	templateFilterFocused bool
	selectedTemplate      *wcfg.TemplateInfo // set when a template is selected
	templateFetchErr      string             // error from fetching templates

	// Spinner (creating/fetching steps)
	spinner spinner.Model

	// Result
	resultMsg   string
	resultIsErr bool
	logPath     string // path to error log (set on failure)
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

	tf := textinput.New()
	tf.Placeholder = "Type to filter..."
	tf.CharLimit = 60
	tf.Width = 40
	tf.Prompt = ""
	tf.TextStyle = theme.ValueStyle
	tf.PlaceholderStyle = theme.DimStyle

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)

	return Model{
		step:           stepName,
		nameInput:      ni,
		existingNames:  existingNames,
		rootDir:        rootDir,
		templateFilter: tf,
		spinner:        s,
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
	case FetchTemplatesDoneMsg:
		return m.handleFetchTemplatesDone(msg)
	case spinner.TickMsg:
		if m.step == stepCreating || m.step == stepFetchingTemplates {
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
		case stepFetchingTemplates:
			// Allow esc to go back while fetching (request continues in background)
			if msg.String() == "esc" {
				m.step = stepLang
				return m, nil
			}
		case stepTemplate:
			return m.updateTemplate(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}

	// Forward non-key messages to active text inputs (cursor blink, etc.)
	switch m.step {
	case stepName:
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	case stepTemplate:
		if m.templateFilterFocused {
			var cmd tea.Cmd
			m.templateFilter, cmd = m.templateFilter.Update(msg)
			return m, cmd
		}
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
		selected := defaultLangs[m.langCursor]
		if selected.Code == "template" {
			// "From Template" selected
			if m.templates != nil {
				// Templates already cached — jump directly to selection
				m.step = stepTemplate
				m.templateCursor = 0
				m.templateScrollOffset = 0
				m.templateFilterFocused = true
				m.templateFilter.SetValue("")
				m.applyTemplateFilter()
				return m, m.templateFilter.Focus()
			}
			// Need to fetch templates
			m.step = stepFetchingTemplates
			m.templateFetchErr = ""
			return m, tea.Batch(
				m.spinner.Tick,
				func() tea.Msg { return FetchTemplatesMsg{} },
			)
		}
		// Language-based creation
		m.projectLang = selected.Code
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

// --- Step: Fetching templates done ---

func (m Model) handleFetchTemplatesDone(msg FetchTemplatesDoneMsg) (Model, tea.Cmd) {
	if msg.Err != nil {
		// Show error on the lang step, let user retry or pick another option.
		// Only show the error if we're still on the fetching step (user didn't navigate away).
		if m.step == stepFetchingTemplates {
			m.step = stepLang
		}
		m.templateFetchErr = fmt.Sprintf("Failed to fetch templates: %s", msg.Err)
		return m, nil
	}

	// Always cache the result regardless of current step.
	m.templates = msg.Templates
	m.filteredTemplates = msg.Templates

	// Only transition to the template selection if we're still waiting for the fetch.
	// If the user navigated away (esc), just cache the data for next time.
	if m.step == stepFetchingTemplates {
		m.templateCursor = 0
		m.templateScrollOffset = 0
		m.templateFilterFocused = true
		m.templateFilter.SetValue("")
		m.step = stepTemplate
		return m, m.templateFilter.Focus()
	}
	return m, nil
}

// --- Step: Template selection ---

func (m Model) updateTemplate(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.templateFilterFocused {
		return m.updateTemplateFilterFocused(msg)
	}
	return m.updateTemplateListFocused(msg)
}

// updateTemplateFilterFocused handles keys when the template filter input has focus.
func (m Model) updateTemplateFilterFocused(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.templateFilter.Value() != "" {
			// Clear filter
			m.templateFilter.SetValue("")
			m.applyTemplateFilter()
			m.templateCursor = 0
			m.templateScrollOffset = 0
			return m, nil
		}
		// Go back to language step
		m.templateFilter.Blur()
		m.step = stepLang
		return m, nil

	case "down":
		// Move focus to template list
		if len(m.filteredTemplates) > 0 {
			m.templateFilterFocused = false
			m.templateFilter.Blur()
			m.templateCursor = 0
			m.templateScrollOffset = 0
		}
		return m, nil

	case "enter":
		// Move focus to list so user can select
		if len(m.filteredTemplates) > 0 {
			m.templateFilterFocused = false
			m.templateFilter.Blur()
			m.templateCursor = 0
			m.templateScrollOffset = 0
		}
		return m, nil
	}

	// Forward to filter text input
	var cmd tea.Cmd
	oldVal := m.templateFilter.Value()
	m.templateFilter, cmd = m.templateFilter.Update(msg)
	if m.templateFilter.Value() != oldVal {
		m.applyTemplateFilter()
		m.templateCursor = 0
		m.templateScrollOffset = 0
	}
	return m, cmd
}

// updateTemplateListFocused handles keys when the template list has focus.
func (m Model) updateTemplateListFocused(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.templateFilter.Value() != "" {
			// Clear filter
			m.templateFilter.SetValue("")
			m.applyTemplateFilter()
			m.templateCursor = 0
			m.templateScrollOffset = 0
			return m, nil
		}
		// Go back to language step
		m.step = stepLang
		return m, nil

	case "up", "k":
		if m.templateCursor > 0 {
			m.templateCursor--
			m.adjustTemplateScroll()
		} else {
			// At top — move focus to filter
			m.templateFilterFocused = true
			return m, m.templateFilter.Focus()
		}
		return m, nil

	case "down", "j":
		if m.templateCursor < len(m.filteredTemplates)-1 {
			m.templateCursor++
			m.adjustTemplateScroll()
		}
		return m, nil

	case "enter":
		if m.templateCursor >= 0 && m.templateCursor < len(m.filteredTemplates) {
			tmpl := m.filteredTemplates[m.templateCursor]
			m.selectedTemplate = &tmpl
			m.step = stepCreating
			return m, tea.Batch(
				m.spinner.Tick,
				func() tea.Msg {
					return CreateProjectMsg{
						Name:     m.projectName,
						Template: tmpl.Name,
						Dir:      m.rootDir,
					}
				},
			)
		}
		return m, nil
	}
	return m, nil
}

// applyTemplateFilter filters the template list by the current filter value.
func (m *Model) applyTemplateFilter() {
	query := strings.ToLower(m.templateFilter.Value())
	if query == "" {
		m.filteredTemplates = m.templates
		return
	}
	var result []wcfg.TemplateInfo
	for _, t := range m.templates {
		if strings.Contains(strings.ToLower(t.Name), query) ||
			strings.Contains(strings.ToLower(t.Label), query) ||
			strings.Contains(strings.ToLower(t.Description), query) {
			result = append(result, t)
		}
	}
	m.filteredTemplates = result
}

// adjustTemplateScroll ensures the cursor is visible within the scroll window.
func (m *Model) adjustTemplateScroll() {
	// Scroll down if cursor is below the visible window.
	if m.templateCursor >= m.templateScrollOffset+maxVisibleTemplates {
		m.templateScrollOffset = m.templateCursor - maxVisibleTemplates + 1
	}
	// Scroll up if cursor is above the visible window.
	if m.templateCursor < m.templateScrollOffset {
		m.templateScrollOffset = m.templateCursor
	}
	// Clamp.
	maxOffset := len(m.filteredTemplates) - maxVisibleTemplates
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.templateScrollOffset > maxOffset {
		m.templateScrollOffset = maxOffset
	}
	if m.templateScrollOffset < 0 {
		m.templateScrollOffset = 0
	}
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
		m.logPath = msg.LogPath
		return m, nil
	}

	if m.selectedTemplate != nil {
		m.resultMsg = fmt.Sprintf("Project %q created from %q", msg.Name, m.selectedTemplate.Label)
	} else {
		m.resultMsg = fmt.Sprintf("Project %q created", msg.Name)
	}
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
	if popupWidth > 80 {
		popupWidth = 80
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
	case stepFetchingTemplates:
		body = m.viewFetchingTemplates()
		help = ""
	case stepTemplate:
		body = m.viewTemplate(popupWidth)
		if m.templateFilterFocused {
			help = "  type to filter  |  down list  |  esc back"
		} else {
			help = "  enter select  |  j/k navigate  |  esc back"
		}
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

	if m.templateFetchErr != "" {
		lines = append(lines, "")
		lines = append(lines, theme.ErrorStyle.Render("  "+m.templateFetchErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewFetchingTemplates() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("  %s %s",
		m.spinner.View(),
		theme.DimStyle.Render("Fetching templates from GitHub...")))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Loading available Cloudflare templates."))
	return strings.Join(lines, "\n")
}

func (m Model) viewTemplate(popupWidth int) string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Select a template for %q:", m.projectName)))
	lines = append(lines, "")

	// Filter line
	var filterLine string
	if m.templateFilterFocused {
		filterLine = fmt.Sprintf("  %s %s",
			theme.LabelStyle.Render("Filter:"),
			m.templateFilter.View())
	} else {
		filterLine = fmt.Sprintf("  %s %s",
			theme.DimStyle.Render("Filter:"),
			m.templateFilter.View())
	}
	lines = append(lines, filterLine)

	// Count + base URL
	countStr := fmt.Sprintf("%d template(s)", len(m.filteredTemplates))
	if len(m.filteredTemplates) != len(m.templates) {
		countStr = fmt.Sprintf("%d of %d template(s)", len(m.filteredTemplates), len(m.templates))
	}
	countLine := theme.DimStyle.Render(fmt.Sprintf("  %s · github.com/cloudflare/templates", countStr))
	lines = append(lines, countLine)
	lines = append(lines, "")

	if len(m.filteredTemplates) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No templates match the filter."))
		return strings.Join(lines, "\n")
	}

	total := len(m.filteredTemplates)
	offset := m.templateScrollOffset

	// Clamp offset (defensive).
	maxOffset := total - maxVisibleTemplates
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}

	// Determine visible window.
	endIdx := offset + maxVisibleTemplates
	if endIdx > total {
		endIdx = total
	}
	visible := m.filteredTemplates[offset:endIdx]

	// Scroll-up indicator.
	if offset > 0 {
		sepLine := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
			fmt.Sprintf("  %s", strings.Repeat("─", popupWidth-8)))
		lines = append(lines,
			theme.DimStyle.Render(fmt.Sprintf("  ▲ %d more above", offset)),
			sepLine)
	}

	// Render visible template entries.
	for vi, tmpl := range visible {
		absIdx := offset + vi // absolute index in filteredTemplates
		cursor := "  "
		if !m.templateFilterFocused && absIdx == m.templateCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		// Label line: published indicator + label
		var labelLine string
		if tmpl.Published {
			indicator := theme.SuccessStyle.Render("✓")
			label := theme.ActionItemStyle.Render(tmpl.Label)
			labelLine = fmt.Sprintf("%s  %s %s", cursor, indicator, label)
		} else {
			indicator := theme.DimStyle.Render("○")
			label := theme.DimStyle.Render(tmpl.Label)
			hint := theme.DimStyle.Render("(community/experimental)")
			labelLine = fmt.Sprintf("%s  %s %s  %s", cursor, indicator, label, hint)
		}
		lines = append(lines, labelLine)

		// Description line
		desc := tmpl.Description
		if desc == "" {
			desc = "No description available"
		}
		lines = append(lines, theme.DimStyle.Render("      "+desc))

		// Blank separator between visible entries (not after the last one)
		if vi < len(visible)-1 {
			lines = append(lines, "")
		}
	}

	// Scroll-down indicator.
	remaining := total - endIdx
	if remaining > 0 {
		sepLine := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
			fmt.Sprintf("  %s", strings.Repeat("─", popupWidth-8)))
		lines = append(lines,
			sepLine,
			theme.DimStyle.Render(fmt.Sprintf("  ▼ %d more below", remaining)))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewCreating() string {
	var lines []string
	if m.selectedTemplate != nil {
		lines = append(lines, fmt.Sprintf("  %s %s",
			m.spinner.View(),
			theme.DimStyle.Render(fmt.Sprintf("Creating project %q from template...", m.projectName))))
	} else {
		lines = append(lines, fmt.Sprintf("  %s %s",
			m.spinner.View(),
			theme.DimStyle.Render(fmt.Sprintf("Creating project %q...", m.projectName))))
	}
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
		if m.logPath != "" {
			lines = append(lines, "")
			lines = append(lines, theme.DimStyle.Render("  Full log: "+m.logPath))
		}
	} else {
		lines = append(lines, theme.SuccessStyle.Render("  "+m.resultMsg))
		lines = append(lines, "")
		if m.selectedTemplate != nil {
			lines = append(lines, theme.DimStyle.Render("  For further reading:"))
			lines = append(lines, theme.DimStyle.Render("  "+m.selectedTemplate.URL))
			lines = append(lines, "")
		}
		lines = append(lines, theme.DimStyle.Render("  Use Ctrl+N to add bindings and Ctrl+P to add environments."))
	}
	return strings.Join(lines, "\n")
}

// IsCreating returns true when the popup is in the creating or fetching step (spinner active).
func (m Model) IsCreating() bool {
	return m.step == stepCreating || m.step == stepFetchingTemplates
}
