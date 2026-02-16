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

// FileSource represents a project's source code toggle in the context panel.
// One entry per project — when selected, all source files from the project
// are included in the AI context.
type FileSource struct {
	ProjectName string              // display name
	ProjectDir  string              // absolute path to project root
	Selected    bool                // include source code yes/no (default: no)
	Summary     *ProjectFileSummary // scanned file summary (nil if not scanned)
}

// contextModel holds the context panel state.
type contextModel struct {
	sources     []ContextSource // log sources from monitoring grid
	fileSources []FileSource    // source file toggles (one per project)
	cursor      int             // cursor position in the combined list
	scrollY     int
}

func newContextModel() contextModel {
	return contextModel{}
}

// --- Combined list helpers ---
//
// The combined list for cursor navigation is:
//   [0..len(sources)-1]                           = log sources
//   [len(sources)]                                = file section header (not selectable)
//   [len(sources)+1..len(sources)+len(fileSources)] = file sources
//
// If fileSources is empty, the header is not shown.
// If sources is empty but fileSources is not, the header is still shown.

// totalItems returns the number of items in the combined list including the
// section header for file sources (if any file sources exist).
func (c contextModel) totalItems() int {
	n := len(c.sources)
	if len(c.fileSources) > 0 {
		n += 1 + len(c.fileSources) // header + file entries
	}
	return n
}

// isFileHeader returns true if the given cursor position is the file section header.
func (c contextModel) isFileHeader(idx int) bool {
	if len(c.fileSources) == 0 {
		return false
	}
	return idx == len(c.sources)
}

// isLogSource returns true if the given cursor position is a log source.
func (c contextModel) isLogSource(idx int) bool {
	return idx >= 0 && idx < len(c.sources)
}

// isFileSource returns true and the file source index if the cursor is on a file source.
func (c contextModel) isFileSource(idx int) (bool, int) {
	if len(c.fileSources) == 0 {
		return false, -1
	}
	fileStart := len(c.sources) + 1 // after log sources + header
	if idx >= fileStart && idx < fileStart+len(c.fileSources) {
		return true, idx - fileStart
	}
	return false, -1
}

// SetSources replaces the current log sources list, preserving selection state
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

	c.clampCursor()
}

// SetFileSources replaces the file source list, preserving selection state.
func (c *contextModel) SetFileSources(fileSources []FileSource) {
	// Build a map of previous selection state
	prevSelected := make(map[string]bool)
	for _, fs := range c.fileSources {
		if fs.Selected {
			prevSelected[fs.ProjectDir] = true
		}
	}

	c.fileSources = fileSources

	// Restore selection state
	for i := range c.fileSources {
		if prevSelected[c.fileSources[i].ProjectDir] {
			c.fileSources[i].Selected = true
		}
	}

	c.clampCursor()
}

func (c *contextModel) clampCursor() {
	total := c.totalItems()
	if total == 0 {
		c.cursor = 0
		return
	}
	if c.cursor >= total {
		c.cursor = total - 1
	}
	if c.cursor < 0 {
		c.cursor = 0
	}
	// If cursor landed on the file section header, advance to the first file source
	if c.isFileHeader(c.cursor) {
		if c.cursor+1 < total {
			c.cursor++
		} else if c.cursor > 0 {
			c.cursor--
		}
	}
}

// SelectedSources returns the currently selected log context sources.
func (c contextModel) SelectedSources() []ContextSource {
	var selected []ContextSource
	for _, s := range c.sources {
		if s.Selected {
			selected = append(selected, s)
		}
	}
	return selected
}

// SelectedScriptIDs returns the script IDs of selected log sources.
func (c contextModel) SelectedScriptIDs() []string {
	var ids []string
	for _, s := range c.sources {
		if s.Selected {
			ids = append(ids, s.ScriptID)
		}
	}
	return ids
}

// SelectedFileSources returns the file sources that are toggled on.
func (c contextModel) SelectedFileSources() []FileSource {
	var selected []FileSource
	for _, fs := range c.fileSources {
		if fs.Selected {
			selected = append(selected, fs)
		}
	}
	return selected
}

// HasSources returns true if there are any context sources (log or file) available.
func (c contextModel) HasSources() bool {
	return len(c.sources) > 0 || len(c.fileSources) > 0
}

// --- Update ---

const contextListVisibleRows = 20

func (c contextModel) update(msg tea.Msg, focused bool) (contextModel, tea.Cmd) {
	if !focused {
		return c, nil
	}

	total := c.totalItems()
	if total == 0 {
		return c, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if c.cursor < total-1 {
				c.cursor++
				// Skip the file section header
				if c.isFileHeader(c.cursor) && c.cursor < total-1 {
					c.cursor++
				}
				if c.cursor >= c.scrollY+contextListVisibleRows {
					c.scrollY = c.cursor - contextListVisibleRows + 1
				}
			}
		case "k", "up":
			if c.cursor > 0 {
				c.cursor--
				// Skip the file section header
				if c.isFileHeader(c.cursor) && c.cursor > 0 {
					c.cursor--
				}
				if c.cursor < c.scrollY {
					c.scrollY = c.cursor
				}
			}
		case " ":
			// Toggle selection of current item
			if c.isLogSource(c.cursor) {
				c.sources[c.cursor].Selected = !c.sources[c.cursor].Selected
			} else if ok, fi := c.isFileSource(c.cursor); ok {
				c.fileSources[fi].Selected = !c.fileSources[fi].Selected
			}
		case "a":
			// Select all (log + file sources)
			for i := range c.sources {
				c.sources[i].Selected = true
			}
			for i := range c.fileSources {
				c.fileSources[i].Selected = true
			}
		case "n":
			// Deselect all
			for i := range c.sources {
				c.sources[i].Selected = false
			}
			for i := range c.fileSources {
				c.fileSources[i].Selected = false
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

	if !c.HasSources() {
		hint := theme.DimStyle.Render("No log sources available.")
		sub1 := theme.DimStyle.Render("Start tailing workers in the")
		sub2 := theme.DimStyle.Render("Monitoring tab to add context.")
		content := lipgloss.JoinVertical(lipgloss.Left, title, "", hint, sub1, sub2)
		return borderStyle.Render(content)
	}

	// Stats line
	selectedCount := 0
	totalLines := 0
	totalFileSize := int64(0)
	totalSelectable := len(c.sources) + len(c.fileSources)
	for _, s := range c.sources {
		if s.Selected {
			selectedCount++
			totalLines += s.LineCount
		}
	}
	for _, fs := range c.fileSources {
		if fs.Selected {
			selectedCount++
			if fs.Summary != nil {
				totalFileSize += fs.Summary.TotalSize
			}
		}
	}

	statsStr := fmt.Sprintf("%d/%d selected", selectedCount, totalSelectable)
	if totalLines > 0 {
		statsStr += fmt.Sprintf("  ~%d lines", totalLines)
	}
	if totalFileSize > 0 {
		statsStr += fmt.Sprintf("  ~%s code", formatSize(totalFileSize))
	}
	stats := theme.DimStyle.Render(statsStr)

	// Build the combined item list for rendering
	var lines []string

	// Log sources
	for i, s := range c.sources {
		line := c.renderLogSourceLine(s, i == c.cursor, focused, innerW)
		lines = append(lines, line)
	}

	// File sources section (if any)
	if len(c.fileSources) > 0 {
		// Section header
		headerStyle := lipgloss.NewStyle().Foreground(theme.ColorDarkGray)
		lines = append(lines, headerStyle.Render("── Source Files ──"))

		fileStart := len(c.sources) + 1 // cursor offset for file sources
		for i, fs := range c.fileSources {
			cursorIdx := fileStart + i
			line := c.renderFileSourceLine(fs, cursorIdx == c.cursor, focused, innerW)
			lines = append(lines, line)
		}
	}

	// Apply scroll
	listH := innerH - 3 // title + stats + blank line
	if listH < 1 {
		listH = 1
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

func (c contextModel) renderLogSourceLine(s ContextSource, isCursor, paneFocused bool, maxW int) string {
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

func (c contextModel) renderFileSourceLine(fs FileSource, isCursor, paneFocused bool, maxW int) string {
	// Checkbox
	check := "[ ]"
	if fs.Selected {
		check = lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("[x]")
	}

	// Cursor indicator
	cursor := "  "
	if isCursor && paneFocused {
		cursor = lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true).Render("> ")
	}

	// Project name
	name := fs.ProjectName

	// File summary
	summaryStr := theme.DimStyle.Render("(no files)")
	if fs.Summary != nil && len(fs.Summary.Files) > 0 {
		summaryStr = theme.DimStyle.Render(fmt.Sprintf("(%s)", FormatFileSummary(fs.Summary)))
	}

	// File icon
	icon := lipgloss.NewStyle().Foreground(theme.ColorBlue).Render("~")

	return fmt.Sprintf("%s%s %s %s %s", cursor, check, icon, name, summaryStr)
}
