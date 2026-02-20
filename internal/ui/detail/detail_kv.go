package detail

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- KV Data Explorer helpers ---

// InitKVExplorer initializes the KV data explorer for a namespace.
func (m *Model) InitKVExplorer(namespaceID string) tea.Cmd {
	m.kvActive = true
	m.kvNamespaceID = namespaceID
	m.kvKeys = nil
	m.kvLoading = true
	m.kvCursor = 0
	m.kvScroll = 0
	m.kvErr = ""

	ti := textinput.New()
	ti.Prompt = "prefix> "
	ti.PromptStyle = theme.KVPromptStyle
	ti.TextStyle = theme.ValueStyle
	ti.PlaceholderStyle = theme.DimStyle
	ti.Placeholder = "filter by prefix..."
	ti.CharLimit = 0
	m.kvInput = ti
	return m.kvInput.Focus()
}

// KVActive returns whether the KV explorer is active.
func (m Model) KVActive() bool {
	return m.kvActive
}

// KVNamespaceID returns the current KV namespace UUID.
func (m Model) KVNamespaceID() string {
	return m.kvNamespaceID
}

// SetKVKeys sets the loaded KV key entries.
func (m *Model) SetKVKeys(keys []service.KVKeyEntry, err error) {
	m.kvLoading = false
	if err != nil {
		m.kvErr = err.Error()
		m.kvKeys = nil
	} else {
		m.kvErr = ""
		// Use non-nil empty slice so we can distinguish "loaded but empty"
		// from "never loaded" (nil).
		if keys == nil {
			keys = []service.KVKeyEntry{}
		}
		m.kvKeys = keys
	}
	m.kvCursor = 0
	m.kvScroll = 0
}

// ClearKV resets all KV explorer state (used on navigation away).
func (m *Model) ClearKV() {
	m.kvActive = false
	m.kvKeys = nil
	m.kvLoading = false
	m.kvNamespaceID = ""
	m.kvCursor = 0
	m.kvScroll = 0
	m.kvErr = ""
	m.kvInput.Blur()
}

// updateKV handles key events when the KV data explorer is active.
func (m Model) updateKV(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Exit interactive mode, switch focus to list pane
		m.interacting = false
		m.focus = FocusList
		return m, nil

	case tea.KeyEnter:
		// Submit the prefix search
		if m.kvLoading {
			return m, nil
		}
		prefix := strings.TrimSpace(m.kvInput.Value())
		m.kvLoading = true
		m.kvErr = ""
		nsID := m.kvNamespaceID
		return m, func() tea.Msg {
			return KVKeysLoadMsg{NamespaceID: nsID, Prefix: prefix}
		}

	case tea.KeyUp:
		if m.kvCursor > 0 {
			m.kvCursor--
			// Scroll up if cursor goes above visible area
			if m.kvCursor < m.kvScroll {
				m.kvScroll = m.kvCursor
			}
		}
		return m, nil

	case tea.KeyDown:
		if m.kvCursor < len(m.kvKeys)-1 {
			m.kvCursor++
		}
		return m, nil

	case tea.KeyTab:
		// Tab to switch focus between search input and results table
		// (not needed — tab is handled at parent level for pane switching)
		return m, nil
	}

	// Check for specific key strings
	switch msg.String() {
	case "ctrl+y":
		// Copy selected key's value to clipboard
		if len(m.kvKeys) > 0 && m.kvCursor < len(m.kvKeys) && !m.kvLoading {
			entry := m.kvKeys[m.kvCursor]
			if !entry.IsBinary {
				return m, func() tea.Msg {
					return CopyToClipboardMsg{Text: entry.Value}
				}
			}
		}
		return m, nil
	case "k":
		if m.kvInput.Focused() {
			break // let textinput handle it
		}
		if m.kvCursor > 0 {
			m.kvCursor--
			if m.kvCursor < m.kvScroll {
				m.kvScroll = m.kvCursor
			}
		}
		return m, nil
	case "j":
		if m.kvInput.Focused() {
			break // let textinput handle it
		}
		if m.kvCursor < len(m.kvKeys)-1 {
			m.kvCursor++
		}
		return m, nil
	}

	// Forward all other keys to the textinput
	var cmd tea.Cmd
	m.kvInput, cmd = m.kvInput.Update(msg)
	return m, cmd
}

// viewResourceDetailKV renders the right pane for KV with the data explorer split.
func (m Model) viewResourceDetailKV(width, height int, title, sep string, topFieldLines []string, copyLineMap map[int]string) []string {
	// Compact metadata at top
	topLines := []string{title, sep}
	topLines = append(topLines, m.renderKVCompactFields(copyLineMap)...)

	metaHeight := len(topLines)

	// Separator between metadata and the explorer pane
	panesSepWidth := width - 3
	if panesSepWidth < 0 {
		panesSepWidth = 0
	}
	panesSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", panesSepWidth))
	topLines = append(topLines, panesSep)
	metaHeight++

	// Explorer region
	paneHeight := height - metaHeight
	if paneHeight < 5 {
		paneHeight = 5
	}

	explorerPane := m.renderKVExplorer(width-2, paneHeight)

	// Register copy targets for metadata lines
	m.registerCopyTargets(copyLineMap, 0, len(topLines))

	result := strings.Join(topLines, "\n") + "\n" + strings.Join(explorerPane, "\n")
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

// renderKVCompactFields renders metadata as compact rows (similar to D1).
func (m Model) renderKVCompactFields(copyLineMap map[int]string) []string {
	if m.detail == nil {
		return nil
	}

	fields := m.detail.Fields
	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Label] = f.Value
	}

	// Row 1: Namespace ID, Title
	row1Parts := []string{}
	if v, ok := fieldMap["Namespace ID"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s%s",
			theme.LabelStyle.Render("ID"), theme.ValueStyle.Render(v), copyIcon()))
	}
	if v, ok := fieldMap["Title"]; ok {
		row1Parts = append(row1Parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Title"), theme.ValueStyle.Render(v)))
	}

	var rows []string
	if len(row1Parts) > 0 {
		rows = append(rows, "  "+strings.Join(row1Parts, "   "))
		// Row 1 is at topLines index 2 (title=0, sep=1) — copy the Namespace ID
		if v, ok := fieldMap["Namespace ID"]; ok {
			copyLineMap[2] = v
		}
	}
	return rows
}

// renderKVExplorer renders the KV data explorer pane.
func (m Model) renderKVExplorer(width, height int) []string {
	header := theme.KVHeaderStyle.Render("Data Explorer")

	// Help text
	help := theme.DimStyle.Render("esc back | enter search | ctrl+y copy")

	// Input line
	inputLine := m.kvInput.View()
	if m.kvLoading {
		inputLine = fmt.Sprintf("%s %s", m.spinner.View(), theme.DimStyle.Render("Loading keys..."))
	}

	// Available lines for the key table (minus header, input, help, status)
	tableHeight := height - 4
	if tableHeight < 1 {
		tableHeight = 1
	}

	// Build the pane
	lines := []string{header, inputLine}

	if m.kvErr != "" {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", m.kvErr)))
	} else if !m.kvLoading && m.kvKeys == nil {
		// Initial state — hasn't loaded yet
		lines = append(lines, theme.DimStyle.Render("Loading initial keys..."))
	} else if !m.kvLoading && len(m.kvKeys) == 0 {
		lines = append(lines, theme.DimStyle.Render("No keys found"))
	} else if len(m.kvKeys) > 0 {
		// Render key-value table
		tableLines := m.renderKVTable(width, tableHeight)
		lines = append(lines, tableLines...)
	}

	lines = append(lines, help)

	// Ensure exact height
	for len(lines) < height {
		// Insert empty lines before help
		lines = append(lines[:len(lines)-1], append([]string{""}, lines[len(lines)-1:]...)...)
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return lines
}

// renderKVTable renders the key-value pairs as a compact table.
func (m Model) renderKVTable(width, maxRows int) []string {
	if len(m.kvKeys) == 0 {
		return nil
	}

	// Calculate column widths: key (30%), value (remaining), expiry (if any, fixed 12 chars)
	hasExpiry := false
	for _, k := range m.kvKeys {
		if !k.Expiration.IsZero() {
			hasExpiry = true
			break
		}
	}

	expiryWidth := 0
	if hasExpiry {
		expiryWidth = 14
	}

	keyWidth := width / 3
	if keyWidth < 10 {
		keyWidth = 10
	}
	valueWidth := width - keyWidth - expiryWidth - 5 // 5 for separators and cursor
	if valueWidth < 10 {
		valueWidth = 10
	}

	// Column header
	headerLine := fmt.Sprintf("  %-*s  %-*s",
		keyWidth, theme.LabelStyle.Render("Key"),
		valueWidth, theme.LabelStyle.Render("Value"))
	if hasExpiry {
		headerLine += fmt.Sprintf("  %s", theme.LabelStyle.Render("Expires"))
	}

	var lines []string
	lines = append(lines, headerLine)
	lines = append(lines, lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", width-1)))

	// Scroll: ensure cursor is visible
	if m.kvCursor >= m.kvScroll+maxRows-2 { // -2 for header + separator
		m.kvScroll = m.kvCursor - (maxRows - 3)
	}
	if m.kvScroll < 0 {
		m.kvScroll = 0
	}

	dataRows := maxRows - 2 // subtract header + separator line
	if dataRows < 1 {
		dataRows = 1
	}

	startIdx := m.kvScroll
	endIdx := startIdx + dataRows
	if endIdx > len(m.kvKeys) {
		endIdx = len(m.kvKeys)
	}

	showCursor := m.focus == FocusDetail

	for i := startIdx; i < endIdx; i++ {
		entry := m.kvKeys[i]

		// Cursor indicator
		cursor := "  "
		keyStyle := theme.KVKeyStyle
		valStyle := theme.KVValueStyle
		if showCursor && i == m.kvCursor {
			cursor = theme.KVSelectedRowStyle.Render("> ")
			keyStyle = theme.KVSelectedRowStyle
			valStyle = theme.KVSelectedRowStyle
		}

		// Truncate key and value to fit
		keyStr := truncateRunesStr(entry.Name, keyWidth)
		valStr := entry.Value
		if entry.IsBinary {
			valStr = theme.KVBinaryStyle.Render(entry.Value)
		} else {
			// Collapse newlines for display
			valStr = strings.ReplaceAll(valStr, "\n", "\\n")
			valStr = truncateRunesStr(valStr, valueWidth)
			valStr = valStyle.Render(valStr)
		}

		line := fmt.Sprintf("%s%-*s  %s",
			cursor,
			keyWidth, keyStyle.Render(keyStr),
			valStr)

		if hasExpiry {
			if !entry.Expiration.IsZero() {
				line += fmt.Sprintf("  %s", theme.KVExpiryStyle.Render(relativeTime(entry.Expiration)))
			} else {
				line += fmt.Sprintf("  %s", theme.DimStyle.Render("never"))
			}
		}

		lines = append(lines, line)
	}

	// Status line
	statusParts := []string{fmt.Sprintf("%d keys", len(m.kvKeys))}
	if len(m.kvKeys) >= 20 {
		statusParts = append(statusParts, "showing first 20")
	}
	lines = append(lines, theme.DimStyle.Render(strings.Join(statusParts, " · ")))

	return lines
}

// truncateRunesStr truncates a string to maxWidth visible runes, adding "…" if truncated.
func truncateRunesStr(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	if maxWidth <= 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

// relativeTime formats a time as a relative string (e.g. "in 2h", "in 3d").
func relativeTime(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "expired"
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("in %dmo", int(d.Hours()/(24*30)))
	}
}
