package detail

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// isCopyableLabel returns true if a detail field label should get a copy icon.
func isCopyableLabel(label string) bool {
	switch label {
	case "Database ID", "Namespace ID", "Name", "Title", "Bucket Name",
		"Index Name", "Config ID", "Store ID", "Queue ID":
		return true
	}
	return false
}

// copyIcon returns the styled copy icon appended to copyable values.
func copyIcon() string {
	return " " + theme.CopyIconStyle.Render("⧉")
}

// isDeletableService returns true for services that support resource deletion from the list view.
func isDeletableService(name string) bool {
	switch name {
	case "KV", "D1", "R2", "Queues", "Vectorize", "Hyperdrive":
		return true
	}
	return false
}

// registerCopyTargets maps allLines indices (within the visible range) to
// absolute Y screen coordinates in the copyTargets map.
// copyLineMap: allLines index → raw text to copy.
// visStart/visEnd: the range of allLines indices currently visible on screen.
func (m Model) registerCopyTargets(copyLineMap map[int]string, visStart, visEnd int) {
	for idx, text := range copyLineMap {
		if idx >= visStart && idx < visEnd {
			screenY := idx - visStart // relative to content area top
			m.copyTargets[screenY] = text
		}
	}
}

// joinSideBySide joins two panes (as line arrays) side by side with a divider.
// leftWidth is used to pad left lines to a fixed column so the divider aligns.
func joinSideBySide(left, right []string, divider string, leftWidth, height int) string {
	var result []string
	for i := 0; i < height; i++ {
		l := ""
		if i < len(left) {
			l = left[i]
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		// Pad left to fixed width using rune count for ANSI-safe padding
		visLen := runeWidth(l)
		if visLen < leftWidth {
			l = l + strings.Repeat(" ", leftWidth-visLen)
		}
		result = append(result, l+divider+r)
	}
	return strings.Join(result, "\n")
}

// runeWidth returns the visible rune count of a string (approximate — doesn't strip ANSI).
// For our use case, lipgloss-styled strings have ANSI sequences, so we use lipgloss.Width.
func runeWidth(s string) int {
	return lipgloss.Width(s)
}

// truncateRunes truncates a string to maxLen runes, appending "..." if needed.
func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	// Collapse newlines to spaces (for multi-line commit messages)
	s = strings.ReplaceAll(s, "\n", " ")
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxLen {
		return s
	}
	if maxLen <= 3 {
		runes := []rune(s)
		return string(runes[:maxLen])
	}
	runes := []rune(s)
	return string(runes[:maxLen-3]) + "..."
}

// padRight pads a plain string to exactly width runes using spaces.
// Use this BEFORE applying ANSI styles to ensure correct column alignment.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}
