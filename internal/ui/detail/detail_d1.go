package detail

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- D1 SQL Console helpers ---

// PreviewD1Schema sets up the D1 database ID and marks the schema as loading,
// without initializing the interactive SQL console. Used in preview mode so the
// schema is visible in the read-only detail view.
func (m *Model) PreviewD1Schema(databaseID string) {
	m.d1DatabaseID = databaseID
	m.d1SchemaTables = nil
	m.d1SchemaErr = ""
	m.d1SchemaLoading = true
}

// InitD1Console initializes the D1 SQL console for a database.
// Preserves schema data if it was already loaded in preview mode for the same database.
func (m *Model) InitD1Console(databaseID string) tea.Cmd {
	preserveSchema := m.d1DatabaseID == databaseID && len(m.d1SchemaTables) > 0
	m.d1Active = true
	m.d1DatabaseID = databaseID
	m.d1Output = nil
	m.d1Querying = false
	if !preserveSchema {
		m.d1SchemaTables = nil
		m.d1SchemaErr = ""
		m.d1SchemaLoading = true
	}

	ti := textinput.New()
	ti.Prompt = "sql> "
	ti.PromptStyle = theme.D1PromptStyle
	ti.TextStyle = theme.ValueStyle
	ti.PlaceholderStyle = theme.DimStyle
	ti.Placeholder = "SELECT * FROM ..."
	ti.CharLimit = 0
	m.d1Input = ti
	return m.d1Input.Focus()
}

// D1DatabaseID returns the current D1 database UUID.
func (m Model) D1DatabaseID() string {
	return m.d1DatabaseID
}

// D1Active returns whether the D1 console is active.
func (m Model) D1Active() bool {
	return m.d1Active
}

// SetD1Schema sets the schema data for the D1 detail view.
func (m *Model) SetD1Schema(tables []service.SchemaTable, err error) {
	m.d1SchemaLoading = false
	if err != nil {
		m.d1SchemaErr = err.Error()
		m.d1SchemaTables = nil
	} else {
		m.d1SchemaErr = ""
		m.d1SchemaTables = tables
	}
}

// SetD1SchemaLoading marks the schema as loading (for auto-refresh after mutation).
func (m *Model) SetD1SchemaLoading() {
	m.d1SchemaLoading = true
}

// SetD1QueryResult appends query results to the output area.
func (m *Model) SetD1QueryResult(result *service.D1QueryResult, err error) {
	m.d1Querying = false
	if err != nil {
		m.d1Output = append(m.d1Output, theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", err)))
		m.d1Output = append(m.d1Output, "")
		return
	}
	// Append the output lines
	outputLines := strings.Split(result.Output, "\n")
	m.d1Output = append(m.d1Output, outputLines...)
	if result.Meta != "" {
		m.d1Output = append(m.d1Output, theme.D1MetaStyle.Render(result.Meta))
	}
	m.d1Output = append(m.d1Output, "") // blank separator between queries
}

// ClearD1 resets all D1 console state (used on navigation away).
func (m *Model) ClearD1() {
	m.d1Active = false
	m.d1Output = nil
	m.d1Querying = false
	m.d1DatabaseID = ""
	m.d1SchemaTables = nil
	m.d1SchemaErr = ""
	m.d1SchemaLoading = false
	m.d1Input.Blur()
}

// updateD1 handles key events when the D1 SQL console is active.
func (m Model) updateD1(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Exit interactive mode, switch focus to list pane
		m.interacting = false
		m.focus = FocusList
		return m, nil
	case tea.KeyEnter:
		// Submit the SQL query
		sql := strings.TrimSpace(m.d1Input.Value())
		if sql == "" || m.d1Querying {
			return m, nil
		}
		m.d1Querying = true
		m.d1Output = append(m.d1Output, theme.D1PromptStyle.Render("sql> ")+theme.ValueStyle.Render(sql))
		m.d1Input.Reset()
		dbID := m.d1DatabaseID
		return m, func() tea.Msg {
			return D1QueryMsg{DatabaseID: dbID, SQL: sql}
		}
	}

	// Forward all other keys to the textinput
	var cmd tea.Cmd
	m.d1Input, cmd = m.d1Input.Update(msg)
	return m, cmd
}

// viewResourceDetailD1 renders the right pane for D1 with the SQL console split.
func (m Model) viewResourceDetailD1(width, height int, title, sep string, topFieldLines []string, copyLineMap map[int]string) []string {
	// Compact metadata at top
	topLines := []string{title, sep}
	topLines = append(topLines, m.renderD1CompactFields(copyLineMap)...)

	metaHeight := len(topLines)

	// Separator between metadata and the split pane
	panesSepWidth := width - 3
	if panesSepWidth < 0 {
		panesSepWidth = 0
	}
	panesSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", panesSepWidth))
	topLines = append(topLines, panesSep)
	metaHeight++

	// Bottom region: left/right split for SQL console and schema
	paneHeight := height - metaHeight
	if paneHeight < 5 {
		paneHeight = 5
	}

	halfWidth := width / 2
	leftWidth := halfWidth
	rightWidth := width - halfWidth - 1 // -1 for divider

	leftPane := m.renderD1SQLConsole(leftWidth, paneHeight)
	rightPane := m.renderD1SchemaPane(rightWidth, paneHeight)

	divider := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render("│")
	splitPane := joinSideBySide(leftPane, rightPane, divider, leftWidth, paneHeight)

	// Register copy targets for metadata lines
	m.registerCopyTargets(copyLineMap, 0, len(topLines))

	result := strings.Join(topLines, "\n") + "\n" + splitPane
	lines := strings.Split(result, "\n")

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// renderD1CompactFields renders metadata as 2 compact rows.
// copyLineMap is populated with the topLines index → text for copyable values.
// The compact rows start at topLines index 2 (after title=0, sep=1).
func (m Model) renderD1CompactFields(copyLineMap map[int]string) []string {
	if m.detail == nil {
		return nil
	}

	fields := m.detail.Fields
	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Label] = f.Value
	}

	// Row 1: Database ID, Name, Version
	row1Parts := []string{}
	if v, ok := fieldMap["Database ID"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s%s",
			theme.LabelStyle.Render("ID"), theme.ValueStyle.Render(v), copyIcon()))
	}
	if v, ok := fieldMap["Name"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Name"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["Version"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Ver"), theme.ValueStyle.Render(v)))
	}

	// Row 2: Created, File Size, Tables, Replication
	row2Parts := []string{}
	if v, ok := fieldMap["Created"]; ok {
		// Show just the date part
		if len(v) > 10 {
			v = v[:10]
		}
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Created"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["File Size"]; ok {
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Size"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["Tables"]; ok {
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Tables"), theme.ValueStyle.Render(v)))
	}
	if v, ok := fieldMap["Replication"]; ok {
		row2Parts = append(row2Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Repl"), theme.ValueStyle.Render(v)))
	}

	var rows []string
	if len(row1Parts) > 0 {
		rows = append(rows, "  "+strings.Join(row1Parts, "   "))
		// Row 1 is at topLines index 2 (title=0, sep=1) — copy the Database ID
		if v, ok := fieldMap["Database ID"]; ok {
			copyLineMap[2] = v
		}
	}
	if len(row2Parts) > 0 {
		rows = append(rows, "  "+strings.Join(row2Parts, "   "))
	}
	return rows
}

// renderD1SQLConsole renders the SQL console left pane as a list of lines.
func (m Model) renderD1SQLConsole(width, height int) []string {
	header := theme.D1SchemaTitleStyle.Render("SQL Console")

	// Help at the bottom
	help := theme.DimStyle.Render("esc back | enter query")

	// Input line
	inputLine := m.d1Input.View()
	if m.d1Querying {
		inputLine = fmt.Sprintf("%s %s", m.spinner.View(), theme.DimStyle.Render("Running..."))
	}

	// Available lines for output (minus header, input, help)
	outputHeight := height - 3
	if outputHeight < 1 {
		outputHeight = 1
	}

	// Build output lines, wrapped/truncated to width
	var outputLines []string
	for _, line := range m.d1Output {
		// Truncate long lines to fit the pane width
		if utf8.RuneCountInString(line) > width-1 {
			runes := []rune(line)
			line = string(runes[:width-2]) + "…"
		}
		outputLines = append(outputLines, line)
	}

	// Show most recent output that fits (scroll to bottom)
	if len(outputLines) > outputHeight {
		outputLines = outputLines[len(outputLines)-outputHeight:]
	}

	// Build the pane
	lines := []string{header}

	// Pad output to fill available space
	for len(outputLines) < outputHeight {
		outputLines = append([]string{""}, outputLines...)
	}
	lines = append(lines, outputLines...)
	lines = append(lines, inputLine)
	lines = append(lines, help)

	// Ensure exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return lines
}

// renderD1SchemaPane renders the schema diagram right pane with syntax coloring.
func (m Model) renderD1SchemaPane(width, height int) []string {
	header := theme.D1SchemaTitleStyle.Render("Schema")

	lines := []string{header}

	if m.d1SchemaLoading {
		lines = append(lines, fmt.Sprintf("%s %s", m.spinner.View(), theme.DimStyle.Render("Loading schema...")))
	} else if m.d1SchemaErr != "" {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", m.d1SchemaErr)))
	} else if len(m.d1SchemaTables) == 0 {
		lines = append(lines, theme.DimStyle.Render("No tables found"))
	} else {
		lines = append(lines, m.renderSchemaStyled(m.d1SchemaTables)...)
	}

	// Pad to exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return lines
}

// renderSchemaStyled renders structured schema data with per-element syntax coloring.
// Returns a slice of styled lines ready for display.
func (m Model) renderSchemaStyled(tables []service.SchemaTable) []string {
	var lines []string

	// Build a global FK list for the relations summary at the bottom
	var allFKs []string

	for ti, t := range tables {
		// Table name header
		lines = append(lines, theme.D1SchemaTableNameStyle.Render(t.Name))

		// Build FK lookup for this table
		fkMap := make(map[string]string)
		for _, fk := range t.FKs {
			ref := fmt.Sprintf("-> %s.%s", fk.ToTable, fk.ToCol)
			fkMap[fk.FromCol] = ref
			allFKs = append(allFKs, fmt.Sprintf("%s.%s -> %s.%s", t.Name, fk.FromCol, fk.ToTable, fk.ToCol))
		}

		// Calculate max column name width for alignment
		maxNameLen := 0
		for _, c := range t.Columns {
			if len(c.Name) > maxNameLen {
				maxNameLen = len(c.Name)
			}
		}
		if maxNameLen < 4 {
			maxNameLen = 4
		}

		for i, c := range t.Columns {
			// Branch character
			branchChar := "├─"
			if i == len(t.Columns)-1 {
				branchChar = "└─"
			}
			branch := theme.D1SchemaBranchStyle.Render(branchChar)

			// Tag: PK, FK, or blank
			var tag string
			if c.PK {
				tag = theme.D1SchemaPKTagStyle.Render("PK")
			} else if _, isFK := fkMap[c.Name]; isFK {
				tag = theme.D1SchemaFKTagStyle.Render("FK")
			} else {
				tag = "  "
			}

			// Column name (padded for alignment)
			paddedName := fmt.Sprintf("%-*s", maxNameLen, c.Name)
			colName := theme.D1SchemaColNameStyle.Render(paddedName)

			// Column type
			colType := c.Type
			if colType == "" {
				colType = "ANY"
			}
			colTypeStyled := theme.D1SchemaColTypeStyle.Render(fmt.Sprintf("%-8s", colType))

			// NOT NULL constraint (skip for PKs since they're implicitly NOT NULL)
			notNull := ""
			if c.NotNull && !c.PK {
				notNull = " " + theme.D1SchemaNotNullStyle.Render("NOT NULL")
			}

			// FK reference
			fkRef := ""
			if ref, ok := fkMap[c.Name]; ok {
				fkRef = "  " + theme.D1SchemaFKRefStyle.Render(ref)
			}

			line := fmt.Sprintf("%s %s %s  %s%s%s", branch, tag, colName, colTypeStyled, notNull, fkRef)
			lines = append(lines, line)
		}

		// Blank line between tables (but not after the last one before relations)
		if ti < len(tables)-1 {
			lines = append(lines, "")
		}
	}

	// Relations summary
	if len(allFKs) > 0 {
		lines = append(lines, "")
		lines = append(lines, theme.D1SchemaTableNameStyle.Render("Relations"))
		for _, fk := range allFKs {
			lines = append(lines, theme.D1SchemaRelationStyle.Render("  "+fk))
		}
	}

	return lines
}
