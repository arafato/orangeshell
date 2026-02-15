// Package confirmbox provides a reusable styled confirmation box for delete
// operations and other destructive actions across the application.
//
// The box renders with a rounded border, title, separator, body text, and
// optional No/Yes buttons. It is a pure rendering helper — no state, no
// Update method. Callers handle their own key events and simply pass the
// current visual state (cursor position, etc.) when rendering.
package confirmbox

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// ButtonStyle controls how the confirm/cancel buttons are rendered.
type ButtonStyle int

const (
	// ButtonsYN renders lightweight [Y] Yes / [N] No indicators (key-driven).
	ButtonsYN ButtonStyle = iota
	// ButtonsCursor renders No / Yes with a highlight cursor (h/l + enter driven).
	ButtonsCursor
)

// Params configures the confirm box rendering.
type Params struct {
	// Title is the header text (rendered in bold orange).
	Title string
	// Body is one or more lines of descriptive text.
	Body []string
	// Width overrides the box width. If 0, a sensible default is computed.
	Width int
	// Buttons selects the button rendering style.
	Buttons ButtonStyle
	// Cursor is the currently highlighted button (0 = No, 1 = Yes).
	// Only used when Buttons == ButtonsCursor.
	Cursor int
	// HelpText is optional help rendered below the buttons (e.g. "esc close").
	// Empty string omits the help section entirely.
	HelpText string
}

// Render produces a styled confirmation box string.
func Render(p Params) string {
	width := p.Width
	if width == 0 {
		width = computeWidth(p)
	}

	titleLine := theme.TitleStyle.Render(p.Title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", width-4))

	var parts []string
	parts = append(parts, titleLine, sep)

	// Body
	for _, line := range p.Body {
		parts = append(parts, line)
	}
	parts = append(parts, "")

	// Buttons
	parts = append(parts, renderButtons(p))

	// Help
	if p.HelpText != "" {
		parts = append(parts, sep)
		parts = append(parts, theme.DimStyle.Render(p.HelpText))
	}

	content := strings.Join(parts, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(width).
		Render(content)
}

// renderButtons produces the button line based on the selected style.
func renderButtons(p Params) string {
	switch p.Buttons {
	case ButtonsCursor:
		noStyle := theme.DimStyle
		yesStyle := theme.DimStyle
		if p.Cursor == 0 {
			noStyle = lipgloss.NewStyle().
				Background(theme.ColorDarkGray).
				Foreground(theme.ColorWhite).
				Padding(0, 2)
		} else {
			yesStyle = lipgloss.NewStyle().
				Background(theme.ColorRed).
				Foreground(theme.ColorWhite).
				Padding(0, 2)
		}
		return fmt.Sprintf("  %s   %s", noStyle.Render("No"), yesStyle.Render("Yes"))

	default: // ButtonsYN
		yesBtn := lipgloss.NewStyle().Foreground(theme.ColorGreen).Bold(true).Render("[Y] Yes")
		noBtn := lipgloss.NewStyle().Foreground(theme.ColorGray).Render("[N] No")
		return fmt.Sprintf("  %s       %s", yesBtn, noBtn)
	}
}

// computeWidth picks a width that fits the content comfortably.
func computeWidth(p Params) int {
	w := 50
	if tw := lipgloss.Width(p.Title) + 8; tw > w {
		w = tw
	}
	for _, line := range p.Body {
		if lw := lipgloss.Width(line) + 8; lw > w {
			w = lw
		}
	}
	if w > 70 {
		w = 70
	}
	return w
}
