package wrangler

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// EnvBox renders a single environment box in the Wrangler view.
type EnvBox struct {
	EnvName    string             // "default" or named env
	WorkerName string             // resolved worker name
	CompatDate string             // resolved compat date
	Routes     []wcfg.RouteConfig // routes for this env
	Bindings   []wcfg.Binding     // bindings for this env
	Vars       map[string]string  // vars for this env (names only)
	cursor     int                // inner cursor position (over bindings)
}

// NewEnvBox creates an EnvBox from a wrangler config and environment name.
func NewEnvBox(cfg *wcfg.WranglerConfig, envName string) EnvBox {
	return EnvBox{
		EnvName:    envName,
		WorkerName: cfg.ResolvedEnvName(envName),
		CompatDate: cfg.ResolvedCompatDate(envName),
		Routes:     cfg.EnvRoutes(envName),
		Bindings:   cfg.EnvBindings(envName),
		Vars:       cfg.EnvVars(envName),
		cursor:     0,
	}
}

// BindingCount returns the number of bindings in this environment.
func (b EnvBox) BindingCount() int {
	return len(b.Bindings)
}

// Cursor returns the current inner cursor position.
func (b EnvBox) Cursor() int {
	return b.cursor
}

// SetCursor sets the inner cursor position (clamped to valid range).
func (b *EnvBox) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(b.Bindings) {
		idx = len(b.Bindings) - 1
	}
	if idx < 0 {
		idx = 0
	}
	b.cursor = idx
}

// CursorUp moves the inner cursor up.
func (b *EnvBox) CursorUp() {
	if b.cursor > 0 {
		b.cursor--
	}
}

// CursorDown moves the inner cursor down.
func (b *EnvBox) CursorDown() {
	if b.cursor < len(b.Bindings)-1 {
		b.cursor++
	}
}

// SelectedBinding returns the binding at the current cursor, or nil if none.
func (b EnvBox) SelectedBinding() *wcfg.Binding {
	if len(b.Bindings) == 0 {
		return nil
	}
	if b.cursor >= 0 && b.cursor < len(b.Bindings) {
		return &b.Bindings[b.cursor]
	}
	return nil
}

// View renders the environment box.
// width: available width for the box content.
// focused: whether this box is the outer-focused box.
// inside: whether the user is navigating inside this box (inner mode).
func (b EnvBox) View(width int, focused, inside bool) string {
	// Title line: env name + worker name
	envLabel := b.EnvName
	if envLabel == "default" {
		envLabel = "default"
	}
	titleParts := []string{
		theme.TitleStyle.Render(envLabel),
	}
	if b.WorkerName != "" {
		titleParts = append(titleParts, theme.ValueStyle.Render(b.WorkerName))
	}
	title := strings.Join(titleParts, "  ")

	// Compat date
	var metaLine string
	if b.CompatDate != "" {
		metaLine = theme.DimStyle.Render(fmt.Sprintf("compat: %s", b.CompatDate))
	}

	// Routes
	var routeLines []string
	if len(b.Routes) > 0 {
		routeLines = append(routeLines, theme.LabelStyle.Render("Routes"))
		for _, r := range b.Routes {
			line := fmt.Sprintf("  %s", theme.ValueStyle.Render(r.Pattern))
			if r.ZoneName != "" {
				line += theme.DimStyle.Render(fmt.Sprintf(" (%s)", r.ZoneName))
			}
			routeLines = append(routeLines, line)
		}
	}

	// Bindings
	var bindingLines []string
	if len(b.Bindings) > 0 {
		bindingLines = append(bindingLines, theme.LabelStyle.Render("Bindings"))
		for i, bnd := range b.Bindings {
			cursor := "  "
			nameStyle := theme.NormalItemStyle
			if inside && i == b.cursor {
				cursor = theme.SelectedItemStyle.Render("> ")
				nameStyle = theme.SelectedItemStyle
			}

			typeLabel := theme.DimStyle.Render(fmt.Sprintf("%-10s", bnd.TypeLabel()))
			name := nameStyle.Render(bnd.Name)

			// Resource ID
			resID := ""
			if bnd.ResourceID != "" {
				resID = theme.DimStyle.Render(fmt.Sprintf(" (%s)", bnd.ResourceID))
			}

			// Navigation arrow for navigable bindings
			navArrow := ""
			if bnd.NavService() != "" {
				navArrow = " " + theme.ActionNavArrowStyle.Render("→")
			}

			line := fmt.Sprintf("%s%s %s%s%s", cursor, typeLabel, name, resID, navArrow)
			bindingLines = append(bindingLines, line)
		}
	}

	// Vars (names only)
	var varLines []string
	if len(b.Vars) > 0 {
		varLines = append(varLines, theme.LabelStyle.Render("Vars"))
		names := sortedKeys(b.Vars)
		line := "  " + theme.DimStyle.Render(strings.Join(names, ", "))
		varLines = append(varLines, line)
	}

	// Assemble box content
	var contentParts []string
	contentParts = append(contentParts, title)
	if metaLine != "" {
		contentParts = append(contentParts, metaLine)
	}
	if len(routeLines) > 0 {
		contentParts = append(contentParts, strings.Join(routeLines, "\n"))
	}
	if len(bindingLines) > 0 {
		contentParts = append(contentParts, strings.Join(bindingLines, "\n"))
	}
	if len(varLines) > 0 {
		contentParts = append(contentParts, strings.Join(varLines, "\n"))
	}

	content := strings.Join(contentParts, "\n")

	// Box border style
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(width - 2) // subtract border chars

	if focused {
		boxStyle = boxStyle.BorderForeground(theme.ColorOrange)
	} else {
		boxStyle = boxStyle.BorderForeground(theme.ColorDarkGray)
	}

	return boxStyle.Render(content)
}

// sortedKeys returns map keys in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort (maps are small)
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
