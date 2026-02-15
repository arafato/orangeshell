package ai

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// ContextSource represents a selectable log source in the context panel.
type ContextSource struct {
	Name      string // display name (e.g., "my-worker" or "my-worker [dev]")
	ScriptID  string // internal script name (may have dev: prefix)
	IsDev     bool
	DevKind   string // "local" or "remote"
	Selected  bool
	LineCount int
	Active    bool // true if tail/dev session is active
}

// contextModel holds the context panel state.
type contextModel struct {
	sources []ContextSource
	cursor  int
	scrollY int
}

func newContextModel() contextModel {
	return contextModel{}
}

// SetSources replaces the current sources list, preserving selection state
// for sources that still exist.
func (c *contextModel) SetSources(sources []ContextSource) {
	// Build a map of previous selection state
	prevSelected := make(map[string]bool)
	for _, s := range c.sources {
		if s.Selected {
			prevSelected[s.ScriptID] = true
		}
	}

	c.sources = sources

	// Restore selection state
	for i := range c.sources {
		if prevSelected[c.sources[i].ScriptID] {
			c.sources[i].Selected = true
		}
	}

	// Clamp cursor
	if c.cursor >= len(c.sources) {
		c.cursor = len(c.sources) - 1
	}
	if c.cursor < 0 {
		c.cursor = 0
	}
}

// SelectedSources returns the currently selected context sources.
func (c contextModel) SelectedSources() []ContextSource {
	var selected []ContextSource
	for _, s := range c.sources {
		if s.Selected {
			selected = append(selected, s)
		}
	}
	return selected
}

// SelectedScriptIDs returns the script IDs of selected sources.
func (c contextModel) SelectedScriptIDs() []string {
	var ids []string
	for _, s := range c.sources {
		if s.Selected {
			ids = append(ids, s.ScriptID)
		}
	}
	return ids
}

// HasSources returns true if there are any context sources available.
func (c contextModel) HasSources() bool {
	return len(c.sources) > 0
}

// --- Update ---

// visibleListHeight returns the number of visible lines in the source list.
// This is a rough estimate based on the view layout.
const contextListVisibleRows = 20

func (c contextModel) update(msg tea.Msg, focused bool) (contextModel, tea.Cmd) {
	if !focused {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if c.cursor < len(c.sources)-1 {
				c.cursor++
				// Scroll down if cursor goes past visible area
				if c.cursor >= c.scrollY+contextListVisibleRows {
					c.scrollY = c.cursor - contextListVisibleRows + 1
				}
			}
		case "k", "up":
			if c.cursor > 0 {
				c.cursor--
				// Scroll up if cursor goes above visible area
				if c.cursor < c.scrollY {
					c.scrollY = c.cursor
				}
			}
		case " ":
			// Toggle selection of current source
			if c.cursor >= 0 && c.cursor < len(c.sources) {
				c.sources[c.cursor].Selected = !c.sources[c.cursor].Selected
			}
		case "a":
			// Select all
			for i := range c.sources {
				c.sources[i].Selected = true
			}
		case "n":
			// Deselect all
			for i := range c.sources {
				c.sources[i].Selected = false
			}
		}
	}
	return c, nil
}

// --- View ---

func (c contextModel) view(w, h int, focused bool) string {
	var borderStyle lipgloss.Style
	if focused {
		borderStyle = theme.ActiveBorderStyle.
			Width(w - 2).
			Height(h - 2)
	} else {
		borderStyle = theme.BorderStyle.
			Width(w - 2).
			Height(h - 2)
	}

	innerW := w - 4 // border + minimal padding
	innerH := h - 4

	title := theme.TitleStyle.Render("Context Sources")

	if len(c.sources) == 0 {
		hint := theme.DimStyle.Render("No log sources available.")
		sub1 := theme.DimStyle.Render("Start tailing workers in the")
		sub2 := theme.DimStyle.Render("Monitoring tab to add context.")
		content := lipgloss.JoinVertical(lipgloss.Left, title, "", hint, sub1, sub2)
		return borderStyle.Render(content)
	}

	// Stats line
	selectedCount := 0
	totalLines := 0
	for _, s := range c.sources {
		if s.Selected {
			selectedCount++
			totalLines += s.LineCount
		}
	}
	stats := theme.DimStyle.Render(
		fmt.Sprintf("%d/%d selected  ~%d lines  ~%dK tokens",
			selectedCount, len(c.sources), totalLines, totalLines*30/4000), // rough estimate
	)

	// Source list
	var lines []string
	for i, s := range c.sources {
		line := c.renderSourceLine(s, i == c.cursor, focused, innerW)
		lines = append(lines, line)
	}

	// Apply scroll
	listH := innerH - 3 // title + stats + blank line
	if listH < 1 {
		listH = 1
	}

	// Ensure cursor is visible
	if c.cursor < c.scrollY {
		// would adjust scrollY, but value receiver — handled visually
	}
	scrollY := c.scrollY
	if c.cursor >= scrollY+listH {
		scrollY = c.cursor - listH + 1
	}
	if c.cursor < scrollY {
		scrollY = c.cursor
	}
	if scrollY < 0 {
		scrollY = 0
	}

	start := scrollY
	end := start + listH
	if end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		start = len(lines)
	}
	visible := lines[start:end]

	// Pad to fill height
	for len(visible) < listH {
		visible = append(visible, "")
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		stats,
		"",
	)
	content += "\n" + strings.Join(visible, "\n")

	return borderStyle.Render(content)
}

func (c contextModel) renderSourceLine(s ContextSource, isCursor, paneFocused bool, maxW int) string {
	// Checkbox
	check := "[ ]"
	if s.Selected {
		check = lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("[x]")
	}

	// Cursor indicator
	cursor := "  "
	if isCursor && paneFocused {
		cursor = lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render("> ")
	}

	// Name with badges
	name := s.Name
	if s.IsDev {
		devBadge := lipgloss.NewStyle().Foreground(theme.ColorYellow).Render("[dev]")
		name = fmt.Sprintf("%s %s", name, devBadge)
	}

	// Status
	var status string
	if s.Active {
		status = lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("*")
	} else {
		status = theme.DimStyle.Render("-")
	}

	// Line count
	lineInfo := theme.DimStyle.Render(fmt.Sprintf("(%d)", s.LineCount))

	return fmt.Sprintf("%s%s %s %s %s", cursor, check, status, name, lineInfo)
}
