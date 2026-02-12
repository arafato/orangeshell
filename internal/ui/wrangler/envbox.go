package wrangler

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// renderHyperlink wraps text in an OSC 8 terminal hyperlink with blue underline styling.
// Most modern terminals (iTerm2, Ghostty, WezTerm, Windows Terminal, Kitty) support this,
// making the text clickable — clicking opens the URL in the default browser.
func renderHyperlink(url, text string) string {
	styled := lipgloss.NewStyle().Foreground(theme.ColorBlue).Underline(true).Render(text)
	return fmt.Sprintf("\x1b]8;;%s\x07%s\x1b]8;;\x07", url, styled)
}

// EnvBox renders a single environment box in the Wrangler view.
// The inner cursor covers all navigable items: the worker name at position 0,
// followed by bindings at positions 1..N, then triggers, then env vars.
type EnvBox struct {
	EnvName    string             // "default" or named env
	WorkerName string             // resolved worker name
	CompatDate string             // resolved compat date
	Routes     []wcfg.RouteConfig // routes for this env
	Bindings   []wcfg.Binding     // bindings for this env
	Vars       map[string]string  // vars for this env (names only)
	Crons      []string           // cron triggers (top-level only, same across all envs)
	cursor     int                // inner cursor position (0=worker, 1..N=bindings, then triggers, then env vars)

	// Deployment info (fetched async from API)
	Deployment        *DeploymentDisplay // active deployment for this env
	Subdomain         string             // account's workers.dev subdomain
	DeploymentFetched bool               // true once API response received (distinguishes "not fetched" from "not deployed")
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
		Crons:      cfg.CronTriggers(), // top-level only, shared across all envs
		cursor:     0,
	}
}

// BindingCount returns the number of bindings in this environment.
func (b EnvBox) BindingCount() int {
	return len(b.Bindings)
}

// ItemCount returns the total number of navigable items (worker name + bindings + triggers + env vars).
func (b EnvBox) ItemCount() int {
	count := 0
	if b.WorkerName != "" {
		count++ // worker name at position 0
	}
	count += len(b.Bindings)
	count++ // triggers item (always present, even when no crons defined)
	count++ // env vars item (always present, even when no vars defined)
	return count
}

// workerOffset returns 1 if the worker name is present (shifts binding indices), 0 otherwise.
func (b EnvBox) workerOffset() int {
	if b.WorkerName != "" {
		return 1
	}
	return 0
}

// IsWorkerSelected returns true if the cursor is on the worker name item.
func (b EnvBox) IsWorkerSelected() bool {
	return b.WorkerName != "" && b.cursor == 0
}

// Cursor returns the current inner cursor position.
func (b EnvBox) Cursor() int {
	return b.cursor
}

// SetCursor sets the inner cursor position (clamped to valid range).
func (b *EnvBox) SetCursor(idx int) {
	max := b.ItemCount() - 1
	if idx < 0 {
		idx = 0
	}
	if idx > max {
		idx = max
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
	if b.cursor < b.ItemCount()-1 {
		b.cursor++
	}
}

// SelectedBinding returns the binding at the current cursor, or nil if the cursor
// is on the worker name, env vars item, or out of range.
func (b EnvBox) SelectedBinding() *wcfg.Binding {
	bindingIdx := b.cursor - b.workerOffset()
	if bindingIdx < 0 || bindingIdx >= len(b.Bindings) {
		return nil
	}
	return &b.Bindings[bindingIdx]
}

// triggersIndex returns the cursor index of the Triggers item.
// Triggers come right after bindings: workerOffset + len(Bindings).
func (b EnvBox) triggersIndex() int {
	return b.workerOffset() + len(b.Bindings)
}

// IsTriggersSelected returns true if the cursor is on the "Triggers" item.
func (b EnvBox) IsTriggersSelected() bool {
	return b.cursor == b.triggersIndex()
}

// IsEnvVarsSelected returns true if the cursor is on the "Environment Variables" item.
// The env vars item is always the last navigable item.
func (b EnvBox) IsEnvVarsSelected() bool {
	return b.cursor == b.ItemCount()-1
}

// View renders the environment box.
// width: available width for the box content.
// focused: whether this box is the outer-focused box.
// inside: whether the user is navigating inside this box (inner mode).
func (b EnvBox) View(width int, focused, inside bool) string {
	// Title line: env name
	envLabel := b.EnvName
	if envLabel == "default" {
		envLabel = "default"
	}
	title := theme.TitleStyle.Render(envLabel)

	// Worker name as a navigable item
	var workerLine string
	if b.WorkerName != "" {
		workerCursor := "  "
		workerStyle := theme.NormalItemStyle
		if inside && b.cursor == 0 {
			workerCursor = theme.SelectedItemStyle.Render("> ")
			workerStyle = theme.SelectedItemStyle
		}
		navArrow := " " + theme.ActionNavArrowStyle.Render("\u2192") // →
		workerLine = fmt.Sprintf("%s%s %s%s",
			workerCursor,
			theme.DimStyle.Render(fmt.Sprintf("%-10s", "Worker")),
			workerStyle.Render(b.WorkerName),
			navArrow)
	}

	// URL line (clickable hyperlink) — only shown when the worker is actually deployed
	var urlLine string
	if b.WorkerName != "" && b.Subdomain != "" && b.Deployment != nil && len(b.Deployment.Versions) > 0 {
		url := fmt.Sprintf("https://%s.%s.workers.dev", b.WorkerName, b.Subdomain)
		urlLine = fmt.Sprintf("  %s %s",
			theme.DimStyle.Render(fmt.Sprintf("%-10s", "URL")),
			renderHyperlink(url, url))
	}

	// Deployment line
	var deployLine string
	if b.Deployment != nil && len(b.Deployment.Versions) > 0 {
		var versionParts []string
		for _, v := range b.Deployment.Versions {
			if v.Percentage >= 100 {
				versionParts = append(versionParts, theme.ValueStyle.Render(fmt.Sprintf("v%s (100%%)", v.ShortID)))
			} else {
				versionParts = append(versionParts, theme.ValueStyle.Render(fmt.Sprintf("v%s@%.0f%%", v.ShortID, v.Percentage)))
			}
		}
		deployLine = fmt.Sprintf("  %s %s",
			theme.DimStyle.Render(fmt.Sprintf("%-10s", "Deploy")),
			strings.Join(versionParts, theme.DimStyle.Render(" / ")))
	} else if b.DeploymentFetched {
		// API responded but no deployment found for this account
		deployLine = fmt.Sprintf("  %s %s",
			theme.DimStyle.Render(fmt.Sprintf("%-10s", "Deploy")),
			theme.ErrorStyle.Render("Currently not deployed"))
	}

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
		offset := b.workerOffset()
		for i, bnd := range b.Bindings {
			cursor := "  "
			nameStyle := theme.NormalItemStyle
			if inside && (i+offset) == b.cursor {
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

	// Configuration section: Triggers + Environment Variables
	var configLines []string
	configLines = append(configLines, theme.LabelStyle.Render("Configuration"))

	// Triggers (navigable item)
	triggersIdx := b.triggersIndex()
	{
		cursor := "  "
		nameStyle := theme.NormalItemStyle
		if inside && triggersIdx == b.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			nameStyle = theme.SelectedItemStyle
		}

		cronCount := len(b.Crons)
		label := nameStyle.Render(fmt.Sprintf("Triggers (%d)", cronCount))
		navArrow := " " + theme.ActionNavArrowStyle.Render("\u2192") // →
		configLines = append(configLines, fmt.Sprintf("%s%s%s", cursor, label, navArrow))
	}

	// Environment Variables (navigable item — always last)
	envVarsIdx := b.ItemCount() - 1
	{
		cursor := "  "
		nameStyle := theme.NormalItemStyle
		if inside && envVarsIdx == b.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
			nameStyle = theme.SelectedItemStyle
		}

		varCount := len(b.Vars)
		label := nameStyle.Render(fmt.Sprintf("Environment Variables (%d)", varCount))
		navArrow := " " + theme.ActionNavArrowStyle.Render("\u2192") // →
		configLines = append(configLines, fmt.Sprintf("%s%s%s", cursor, label, navArrow))
	}

	// Assemble box content
	var contentParts []string
	contentParts = append(contentParts, title)
	if metaLine != "" {
		contentParts = append(contentParts, metaLine)
	}
	if workerLine != "" {
		contentParts = append(contentParts, workerLine)
	}
	if urlLine != "" {
		contentParts = append(contentParts, urlLine)
	}
	if deployLine != "" {
		contentParts = append(contentParts, deployLine)
	}
	if len(routeLines) > 0 {
		contentParts = append(contentParts, strings.Join(routeLines, "\n"))
	}
	if len(bindingLines) > 0 {
		contentParts = append(contentParts, strings.Join(bindingLines, "\n"))
	}
	if len(configLines) > 0 {
		contentParts = append(contentParts, strings.Join(configLines, "\n"))
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
