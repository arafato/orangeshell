package triggers

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Messages emitted by this component (handled by app.go) ---

// CloseMsg signals the triggers view should close and return to the wrangler view.
type CloseMsg struct{}

// AddCronMsg requests the app to add a cron trigger to the wrangler config.
type AddCronMsg struct {
	ConfigPath string
	Cron       string
}

// DeleteCronMsg requests the app to remove a cron trigger from the wrangler config.
type DeleteCronMsg struct {
	ConfigPath string
	Cron       string
}

// DoneMsg signals a mutation succeeded; the app should re-parse the config.
type DoneMsg struct {
	ConfigPath string
}

// AddCronDoneMsg delivers the result of an AddCron operation.
type AddCronDoneMsg struct {
	Err error
}

// DeleteCronDoneMsg delivers the result of a DeleteCron operation.
type DeleteCronDoneMsg struct {
	Err error
}

// --- Presets ---

type cronPreset struct {
	Label string
	Cron  string
}

var presets = []cronPreset{
	{Label: "Every minute", Cron: "* * * * *"},
	{Label: "Every 5 minutes", Cron: "*/5 * * * *"},
	{Label: "Every 15 minutes", Cron: "*/15 * * * *"},
	{Label: "Hourly", Cron: "0 * * * *"},
	{Label: "Daily (midnight)", Cron: "0 0 * * *"},
	{Label: "Weekly (Sun midnight)", Cron: "0 0 * * 0"},
	{Label: "Monthly (1st midnight)", Cron: "0 0 1 * *"},
	{Label: "Custom...", Cron: ""},
}

// --- Mode ---

type mode int

const (
	modeList      mode = iota // Browsing cron triggers
	modeAdd                   // Preset picker
	modeAddCustom             // Custom cron text input
	modeDelete                // Inline delete confirmation
)

// --- Model ---

// Model is the triggers view state.
type Model struct {
	crons  []string // current cron trigger expressions
	cursor int
	mode   mode

	// Context
	configPath  string
	projectName string

	// Add mode (preset picker)
	presetCursor int

	// Custom cron input
	customInput textinput.Model

	// Delete confirmation
	deleteTarget string

	// Error display
	errMsg string

	// Dimensions
	width   int
	height  int
	scrollY int
}

// New creates a new triggers view model.
func New(configPath, projectName string, crons []string) Model {
	ci := textinput.New()
	ci.Placeholder = "*/5 * * * *"
	ci.CharLimit = 50
	ci.Width = 40

	return Model{
		crons:       crons,
		configPath:  configPath,
		projectName: projectName,
		customInput: ci,
	}
}

// SetSize updates the terminal dimensions for the view.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetCrons replaces the cron list (called after config re-parse).
func (m *Model) SetCrons(crons []string) {
	m.crons = crons
	if m.cursor >= len(crons) {
		m.cursor = len(crons) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// --- Update ---

// Update handles messages for the triggers view.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case AddCronDoneMsg:
		if msg.Err != nil {
			m.errMsg = fmt.Sprintf("Failed to add cron: %v", msg.Err)
			m.mode = modeList
			return m, nil
		}
		m.mode = modeList
		m.errMsg = ""
		configPath := m.configPath
		return m, func() tea.Msg { return DoneMsg{ConfigPath: configPath} }

	case DeleteCronDoneMsg:
		if msg.Err != nil {
			m.errMsg = fmt.Sprintf("Failed to delete cron: %v", msg.Err)
			m.mode = modeList
			m.deleteTarget = ""
			return m, nil
		}
		m.mode = modeList
		m.deleteTarget = ""
		m.errMsg = ""
		configPath := m.configPath
		return m, func() tea.Msg { return DoneMsg{ConfigPath: configPath} }

	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeAdd:
			return m.updateAdd(msg)
		case modeAddCustom:
			return m.updateAddCustom(msg)
		case modeDelete:
			return m.updateDelete(msg)
		}
	}

	// Forward non-key messages to text input (cursor blink)
	if m.mode == modeAddCustom {
		var cmd tea.Cmd
		m.customInput, cmd = m.customInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return CloseMsg{} }

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.adjustScroll()
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(m.crons)-1 {
			m.cursor++
			m.adjustScroll()
		}
		return m, nil

	case "a":
		m.mode = modeAdd
		m.presetCursor = 0
		m.errMsg = ""
		return m, nil

	case "d":
		if m.cursor >= 0 && m.cursor < len(m.crons) {
			m.mode = modeDelete
			m.deleteTarget = m.crons[m.cursor]
		}
		return m, nil
	}

	return m, nil
}

func (m Model) updateAdd(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errMsg = ""
		return m, nil

	case "up", "k":
		if m.presetCursor > 0 {
			m.presetCursor--
		}
		return m, nil

	case "down", "j":
		if m.presetCursor < len(presets)-1 {
			m.presetCursor++
		}
		return m, nil

	case "enter":
		p := presets[m.presetCursor]
		if p.Cron == "" {
			// Custom — switch to text input mode
			m.mode = modeAddCustom
			m.customInput.SetValue("")
			return m, m.customInput.Focus()
		}
		// Check for duplicate
		for _, existing := range m.crons {
			if existing == p.Cron {
				m.errMsg = fmt.Sprintf("Cron %q already exists", p.Cron)
				return m, nil
			}
		}
		configPath := m.configPath
		cron := p.Cron
		return m, func() tea.Msg {
			return AddCronMsg{ConfigPath: configPath, Cron: cron}
		}
	}

	return m, nil
}

func (m Model) updateAddCustom(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeAdd
		m.errMsg = ""
		return m, nil

	case "enter":
		cron := strings.TrimSpace(m.customInput.Value())
		if cron == "" {
			m.errMsg = "Cron expression cannot be empty"
			return m, nil
		}
		// Basic validation: should have 5 space-separated fields
		parts := strings.Fields(cron)
		if len(parts) != 5 {
			m.errMsg = "Cron must have 5 fields: min hour day month weekday"
			return m, nil
		}
		// Check for duplicate
		for _, existing := range m.crons {
			if existing == cron {
				m.errMsg = fmt.Sprintf("Cron %q already exists", cron)
				return m, nil
			}
		}
		configPath := m.configPath
		return m, func() tea.Msg {
			return AddCronMsg{ConfigPath: configPath, Cron: cron}
		}
	}

	var cmd tea.Cmd
	m.customInput, cmd = m.customInput.Update(msg)
	m.errMsg = ""
	return m, cmd
}

func (m Model) updateDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.deleteTarget != "" {
			configPath := m.configPath
			cron := m.deleteTarget
			return m, func() tea.Msg {
				return DeleteCronMsg{ConfigPath: configPath, Cron: cron}
			}
		}
		m.mode = modeList
		m.deleteTarget = ""
		return m, nil

	case "n", "N", "esc":
		m.mode = modeList
		m.deleteTarget = ""
		return m, nil
	}
	return m, nil
}

// --- View ---

// View renders the triggers view as a full-panel replacement.
func (m Model) View(termWidth, termHeight int) string {
	contentHeight := termHeight - 4
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Title
	titleText := "  Cron Triggers"
	if m.projectName != "" {
		titleText = fmt.Sprintf("  Cron Triggers — %s", m.projectName)
	}
	title := theme.TitleStyle.Render(titleText)

	sepWidth := termWidth - 6
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Count
	countText := theme.DimStyle.Render(fmt.Sprintf("  %d cron trigger(s)", len(m.crons)))

	var allLines []string
	allLines = append(allLines, title, sep, countText, "")

	switch m.mode {
	case modeList:
		allLines = append(allLines, m.viewList()...)
	case modeAdd:
		allLines = append(allLines, m.viewAdd()...)
	case modeAddCustom:
		allLines = append(allLines, m.viewAddCustom()...)
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

func (m Model) viewList() []string {
	var lines []string

	if len(m.crons) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No cron triggers defined."))
		lines = append(lines, theme.DimStyle.Render("  Press 'a' to add one."))
		return lines
	}

	for i, cron := range m.crons {
		cursor := "  "
		cronStyle := theme.NormalItemStyle
		if i == m.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			cronStyle = theme.SelectedItemStyle
		}

		desc := describeCron(cron)
		line := fmt.Sprintf("%s%s  %s",
			cursor,
			cronStyle.Render(cron),
			theme.DimStyle.Render(desc))
		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewAdd() []string {
	var lines []string
	lines = append(lines, theme.LabelStyle.Render("  Select a cron schedule:"))
	lines = append(lines, "")

	for i, p := range presets {
		cursor := "  "
		labelStyle := theme.NormalItemStyle
		if i == m.presetCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			labelStyle = theme.SelectedItemStyle
		}

		if p.Cron != "" {
			line := fmt.Sprintf("%s%s  %s",
				cursor,
				labelStyle.Render(p.Label),
				theme.DimStyle.Render(p.Cron))
			lines = append(lines, line)
		} else {
			lines = append(lines, fmt.Sprintf("%s%s", cursor, labelStyle.Render(p.Label)))
		}
	}

	return lines
}

func (m Model) viewAddCustom() []string {
	var lines []string
	lines = append(lines, theme.LabelStyle.Render("  Enter cron expression (5 fields: min hour day month weekday):"))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", m.customInput.View()))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Examples: */5 * * * *  |  0 0 * * *  |  0 15 1 * *"))
	return lines
}

func (m Model) viewDelete() []string {
	var lines []string
	if m.deleteTarget != "" {
		lines = append(lines, theme.ErrorStyle.Render(
			fmt.Sprintf("  Delete cron trigger %q? (y/n)", m.deleteTarget)))
	}
	return lines
}

func (m Model) viewHelp() string {
	switch m.mode {
	case modeList:
		if len(m.crons) == 0 {
			return theme.DimStyle.Render("  a add  |  esc back")
		}
		return theme.DimStyle.Render("  j/k navigate  |  a add  |  d delete  |  esc back")
	case modeAdd:
		return theme.DimStyle.Render("  j/k navigate  |  enter select  |  esc back")
	case modeAddCustom:
		return theme.DimStyle.Render("  enter save  |  esc back")
	case modeDelete:
		return theme.DimStyle.Render("  y confirm  |  n cancel")
	}
	return ""
}

// --- Helpers ---

func (m *Model) adjustScroll() {
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

// describeCron returns a human-readable description of a cron expression.
func describeCron(cron string) string {
	switch cron {
	case "* * * * *":
		return "every minute"
	case "*/5 * * * *":
		return "every 5 minutes"
	case "*/15 * * * *":
		return "every 15 minutes"
	case "0 * * * *":
		return "hourly"
	case "0 0 * * *":
		return "daily at midnight"
	case "0 0 * * 0":
		return "weekly on Sunday"
	case "0 0 1 * *":
		return "monthly on the 1st"
	}

	parts := strings.Fields(cron)
	if len(parts) != 5 {
		return ""
	}

	// Try to describe simple patterns
	min, hour, dom, mon, dow := parts[0], parts[1], parts[2], parts[3], parts[4]

	if strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && mon == "*" && dow == "*" {
		return fmt.Sprintf("every %s minutes", min[2:])
	}
	if min != "*" && hour == "*" && dom == "*" && mon == "*" && dow == "*" {
		return fmt.Sprintf("hourly at :%s", min)
	}
	if min != "*" && hour != "*" && dom == "*" && mon == "*" && dow == "*" {
		return fmt.Sprintf("daily at %s:%s", hour, min)
	}

	return ""
}
