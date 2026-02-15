package config

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/ui/envpopup"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Environments Update ---

func (m Model) updateEnvironments(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.mode {
	case modeNormal:
		return m.updateEnvsList(msg)
	case modeAdd:
		return m.updateEnvsAdd(msg)
	case modeDelete:
		return m.updateEnvsDelete(msg)
	}
	return m, nil
}

func (m Model) updateEnvsList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.envsCursor < len(m.envsList)-1 {
			m.envsCursor++
		}
		return m, nil
	case "k", "up":
		if m.envsCursor > 0 {
			m.envsCursor--
		}
		return m, nil
	case "a":
		m.mode = modeAdd
		m.envsAddInput.SetValue("")
		m.errMsg = ""
		return m, m.envsAddInput.Focus()
	case "d":
		if m.envsCursor >= 0 && m.envsCursor < len(m.envsList) {
			envName := m.envsList[m.envsCursor]
			if envName == "default" {
				m.errMsg = "Cannot delete the default environment"
				return m, nil
			}
			m.mode = modeDelete
			m.envsDeleteTarget = envName
			m.confirmCursor = 0
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) updateEnvsAdd(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		m.envsAddInput.Blur()
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.envsAddInput.Value())
		if name == "" {
			m.errMsg = "Environment name cannot be empty"
			return m, nil
		}
		// Check for duplicates
		for _, existing := range m.envsList {
			if existing == name {
				m.errMsg = fmt.Sprintf("Environment %q already exists", name)
				return m, nil
			}
		}
		// Validate: lowercase letters, numbers, hyphens
		if !isValidEnvName(name) {
			m.errMsg = "Use lowercase letters, numbers, and hyphens"
			return m, nil
		}
		configPath := m.configPath
		return m, func() tea.Msg {
			return envpopup.CreateEnvMsg{ConfigPath: configPath, EnvName: name}
		}
	}
	var cmd tea.Cmd
	m.envsAddInput, cmd = m.envsAddInput.Update(msg)
	m.errMsg = ""
	return m, cmd
}

func (m Model) updateEnvsDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
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
			m.envsDeleteTarget = ""
			return m, nil
		}
		// "Yes" selected — delete
		if m.envsDeleteTarget != "" {
			configPath := m.configPath
			envName := m.envsDeleteTarget
			return m, func() tea.Msg {
				return envpopup.DeleteEnvMsg{ConfigPath: configPath, EnvName: envName}
			}
		}
		m.mode = modeNormal
		m.envsDeleteTarget = ""
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.envsDeleteTarget = ""
		return m, nil
	}
	return m, nil
}

// --- Environments View ---

func (m Model) viewEnvironments() []string {
	switch m.mode {
	case modeNormal:
		return m.viewEnvsList()
	case modeAdd:
		return m.viewEnvsAddForm()
	case modeDelete:
		return m.viewEnvsDeleteConfirm()
	}
	return nil
}

func (m Model) viewEnvsList() []string {
	var lines []string

	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %d environment(s)", len(m.envsList))))
	lines = append(lines, theme.SuccessStyle.Render("  + Add Environment (a)"))
	lines = append(lines, "")

	if len(m.envsList) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No environments defined yet."))
		return lines
	}

	for i, envName := range m.envsList {
		cursor := "    "
		nameStyle := theme.NormalItemStyle
		if i == m.envsCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}

		suffix := ""
		if envName == "default" {
			suffix = theme.DimStyle.Render("  (top-level)")
		}

		line := fmt.Sprintf("%s%s%s", cursor, nameStyle.Render(envName), suffix)
		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewEnvsAddForm() []string {
	var lines []string
	lines = append(lines, theme.LabelStyle.Render("  New environment name:"))
	lines = append(lines, "")
	lines = append(lines, "  "+m.envsAddInput.View())
	return lines
}

func (m Model) viewEnvsDeleteConfirm() []string {
	if m.envsDeleteTarget == "" {
		return nil
	}
	return viewDeleteConfirmBox(
		"Delete Environment",
		fmt.Sprintf("Remove %q and all its config?", m.envsDeleteTarget),
		m.confirmCursor,
	)
}

// --- Environments Help ---

func (m Model) helpEnvironments(base []HelpEntry) []HelpEntry {
	switch m.mode {
	case modeNormal:
		entries := append(base, HelpEntry{"j/k", "navigate"}, HelpEntry{"a", "add"})
		if len(m.envsList) > 0 {
			entries = append(entries, HelpEntry{"d", "delete"})
		}
		return append(entries, HelpEntry{"q", "quit"})
	case modeAdd:
		return []HelpEntry{{"enter", "save"}, {"esc", "cancel"}}
	case modeDelete:
		return []HelpEntry{{"h/l", "select"}, {"enter", "confirm"}, {"esc", "cancel"}}
	}
	return base
}

// --- Helpers ---

func isValidEnvName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
