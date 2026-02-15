package config

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/ui/envvars"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Env Variables Update ---

func (m Model) updateEnvVars(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.mode {
	case modeNormal:
		return m.updateEnvVarsList(msg)
	case modeEdit:
		return m.updateEnvVarsEdit(msg)
	case modeAdd:
		return m.updateEnvVarsAdd(msg)
	case modeDelete:
		return m.updateEnvVarsDelete(msg)
	}
	return m, nil
}

func (m Model) updateEnvVarsList(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	// When filter is active, handle filter-specific keys
	if m.envVarsFilterActive {
		switch key {
		case "esc":
			if m.envVarsFilter.Value() != "" {
				m.envVarsFilter.SetValue("")
				m.envVarsFilterActive = false
				m.envVarsFilter.Blur()
				m.envVarsCursor = 0
				m.envVarsScrollY = 0
				return m, nil
			}
			m.envVarsFilterActive = false
			m.envVarsFilter.Blur()
			return m, nil
		case "enter", "down", "j":
			// Exit filter, focus list
			m.envVarsFilterActive = false
			m.envVarsFilter.Blur()
			m.envVarsCursor = 0
			return m, nil
		}
		// Forward to filter input
		var cmd tea.Cmd
		old := m.envVarsFilter.Value()
		m.envVarsFilter, cmd = m.envVarsFilter.Update(msg)
		if m.envVarsFilter.Value() != old {
			m.envVarsCursor = 0
			m.envVarsScrollY = 0
		}
		return m, cmd
	}

	filtered := m.filteredEnvVars()
	switch key {
	case "/":
		m.envVarsFilterActive = true
		return m, m.envVarsFilter.Focus()
	case "j", "down":
		if m.envVarsCursor < len(filtered)-1 {
			m.envVarsCursor++
		}
		return m, nil
	case "k", "up":
		if m.envVarsCursor > 0 {
			m.envVarsCursor--
		}
		return m, nil
	case "a":
		m.mode = modeAdd
		m.evEditNameInput.SetValue("")
		m.evEditValueInput.SetValue("")
		m.errMsg = ""
		// Default env cursor
		m.evEditEnvCursor = 0
		envNames := m.envNames()
		if len(envNames) > 0 {
			m.evEditEnvName = envNames[0]
		}
		m.evAddFocusField = addFieldEnv
		return m, nil
	case "enter":
		if m.envVarsCursor >= 0 && m.envVarsCursor < len(filtered) {
			v := filtered[m.envVarsCursor]
			m.mode = modeEdit
			m.evEditEnvName = v.EnvName
			m.evEditOrigName = v.Name
			m.evEditValueInput.SetValue(v.Value)
			m.errMsg = ""
			return m, m.evEditValueInput.Focus()
		}
		return m, nil
	case "d":
		if m.envVarsCursor >= 0 && m.envVarsCursor < len(filtered) {
			v := filtered[m.envVarsCursor]
			m.mode = modeDelete
			m.evDeleteTarget = &v
			m.confirmCursor = 0
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) updateEnvVarsEdit(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		return m, nil
	case "enter":
		value := m.evEditValueInput.Value()
		configPath := m.configPath
		envName := m.evEditEnvName
		varName := m.evEditOrigName
		return m, func() tea.Msg {
			return envvars.SetVarMsg{
				ConfigPath: configPath,
				EnvName:    envName,
				VarName:    varName,
				Value:      value,
			}
		}
	}
	var cmd tea.Cmd
	m.evEditValueInput, cmd = m.evEditValueInput.Update(msg)
	return m, cmd
}

func (m Model) updateEnvVarsAdd(msg tea.KeyMsg) (Model, tea.Cmd) {
	envNames := m.envNames()

	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		m.evEditNameInput.Blur()
		m.evEditValueInput.Blur()
		return m, nil
	case "tab":
		switch m.evAddFocusField {
		case addFieldEnv:
			m.evAddFocusField = addFieldName
			return m, m.evEditNameInput.Focus()
		case addFieldName:
			m.evEditNameInput.Blur()
			m.evAddFocusField = addFieldValue
			return m, m.evEditValueInput.Focus()
		case addFieldValue:
			m.evEditValueInput.Blur()
			m.evAddFocusField = addFieldEnv
			return m, nil
		}
	case "left":
		if m.evAddFocusField == addFieldEnv && len(envNames) > 0 {
			m.evEditEnvCursor = (m.evEditEnvCursor - 1 + len(envNames)) % len(envNames)
			m.evEditEnvName = envNames[m.evEditEnvCursor]
			return m, nil
		}
	case "right":
		if m.evAddFocusField == addFieldEnv && len(envNames) > 0 {
			m.evEditEnvCursor = (m.evEditEnvCursor + 1) % len(envNames)
			m.evEditEnvName = envNames[m.evEditEnvCursor]
			return m, nil
		}
	case "enter":
		if m.evAddFocusField == addFieldEnv {
			m.evAddFocusField = addFieldName
			return m, m.evEditNameInput.Focus()
		}
		name := strings.TrimSpace(m.evEditNameInput.Value())
		if name == "" {
			m.errMsg = "Variable name cannot be empty"
			return m, nil
		}
		if !isValidVarName(name) {
			m.errMsg = "Must start with letter/underscore, then alphanumeric/underscores"
			return m, nil
		}
		value := m.evEditValueInput.Value()
		configPath := m.configPath
		envName := m.evEditEnvName
		return m, func() tea.Msg {
			return envvars.SetVarMsg{
				ConfigPath: configPath,
				EnvName:    envName,
				VarName:    name,
				Value:      value,
			}
		}
	}

	// Forward to focused input
	var cmd tea.Cmd
	switch m.evAddFocusField {
	case addFieldName:
		m.evEditNameInput, cmd = m.evEditNameInput.Update(msg)
	case addFieldValue:
		m.evEditValueInput, cmd = m.evEditValueInput.Update(msg)
	}
	m.errMsg = ""
	return m, cmd
}

func (m Model) updateEnvVarsDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.confirmCursor > 0 {
			m.confirmCursor--
		}
		return m, nil
	case "right", "l":
		if m.confirmCursor < 1 {
			m.confirmCursor++
		}
		return m, nil
	case "enter":
		if m.confirmCursor == 0 {
			// "No" selected — cancel
			m.mode = modeNormal
			m.evDeleteTarget = nil
			return m, nil
		}
		// "Yes" selected — delete
		if m.evDeleteTarget != nil {
			v := m.evDeleteTarget
			return m, func() tea.Msg {
				return envvars.DeleteVarMsg{
					ConfigPath: v.ConfigPath,
					EnvName:    v.EnvName,
					VarName:    v.Name,
				}
			}
		}
		m.mode = modeNormal
		m.evDeleteTarget = nil
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.evDeleteTarget = nil
		return m, nil
	}
	return m, nil
}

// --- Env Variables View ---

func (m Model) viewEnvVars() []string {
	var lines []string

	// Filter bar
	if m.envVarsFilterActive {
		lines = append(lines, fmt.Sprintf("  %s %s",
			theme.LabelStyle.Render("Filter:"),
			m.envVarsFilter.View()))
	} else if m.envVarsFilter.Value() != "" {
		lines = append(lines, fmt.Sprintf("  %s %s",
			theme.DimStyle.Render("Filter:"),
			theme.DimStyle.Render(m.envVarsFilter.Value())))
	}

	switch m.mode {
	case modeNormal:
		lines = append(lines, m.viewEnvVarsList()...)
	case modeEdit:
		lines = append(lines, m.viewEnvVarsEdit()...)
	case modeAdd:
		lines = append(lines, m.viewEnvVarsAddForm()...)
	case modeDelete:
		lines = append(lines, m.viewEnvVarsDelete()...)
	}

	return lines
}

func (m Model) viewEnvVarsList() []string {
	var lines []string
	filtered := m.filteredEnvVars()

	if len(filtered) == 0 {
		if len(m.envVars) == 0 {
			lines = append(lines, theme.DimStyle.Render("  No environment variables defined."))
			lines = append(lines, theme.DimStyle.Render("  Press 'a' to add one."))
		} else {
			lines = append(lines, theme.DimStyle.Render("  No variables match the filter."))
		}
		return lines
	}

	// Count + add hint
	if len(filtered) != len(m.envVars) {
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %d of %d variable(s)", len(filtered), len(m.envVars))))
	} else {
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %d variable(s)", len(m.envVars))))
	}
	lines = append(lines, theme.SuccessStyle.Render("  + Add Variable (a)"))
	lines = append(lines, "")

	// Stacked per-environment sections
	boxWidth := m.width - 6
	if boxWidth < 40 {
		boxWidth = 40
	}

	prevEnv := ""
	for i, v := range filtered {
		if v.EnvName != prevEnv {
			if prevEnv != "" {
				// Close previous section
				lines = append(lines, "")
			}
			prevEnv = v.EnvName
			// Section header
			header := m.renderSectionHeader(v.EnvName, boxWidth)
			lines = append(lines, header)
		}

		cursor := "    "
		nameStyle := theme.NormalItemStyle
		if !m.envVarsFilterActive && i == m.envVarsCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}

		// Truncate value if too wide
		maxValWidth := boxWidth - 30
		if maxValWidth < 10 {
			maxValWidth = 10
		}
		displayValue := v.Value
		if len(displayValue) > maxValWidth {
			displayValue = displayValue[:maxValWidth-3] + "..."
		}

		line := fmt.Sprintf("%s%s = %s",
			cursor,
			nameStyle.Render(fmt.Sprintf("%-20s", v.Name)),
			theme.ValueStyle.Render(fmt.Sprintf("%q", displayValue)))
		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewEnvVarsEdit() []string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Editing [%s] %s:", m.evEditEnvName, m.evEditOrigName)))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", theme.LabelStyle.Render("Value:")))
	lines = append(lines, "  "+m.evEditValueInput.View())
	return lines
}

func (m Model) viewEnvVarsAddForm() []string {
	var lines []string
	envNames := m.envNames()

	lines = append(lines, theme.DimStyle.Render("  Adding variable:"))
	lines = append(lines, "")

	// Env selector
	envName := envNames[m.evEditEnvCursor]
	if m.evAddFocusField == addFieldEnv {
		arrows := theme.DimStyle.Render("<") + " " + theme.SelectedItemStyle.Render(envName) + " " + theme.DimStyle.Render(">")
		lines = append(lines, fmt.Sprintf("  %s  %s", theme.SelectedItemStyle.Render("Env:"), arrows))
	} else {
		lines = append(lines, fmt.Sprintf("  %s  %s", "Env:", theme.ValueStyle.Render(envName)))
	}
	lines = append(lines, "")

	// Name field
	nameLabel := "Name:"
	if m.evAddFocusField == addFieldName {
		nameLabel = theme.SelectedItemStyle.Render("Name:")
	}
	lines = append(lines, fmt.Sprintf("  %s", nameLabel))
	lines = append(lines, "  "+m.evEditNameInput.View())
	lines = append(lines, "")

	// Value field
	valueLabel := "Value:"
	if m.evAddFocusField == addFieldValue {
		valueLabel = theme.SelectedItemStyle.Render("Value:")
	}
	lines = append(lines, fmt.Sprintf("  %s", valueLabel))
	lines = append(lines, "  "+m.evEditValueInput.View())

	return lines
}

func (m Model) viewEnvVarsDelete() []string {
	if m.evDeleteTarget == nil {
		return nil
	}
	return viewDeleteConfirmBox(
		"Delete Variable",
		fmt.Sprintf("Remove %s from environment [%s]?", m.evDeleteTarget.Name, m.evDeleteTarget.EnvName),
		m.confirmCursor,
	)
}

// --- Env Variables Help ---

func (m Model) helpEnvVars(base []HelpEntry) []HelpEntry {
	switch m.mode {
	case modeNormal:
		if m.envVarsFilterActive {
			return append(base, HelpEntry{"esc", "clear"}, HelpEntry{"enter", "list"})
		}
		return append(base,
			HelpEntry{"j/k", "navigate"},
			HelpEntry{"a", "add"},
			HelpEntry{"enter", "edit"},
			HelpEntry{"d", "delete"},
			HelpEntry{"/", "filter"},
			HelpEntry{"q", "quit"},
		)
	case modeEdit:
		return []HelpEntry{{"esc", "cancel"}, {"enter", "save"}}
	case modeAdd:
		return []HelpEntry{{"esc", "cancel"}, {"tab", "next field"}, {"enter", "save"}}
	case modeDelete:
		return []HelpEntry{{"h/l", "select"}, {"enter", "confirm"}, {"esc", "cancel"}}
	}
	return base
}

// --- Helpers ---

func (m Model) filteredEnvVars() []envVarItem {
	query := strings.ToLower(m.envVarsFilter.Value())
	if query == "" {
		return m.envVars
	}
	var result []envVarItem
	for _, v := range m.envVars {
		if strings.Contains(strings.ToLower(v.EnvName), query) ||
			strings.Contains(strings.ToLower(v.Name), query) ||
			strings.Contains(strings.ToLower(v.Value), query) {
			result = append(result, v)
		}
	}
	return result
}

func (m Model) envNames() []string {
	if m.config != nil {
		return m.config.EnvNames()
	}
	return []string{"default"}
}

func (m Model) renderSectionHeader(envName string, width int) string {
	label := fmt.Sprintf(" %s ", envName)
	lineWidth := width - len(label) - 4
	if lineWidth < 0 {
		lineWidth = 0
	}
	left := strings.Repeat("─", 2)
	right := strings.Repeat("─", lineWidth)
	return theme.DimStyle.Render(fmt.Sprintf("  %s%s%s", left, label, right))
}

func isValidVarName(name string) bool {
	if len(name) == 0 {
		return false
	}
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
