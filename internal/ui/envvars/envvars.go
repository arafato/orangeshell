package envvars

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Messages emitted by this component (handled by app.go) ---

// CloseMsg signals the envvars view should close and return to the wrangler view.
type CloseMsg struct{}

// SetVarMsg requests the app to write a var into the wrangler config.
type SetVarMsg struct {
	ConfigPath string
	EnvName    string
	VarName    string
	Value      string
}

// DeleteVarMsg requests the app to remove a var from the wrangler config.
type DeleteVarMsg struct {
	ConfigPath string
	EnvName    string
	VarName    string
}

// DoneMsg signals a mutation succeeded; the app should re-parse the config.
type DoneMsg struct {
	ConfigPath string
}

// SetVarDoneMsg delivers the result of a SetVar operation.
type SetVarDoneMsg struct {
	Err error
}

// DeleteVarDoneMsg delivers the result of a DeleteVar operation.
type DeleteVarDoneMsg struct {
	Err error
}

// --- Data model ---

// EnvVar represents a single environment variable from a wrangler config.
type EnvVar struct {
	EnvName     string // "default", "staging", etc.
	Name        string // "API_HOST"
	Value       string // "example.com"
	ConfigPath  string // path to wrangler config
	ProjectName string // project name (for multi-project context)
}

// --- Mode ---

type mode int

const (
	modeList   mode = iota // Browsing/filtering vars
	modeEdit               // Editing an existing var's value
	modeAdd                // Adding a new var (name + value)
	modeDelete             // Inline delete confirmation
)

// --- Model ---

// Model is the env vars view state.
type Model struct {
	vars     []EnvVar // all env vars (flat list)
	filtered []EnvVar // after applying filter
	filter   textinput.Model
	cursor   int
	mode     mode

	// Filter focus: when true, the filter bar is the active item and all
	// printable keys go to the text input. When false, focus is on the
	// var list and a/d are shortcuts. Arrow keys move between the two.
	filterFocused bool

	// Context
	configPath    string
	projectName   string
	sourceEnvName string // the environment the user navigated from

	// Edit mode
	editNameInput  textinput.Model
	editValueInput textinput.Model
	editEnvName    string // target env for the edit/add
	editOrigName   string // original var name (for edit, empty for add)

	// Delete confirmation
	deleteTarget *EnvVar

	// Error display
	errMsg string

	// Dimensions
	width   int
	height  int
	scrollY int
}

// New creates a new envvars view model with the given variables.
// envName is the environment the user navigated from (used as default for add
// and displayed in the title).
func New(configPath, projectName, envName string, vars []EnvVar) Model {
	fi := textinput.New()
	fi.Placeholder = "Type to filter..."
	fi.CharLimit = 100
	fi.Width = 40
	fi.Prompt = "> "
	fi.PromptStyle = theme.SelectedItemStyle
	fi.TextStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
	fi.PlaceholderStyle = theme.DimStyle
	fi.Focus()

	ni := textinput.New()
	ni.Placeholder = "VARIABLE_NAME"
	ni.CharLimit = 128
	ni.Width = 40
	ni.Prompt = "  "
	ni.PromptStyle = theme.SelectedItemStyle
	ni.TextStyle = theme.ValueStyle
	ni.PlaceholderStyle = theme.DimStyle

	vi := textinput.New()
	vi.Placeholder = "value"
	vi.CharLimit = 1024
	vi.Width = 60
	vi.Prompt = "  "
	vi.PromptStyle = theme.SelectedItemStyle
	vi.TextStyle = theme.ValueStyle
	vi.PlaceholderStyle = theme.DimStyle

	sorted := sortVars(vars)

	m := Model{
		vars:           sorted,
		filtered:       sorted,
		filter:         fi,
		filterFocused:  true,
		configPath:     configPath,
		projectName:    projectName,
		sourceEnvName:  envName,
		editNameInput:  ni,
		editValueInput: vi,
		mode:           modeList,
	}

	// Prepopulate filter with the source environment name so the user
	// immediately sees only variables relevant to that environment.
	if envName != "" {
		m.filter.SetValue(envName)
		m.applyFilter()
	}

	return m
}

// SetSize updates the dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetVars replaces the var list (called after config re-parse).
func (m *Model) SetVars(vars []EnvVar) {
	m.vars = sortVars(vars)
	m.applyFilter()
}

// --- Update ---

// Update handles messages for the envvars view.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case SetVarDoneMsg:
		if msg.Err != nil {
			m.errMsg = fmt.Sprintf("Failed to set var: %v", msg.Err)
			m.mode = modeList
			m.filterFocused = false
			m.filter.Blur()
			return m, nil
		}
		// Success — emit DoneMsg so app re-parses config
		m.mode = modeList
		m.errMsg = ""
		m.filterFocused = false
		m.filter.Blur()
		configPath := m.configPath
		return m, func() tea.Msg { return DoneMsg{ConfigPath: configPath} }

	case DeleteVarDoneMsg:
		if msg.Err != nil {
			m.errMsg = fmt.Sprintf("Failed to delete var: %v", msg.Err)
			m.mode = modeList
			m.deleteTarget = nil
			m.filterFocused = false
			m.filter.Blur()
			return m, nil
		}
		// Success — emit DoneMsg
		m.mode = modeList
		m.deleteTarget = nil
		m.errMsg = ""
		m.filterFocused = false
		m.filter.Blur()
		configPath := m.configPath
		return m, func() tea.Msg { return DoneMsg{ConfigPath: configPath} }

	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeEdit:
			return m.updateEdit(msg)
		case modeAdd:
			return m.updateAdd(msg)
		case modeDelete:
			return m.updateDelete(msg)
		}
	}

	// Forward non-key messages to active text inputs (cursor blink, etc.)
	var cmd tea.Cmd
	switch m.mode {
	case modeList:
		if m.filterFocused {
			m.filter, cmd = m.filter.Update(msg)
		}
	case modeEdit:
		m.editValueInput, cmd = m.editValueInput.Update(msg)
	case modeAdd:
		if m.editNameInput.Focused() {
			m.editNameInput, cmd = m.editNameInput.Update(msg)
		} else {
			m.editValueInput, cmd = m.editValueInput.Update(msg)
		}
	}
	return m, cmd
}

// updateList handles keys in list mode. Behaviour depends on whether the
// filter bar or the var list has focus.
func (m Model) updateList(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.filterFocused {
		return m.updateFilterFocused(msg)
	}
	return m.updateItemFocused(msg)
}

// updateFilterFocused handles keys when the filter bar is the active element.
// All printable keys go to the text input. Navigation keys move focus to the list.
func (m Model) updateFilterFocused(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.filter.Value() != "" {
			// First esc: clear the filter
			m.filter.SetValue("")
			m.applyFilter()
			m.cursor = 0
			m.scrollY = 0
			return m, nil
		}
		// Second esc: close the view
		return m, func() tea.Msg { return CloseMsg{} }

	case "down":
		// Move focus from filter to the first list item
		m.filterFocused = false
		m.filter.Blur()
		m.cursor = 0
		return m, nil

	case "enter":
		// Enter while in filter: move focus to list so user can interact
		if len(m.filtered) > 0 {
			m.filterFocused = false
			m.filter.Blur()
			m.cursor = 0
		}
		return m, nil
	}

	// Forward everything else to the filter text input
	var cmd tea.Cmd
	oldVal := m.filter.Value()
	m.filter, cmd = m.filter.Update(msg)
	if m.filter.Value() != oldVal {
		m.applyFilter()
		m.cursor = 0
		m.scrollY = 0
	}
	return m, cmd
}

// updateItemFocused handles keys when the var list has focus.
// a/d are shortcuts, arrow keys navigate, up at cursor 0 moves focus to filter.
func (m Model) updateItemFocused(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.filter.Value() != "" {
			// Clear filter, stay on list
			m.filter.SetValue("")
			m.applyFilter()
			m.cursor = 0
			m.scrollY = 0
			return m, nil
		}
		return m, func() tea.Msg { return CloseMsg{} }

	case "up":
		if m.cursor > 0 {
			m.cursor--
			m.adjustScroll()
		} else {
			// At top of list — move focus to filter
			m.filterFocused = true
			return m, m.filter.Focus()
		}
		return m, nil

	case "down":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.adjustScroll()
		}
		return m, nil

	case "enter":
		// Edit selected var
		if m.cursor >= 0 && m.cursor < len(m.filtered) {
			v := m.filtered[m.cursor]
			m.mode = modeEdit
			m.editEnvName = v.EnvName
			m.editOrigName = v.Name
			m.editValueInput.SetValue(v.Value)
			m.errMsg = ""
			return m, m.editValueInput.Focus()
		}
		return m, nil

	case "a":
		// Add new var — default to the source env the user navigated from
		m.mode = modeAdd
		m.editNameInput.SetValue("")
		m.editValueInput.SetValue("")
		m.errMsg = ""
		m.editEnvName = m.sourceEnvName
		if m.editEnvName == "" {
			m.editEnvName = "default"
		}
		return m, m.editNameInput.Focus()

	case "d":
		// Delete selected var (show inline confirmation)
		if m.cursor >= 0 && m.cursor < len(m.filtered) {
			v := m.filtered[m.cursor]
			m.mode = modeDelete
			m.deleteTarget = &v
		}
		return m, nil
	}

	return m, nil
}

// updateEdit handles keys in edit mode (editing an existing var's value).
func (m Model) updateEdit(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errMsg = ""
		// Return focus to the list (not the filter)
		m.filterFocused = false
		m.filter.Blur()
		return m, nil

	case "enter":
		value := m.editValueInput.Value()
		configPath := m.configPath
		envName := m.editEnvName
		varName := m.editOrigName
		return m, func() tea.Msg {
			return SetVarMsg{
				ConfigPath: configPath,
				EnvName:    envName,
				VarName:    varName,
				Value:      value,
			}
		}
	}

	var cmd tea.Cmd
	m.editValueInput, cmd = m.editValueInput.Update(msg)
	return m, cmd
}

// updateAdd handles keys in add mode (name input first, then value).
func (m Model) updateAdd(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errMsg = ""
		m.filterFocused = false
		m.filter.Blur()
		return m, nil

	case "tab":
		// Toggle focus between name and value inputs
		if m.editNameInput.Focused() {
			m.editNameInput.Blur()
			return m, m.editValueInput.Focus()
		}
		m.editValueInput.Blur()
		return m, m.editNameInput.Focus()

	case "enter":
		name := strings.TrimSpace(m.editNameInput.Value())
		if name == "" {
			m.errMsg = "Variable name cannot be empty"
			return m, nil
		}
		if !isValidVarName(name) {
			m.errMsg = "Must start with letter/underscore, then alphanumeric/underscores"
			return m, nil
		}
		value := m.editValueInput.Value()
		configPath := m.configPath
		envName := m.editEnvName
		return m, func() tea.Msg {
			return SetVarMsg{
				ConfigPath: configPath,
				EnvName:    envName,
				VarName:    name,
				Value:      value,
			}
		}
	}

	// Forward to the focused input
	var cmd tea.Cmd
	if m.editNameInput.Focused() {
		m.editNameInput, cmd = m.editNameInput.Update(msg)
	} else {
		m.editValueInput, cmd = m.editValueInput.Update(msg)
	}
	m.errMsg = ""
	return m, cmd
}

// updateDelete handles keys in delete confirmation mode.
func (m Model) updateDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.deleteTarget != nil {
			v := m.deleteTarget
			return m, func() tea.Msg {
				return DeleteVarMsg{
					ConfigPath: v.ConfigPath,
					EnvName:    v.EnvName,
					VarName:    v.Name,
				}
			}
		}
		m.mode = modeList
		m.deleteTarget = nil
		m.filterFocused = false
		m.filter.Blur()
		return m, nil

	case "n", "N", "esc":
		m.mode = modeList
		m.deleteTarget = nil
		m.filterFocused = false
		m.filter.Blur()
		return m, nil
	}
	return m, nil
}

// --- View ---

// View renders the envvars panel (full-width, replaces the wrangler view content).
func (m Model) View(termWidth, termHeight int) string {
	contentHeight := termHeight - 4
	if contentHeight < 1 {
		contentHeight = 1
	}
	boxWidth := termWidth - 4
	if boxWidth < 40 {
		boxWidth = 40
	}

	// Title: "Environment Variables — <project> - <env>"
	titleText := "  Environment Variables"
	if m.projectName != "" && m.sourceEnvName != "" {
		titleText = fmt.Sprintf("  Environment Variables — %s - %s", m.projectName, m.sourceEnvName)
	} else if m.projectName != "" {
		titleText = fmt.Sprintf("  Environment Variables — %s", m.projectName)
	} else if m.sourceEnvName != "" {
		titleText = fmt.Sprintf("  Environment Variables — %s", m.sourceEnvName)
	}
	title := theme.TitleStyle.Render(titleText)

	sepWidth := termWidth - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Filter line — show a selection indicator when focused
	var filterLine string
	if m.filterFocused && m.mode == modeList {
		filterLine = fmt.Sprintf("  %s %s",
			theme.LabelStyle.Render("Filter:"),
			m.filter.View())
	} else {
		filterLine = fmt.Sprintf("  %s %s",
			theme.DimStyle.Render("Filter:"),
			m.filter.View())
	}

	// Count
	countText := theme.DimStyle.Render(fmt.Sprintf("  %d variable(s)", len(m.filtered)))
	if len(m.filtered) != len(m.vars) {
		countText = theme.DimStyle.Render(fmt.Sprintf("  %d of %d variable(s)", len(m.filtered), len(m.vars)))
	}

	var allLines []string
	allLines = append(allLines, title, sep, filterLine, countText, "")

	switch m.mode {
	case modeList:
		allLines = append(allLines, m.viewList(boxWidth)...)
	case modeEdit:
		allLines = append(allLines, m.viewEdit()...)
	case modeAdd:
		allLines = append(allLines, m.viewAdd()...)
	case modeDelete:
		allLines = append(allLines, m.viewDelete()...)
	}

	// Error message
	if m.errMsg != "" {
		allLines = append(allLines, "")
		allLines = append(allLines, theme.ErrorStyle.Render("  "+m.errMsg))
	}

	// Help text
	allLines = append(allLines, "")
	allLines = append(allLines, m.viewHelp())

	// Apply vertical scrolling
	visibleHeight := contentHeight
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

	// Pad to exact height
	for len(visible) < visibleHeight {
		visible = append(visible, "")
	}

	content := strings.Join(visible, "\n")

	// Border
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(0, 1).
		Width(termWidth - 2)

	return borderStyle.Render(content)
}

func (m Model) viewList(boxWidth int) []string {
	var lines []string

	if len(m.filtered) == 0 {
		if len(m.vars) == 0 {
			lines = append(lines, theme.DimStyle.Render("  No environment variables defined."))
			lines = append(lines, theme.DimStyle.Render("  Navigate to list and press 'a' to add one."))
		} else {
			lines = append(lines, theme.DimStyle.Render("  No variables match the filter."))
		}
		return lines
	}

	// Group by env name for visual separation
	prevEnv := ""
	for i, v := range m.filtered {
		if v.EnvName != prevEnv {
			if prevEnv != "" {
				lines = append(lines, "") // spacer between groups
			}
			prevEnv = v.EnvName
		}

		cursor := "  "
		nameStyle := theme.NormalItemStyle
		if !m.filterFocused && i == m.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			nameStyle = theme.SelectedItemStyle
		}

		envTag := theme.DimStyle.Render(fmt.Sprintf("[%-10s]", v.EnvName))

		// Truncate value if too wide
		maxValueWidth := boxWidth - 30
		if maxValueWidth < 10 {
			maxValueWidth = 10
		}
		displayValue := v.Value
		if len(displayValue) > maxValueWidth {
			displayValue = displayValue[:maxValueWidth-3] + "..."
		}

		line := fmt.Sprintf("%s%s  %s = %s",
			cursor,
			envTag,
			nameStyle.Render(fmt.Sprintf("%-20s", v.Name)),
			theme.ValueStyle.Render(fmt.Sprintf("%q", displayValue)))

		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewEdit() []string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Editing [%s] %s:", m.editEnvName, m.editOrigName)))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", theme.LabelStyle.Render("Value:")))
	lines = append(lines, m.editValueInput.View())
	return lines
}

func (m Model) viewAdd() []string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Adding variable to [%s]:", m.editEnvName)))
	lines = append(lines, "")

	nameFocused := m.editNameInput.Focused()
	nameLabel := "Name:"
	valueLabel := "Value:"
	if nameFocused {
		nameLabel = theme.SelectedItemStyle.Render("Name:")
	} else {
		valueLabel = theme.SelectedItemStyle.Render("Value:")
	}

	lines = append(lines, fmt.Sprintf("  %s", nameLabel))
	lines = append(lines, m.editNameInput.View())
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", valueLabel))
	lines = append(lines, m.editValueInput.View())
	return lines
}

func (m Model) viewDelete() []string {
	var lines []string
	if m.deleteTarget != nil {
		lines = append(lines, theme.ErrorStyle.Render(
			fmt.Sprintf("  Delete %s from [%s]? (y/n)", m.deleteTarget.Name, m.deleteTarget.EnvName)))
	}
	return lines
}

func (m Model) viewHelp() string {
	switch m.mode {
	case modeList:
		if m.filterFocused {
			return theme.DimStyle.Render("  type to filter  |  down list  |  esc back")
		}
		return theme.DimStyle.Render("  up/down navigate  |  enter edit  |  a add  |  d delete  |  up filter  |  esc back")
	case modeEdit:
		return theme.DimStyle.Render("  esc cancel  |  enter save")
	case modeAdd:
		return theme.DimStyle.Render("  esc cancel  |  tab switch field  |  enter save")
	case modeDelete:
		return theme.DimStyle.Render("  y confirm  |  n cancel")
	}
	return ""
}

// --- Helpers ---

func sortVars(vars []EnvVar) []EnvVar {
	sorted := make([]EnvVar, len(vars))
	copy(sorted, vars)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].EnvName != sorted[j].EnvName {
			if sorted[i].EnvName == "default" {
				return true
			}
			if sorted[j].EnvName == "default" {
				return false
			}
			return sorted[i].EnvName < sorted[j].EnvName
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func (m *Model) applyFilter() {
	query := strings.ToLower(m.filter.Value())
	if query == "" {
		m.filtered = m.vars
		return
	}

	var result []EnvVar
	for _, v := range m.vars {
		if strings.Contains(strings.ToLower(v.EnvName), query) ||
			strings.Contains(strings.ToLower(v.Name), query) ||
			strings.Contains(strings.ToLower(v.Value), query) {
			result = append(result, v)
		}
	}
	m.filtered = result
}

func (m *Model) adjustScroll() {
	// Keep cursor visible within the rendered list area
	// Estimate: 5 header lines + 1 line per var + group spacers
	visibleItems := m.height - 10
	if visibleItems < 3 {
		visibleItems = 3
	}
	if m.cursor < m.scrollY {
		m.scrollY = m.cursor
	}
	if m.cursor >= m.scrollY+visibleItems {
		m.scrollY = m.cursor - visibleItems + 1
	}
	if m.scrollY < 0 {
		m.scrollY = 0
	}
}

func isValidVarName(name string) bool {
	if len(name) == 0 {
		return false
	}
	// First character must be a letter or underscore (not a digit)
	first := name[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		return false
	}
	for _, c := range name[1:] {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}
