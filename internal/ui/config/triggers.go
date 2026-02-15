package config

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	uitriggers "github.com/oarafat/orangeshell/internal/ui/triggers"
)

// --- Cron presets (same as triggers package) ---

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

// --- Triggers Update ---

func (m Model) updateTriggers(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.mode {
	case modeNormal:
		return m.updateTriggersList(msg)
	case modeAddPreset:
		return m.updateTriggersPreset(msg)
	case modeAddCustom:
		return m.updateTriggersCustom(msg)
	case modeDelete:
		return m.updateTriggersDelete(msg)
	}
	return m, nil
}

func (m Model) updateTriggersList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.triggersCursor < len(m.triggersCrons)-1 {
			m.triggersCursor++
		}
		return m, nil
	case "k", "up":
		if m.triggersCursor > 0 {
			m.triggersCursor--
		}
		return m, nil
	case "a":
		m.mode = modeAddPreset
		m.triggersPresetCursor = 0
		m.errMsg = ""
		return m, nil
	case "d":
		if m.triggersCursor >= 0 && m.triggersCursor < len(m.triggersCrons) {
			m.mode = modeDelete
			m.triggersDeleteTarget = m.triggersCrons[m.triggersCursor]
			m.confirmCursor = 0
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) updateTriggersPreset(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		return m, nil
	case "j", "down":
		if m.triggersPresetCursor < len(presets)-1 {
			m.triggersPresetCursor++
		}
		return m, nil
	case "k", "up":
		if m.triggersPresetCursor > 0 {
			m.triggersPresetCursor--
		}
		return m, nil
	case "enter":
		p := presets[m.triggersPresetCursor]
		if p.Cron == "" {
			// Custom
			m.mode = modeAddCustom
			m.triggersCustomInput.SetValue("")
			return m, m.triggersCustomInput.Focus()
		}
		// Check duplicate
		for _, existing := range m.triggersCrons {
			if existing == p.Cron {
				m.errMsg = fmt.Sprintf("Cron %q already exists", p.Cron)
				return m, nil
			}
		}
		configPath := m.configPath
		cron := p.Cron
		return m, func() tea.Msg {
			return uitriggers.AddCronMsg{ConfigPath: configPath, Cron: cron}
		}
	}
	return m, nil
}

func (m Model) updateTriggersCustom(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeAddPreset
		m.errMsg = ""
		return m, nil
	case "enter":
		cron := strings.TrimSpace(m.triggersCustomInput.Value())
		if cron == "" {
			m.errMsg = "Cron expression cannot be empty"
			return m, nil
		}
		parts := strings.Fields(cron)
		if len(parts) != 5 {
			m.errMsg = "Cron must have 5 fields: min hour day month weekday"
			return m, nil
		}
		for _, existing := range m.triggersCrons {
			if existing == cron {
				m.errMsg = fmt.Sprintf("Cron %q already exists", cron)
				return m, nil
			}
		}
		configPath := m.configPath
		return m, func() tea.Msg {
			return uitriggers.AddCronMsg{ConfigPath: configPath, Cron: cron}
		}
	}
	var cmd tea.Cmd
	m.triggersCustomInput, cmd = m.triggersCustomInput.Update(msg)
	m.errMsg = ""
	return m, cmd
}

func (m Model) updateTriggersDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
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
			m.triggersDeleteTarget = ""
			return m, nil
		}
		// "Yes" selected — delete
		if m.triggersDeleteTarget != "" {
			configPath := m.configPath
			cron := m.triggersDeleteTarget
			return m, func() tea.Msg {
				return uitriggers.DeleteCronMsg{ConfigPath: configPath, Cron: cron}
			}
		}
		m.mode = modeNormal
		m.triggersDeleteTarget = ""
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.triggersDeleteTarget = ""
		return m, nil
	}
	return m, nil
}

// --- Triggers View ---

func (m Model) viewTriggers() []string {
	switch m.mode {
	case modeNormal:
		return m.viewTriggersList()
	case modeAddPreset:
		return m.viewTriggersPreset()
	case modeAddCustom:
		return m.viewTriggersCustom()
	case modeDelete:
		return m.viewTriggersDeleteConfirm()
	}
	return nil
}

func (m Model) viewTriggersList() []string {
	var lines []string

	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %d cron trigger(s)", len(m.triggersCrons))))
	lines = append(lines, theme.SuccessStyle.Render("  + Add Trigger (a)"))
	lines = append(lines, "")

	if len(m.triggersCrons) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No cron triggers defined yet."))
		return lines
	}

	for i, cron := range m.triggersCrons {
		cursor := "    "
		cronStyle := theme.NormalItemStyle
		if i == m.triggersCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			cronStyle = theme.SelectedItemStyle
		}
		desc := describeCron(cron)
		line := fmt.Sprintf("%s%s  %s", cursor, cronStyle.Render(cron), theme.DimStyle.Render(desc))
		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewTriggersPreset() []string {
	var lines []string
	lines = append(lines, theme.LabelStyle.Render("  Select a cron schedule:"))
	lines = append(lines, "")

	for i, p := range presets {
		cursor := "    "
		labelStyle := theme.NormalItemStyle
		if i == m.triggersPresetCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			labelStyle = theme.SelectedItemStyle
		}
		if p.Cron != "" {
			lines = append(lines, fmt.Sprintf("%s%s  %s", cursor, labelStyle.Render(p.Label), theme.DimStyle.Render(p.Cron)))
		} else {
			lines = append(lines, fmt.Sprintf("%s%s", cursor, labelStyle.Render(p.Label)))
		}
	}

	return lines
}

func (m Model) viewTriggersCustom() []string {
	var lines []string
	lines = append(lines, theme.LabelStyle.Render("  Enter cron expression (5 fields: min hour day month weekday):"))
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s", m.triggersCustomInput.View()))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Examples: */5 * * * *  |  0 0 * * *  |  0 15 1 * *"))
	return lines
}

func (m Model) viewTriggersDeleteConfirm() []string {
	if m.triggersDeleteTarget == "" {
		return nil
	}
	return viewDeleteConfirmBox(
		"Delete Trigger",
		fmt.Sprintf("Remove cron trigger %q?", m.triggersDeleteTarget),
		m.confirmCursor,
	)
}

// --- Triggers Help ---

func (m Model) helpTriggers(base []HelpEntry) []HelpEntry {
	switch m.mode {
	case modeNormal:
		entries := append(base, HelpEntry{"j/k", "navigate"}, HelpEntry{"a", "add"})
		if len(m.triggersCrons) > 0 {
			entries = append(entries, HelpEntry{"d", "delete"})
		}
		return append(entries, HelpEntry{"q", "quit"})
	case modeAddPreset:
		return []HelpEntry{{"j/k", "navigate"}, {"enter", "select"}, {"esc", "back"}}
	case modeAddCustom:
		return []HelpEntry{{"enter", "save"}, {"esc", "back"}}
	case modeDelete:
		return []HelpEntry{{"h/l", "select"}, {"enter", "confirm"}, {"esc", "cancel"}}
	}
	return base
}

// --- Cron helpers ---

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
