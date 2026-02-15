package config

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Messages ---

// OpenBindingsWizardMsg is emitted when the user wants to add a binding via the popup wizard.
type OpenBindingsWizardMsg struct {
	ConfigPath string
	EnvName    string
	WorkerName string
}

// DeleteBindingMsg requests removing a binding from the wrangler config.
type DeleteBindingMsg struct {
	ConfigPath  string
	EnvName     string
	BindingName string
	BindingType string
}

// DeleteBindingDoneMsg delivers the result.
type DeleteBindingDoneMsg struct {
	Err error
}

// --- Bindings Update ---

func (m Model) updateBindings(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.mode {
	case modeNormal:
		return m.updateBindingsList(msg)
	case modeDelete:
		return m.updateBindingsDelete(msg)
	}
	return m, nil
}

func (m Model) updateBindingsList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.bindingsCursor < len(m.bindingItems)-1 {
			m.bindingsCursor++
		}
		return m, nil
	case "k", "up":
		if m.bindingsCursor > 0 {
			m.bindingsCursor--
		}
		return m, nil
	case "a":
		// Emit message to open the popup wizard
		if m.config == nil {
			return m, nil
		}
		configPath := m.configPath
		workerName := m.config.Name
		// Determine current env from cursor position
		envName := "default"
		if m.bindingsCursor >= 0 && m.bindingsCursor < len(m.bindingItems) {
			envName = m.bindingItems[m.bindingsCursor].EnvName
		}
		return m, func() tea.Msg {
			return OpenBindingsWizardMsg{
				ConfigPath: configPath,
				EnvName:    envName,
				WorkerName: workerName,
			}
		}
	case "d":
		if m.bindingsCursor >= 0 && m.bindingsCursor < len(m.bindingItems) {
			b := m.bindingItems[m.bindingsCursor]
			m.mode = modeDelete
			m.bindingsDeleteTarget = &b
			m.confirmCursor = 0
		}
		return m, nil
	case "enter":
		// Navigate to the resource in the Resources tab
		if m.bindingsCursor >= 0 && m.bindingsCursor < len(m.bindingItems) {
			b := m.bindingItems[m.bindingsCursor]
			navService := b.Binding.NavService()
			if navService != "" {
				return m, func() tea.Msg {
					return NavigateToResourceMsg{
						ServiceName: navService,
						ResourceID:  b.Binding.ResourceID,
					}
				}
			}
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) updateBindingsDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
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
			m.bindingsDeleteTarget = nil
			return m, nil
		}
		// "Yes" selected — delete
		if m.bindingsDeleteTarget != nil {
			b := m.bindingsDeleteTarget
			return m, func() tea.Msg {
				return DeleteBindingMsg{
					ConfigPath:  b.ConfigPath,
					EnvName:     b.EnvName,
					BindingName: b.Binding.Name,
					BindingType: b.Binding.Type,
				}
			}
		}
		m.mode = modeNormal
		m.bindingsDeleteTarget = nil
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.bindingsDeleteTarget = nil
		return m, nil
	}
	return m, nil
}

// --- Bindings View ---

func (m Model) viewBindings() []string {
	switch m.mode {
	case modeNormal:
		return m.viewBindingsList()
	case modeDelete:
		return m.viewBindingsDeleteConfirm()
	}
	return nil
}

func (m Model) viewBindingsList() []string {
	var lines []string

	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %d binding(s)", len(m.bindingItems))))
	lines = append(lines, theme.SuccessStyle.Render("  + Add Binding (a)"))
	lines = append(lines, "")

	if len(m.bindingItems) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No bindings defined yet."))
		return lines
	}

	boxWidth := m.width - 6
	if boxWidth < 40 {
		boxWidth = 40
	}

	// Stacked per-environment sections
	prevEnv := ""
	for i, b := range m.bindingItems {
		if b.EnvName != prevEnv {
			if prevEnv != "" {
				lines = append(lines, "")
			}
			prevEnv = b.EnvName
			header := m.renderSectionHeader(b.EnvName, boxWidth)
			lines = append(lines, header)
		}

		cursor := "    "
		nameStyle := theme.NormalItemStyle
		if i == m.bindingsCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}

		// Type badge
		typeBadge := renderTypeBadge(b.Binding.TypeLabel())

		// Arrow for navigable bindings
		arrow := ""
		if b.Binding.NavService() != "" {
			arrow = " " + theme.ActionNavArrowStyle.Render("->")
		}

		// Resource ID
		resourceID := theme.DimStyle.Render(b.Binding.ResourceID)
		if len(b.Binding.ResourceID) > boxWidth-40 && boxWidth > 43 {
			resourceID = theme.DimStyle.Render(b.Binding.ResourceID[:boxWidth-43] + "...")
		}

		line := fmt.Sprintf("%s%s %s %s %s%s",
			cursor,
			typeBadge,
			nameStyle.Render(b.Binding.Name),
			theme.DimStyle.Render("→"),
			resourceID,
			arrow,
		)
		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewBindingsDeleteConfirm() []string {
	if m.bindingsDeleteTarget == nil {
		return nil
	}
	b := m.bindingsDeleteTarget
	return viewDeleteConfirmBox(
		"Delete Binding",
		fmt.Sprintf("Remove %s [%s] from environment [%s]?", b.Binding.Name, b.Binding.TypeLabel(), b.EnvName),
		m.confirmCursor,
	)
}

// --- Bindings Help ---

func (m Model) helpBindings(base []HelpEntry) []HelpEntry {
	switch m.mode {
	case modeNormal:
		entries := append(base, HelpEntry{"j/k", "navigate"}, HelpEntry{"a", "add"})
		if len(m.bindingItems) > 0 {
			entries = append(entries, HelpEntry{"d", "delete"})
			entries = append(entries, HelpEntry{"enter", "open"})
		}
		return append(entries, HelpEntry{"q", "quit"})
	case modeDelete:
		return []HelpEntry{{"h/l", "select"}, {"enter", "confirm"}, {"esc", "cancel"}}
	}
	return base
}

// --- Type badge rendering ---

func renderTypeBadge(label string) string {
	color := typeColor(label)
	return lipgloss.NewStyle().
		Foreground(theme.ColorWhite).
		Background(color).
		Padding(0, 0).
		Render(fmt.Sprintf("[%s]", label))
}

func typeColor(label string) lipgloss.Color {
	switch label {
	case "KV":
		return theme.ColorBlue
	case "R2":
		return theme.ColorGreen
	case "D1":
		return theme.ColorOrange
	case "Service":
		return theme.ColorYellow
	case "DO":
		return theme.ColorRed
	default:
		return theme.ColorGray
	}
}
