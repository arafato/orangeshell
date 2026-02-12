package wrangler

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// DeploymentDisplay holds deployment data for rendering in a project box.
type DeploymentDisplay struct {
	Versions []VersionSplit
	URL      string // full workers.dev URL
}

// VersionSplit holds a single version's short ID and percentage.
type VersionSplit struct {
	ShortID    string // first 8 chars of version ID
	Percentage float64
}

// ProjectBox renders a single project in the monorepo list view.
type ProjectBox struct {
	Name              string                        // directory basename
	RelPath           string                        // relative path from CWD
	Config            *wcfg.WranglerConfig          // parsed config (nil on error)
	Err               error                         // parse error
	Deployments       map[string]*DeploymentDisplay // envName -> deployment info
	DeploymentFetched map[string]bool               // envName -> true once API responded
	Subdomain         string                        // account's workers.dev subdomain
}

// View renders the project box.
// width: available width for the box content.
// focused: whether this box is the currently selected project.
func (b ProjectBox) View(width int, focused bool) string {
	// Title: project name
	title := theme.TitleStyle.Render(b.Name)

	// Subtitle: relative path
	pathLine := theme.DimStyle.Render(b.RelPath)

	// Error state
	if b.Err != nil {
		errLine := theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", b.Err.Error()))
		content := fmt.Sprintf("%s\n%s\n\n%s", title, pathLine, errLine)
		return b.renderBorder(content, width, focused)
	}

	// No config parsed
	if b.Config == nil {
		content := fmt.Sprintf("%s\n%s", title, pathLine)
		return b.renderBorder(content, width, focused)
	}

	// Build per-environment sections
	envNames := b.Config.EnvNames()
	var envSections []string
	for _, envName := range envNames {
		section := b.renderEnvSection(envName)
		if section != "" {
			envSections = append(envSections, section)
		}
	}

	// Assemble
	var parts []string
	parts = append(parts, title)
	parts = append(parts, pathLine)
	if len(envSections) > 0 {
		parts = append(parts, "") // spacer
		parts = append(parts, strings.Join(envSections, "\n\n"))
	}

	content := strings.Join(parts, "\n")
	return b.renderBorder(content, width, focused)
}

// renderEnvSection renders a single environment's info block.
func (b ProjectBox) renderEnvSection(envName string) string {
	workerName := b.Config.ResolvedEnvName(envName)
	if workerName == "" {
		return ""
	}

	// Environment label
	envLabel := theme.LabelStyle.Render(envName)

	// Worker line with nav arrow
	workerLine := fmt.Sprintf("  %s  %s %s",
		theme.DimStyle.Render(fmt.Sprintf("%-9s", "Worker")),
		theme.ValueStyle.Render(workerName),
		theme.ActionNavArrowStyle.Render("\u2192"))

	// URL line (clickable hyperlink) — only shown when the worker is actually deployed
	var urlLine string
	dep := b.Deployments[envName]
	if b.Subdomain != "" && dep != nil && len(dep.Versions) > 0 {
		url := fmt.Sprintf("https://%s.%s.workers.dev", workerName, b.Subdomain)
		urlLine = fmt.Sprintf("  %s  %s",
			theme.DimStyle.Render(fmt.Sprintf("%-9s", "URL")),
			renderHyperlink(url, url))
	}

	// Deployment line
	var deployLine string
	if dep, ok := b.Deployments[envName]; ok && dep != nil && len(dep.Versions) > 0 {
		var versionParts []string
		for _, v := range dep.Versions {
			if v.Percentage >= 100 {
				versionParts = append(versionParts, theme.ValueStyle.Render(fmt.Sprintf("v%s (100%%)", v.ShortID)))
			} else {
				versionParts = append(versionParts, theme.ValueStyle.Render(fmt.Sprintf("v%s@%.0f%%", v.ShortID, v.Percentage)))
			}
		}
		deployLine = fmt.Sprintf("  %s  %s",
			theme.DimStyle.Render(fmt.Sprintf("%-9s", "Deploy")),
			strings.Join(versionParts, theme.DimStyle.Render(" / ")))
	} else if b.DeploymentFetched[envName] {
		// API responded but no deployment found for this account
		deployLine = fmt.Sprintf("  %s  %s",
			theme.DimStyle.Render(fmt.Sprintf("%-9s", "Deploy")),
			theme.ErrorStyle.Render("Currently not deployed"))
	}

	// Bindings summary (compact: "KV MY_CACHE · D1 MY_DB · R2 ASSETS")
	var bindingsLine string
	bindings := b.Config.EnvBindings(envName)
	if len(bindings) > 0 {
		var bindParts []string
		for _, bnd := range bindings {
			bindParts = append(bindParts, fmt.Sprintf("%s %s",
				theme.DimStyle.Render(bnd.TypeLabel()),
				theme.ValueStyle.Render(bnd.Name)))
		}
		summary := strings.Join(bindParts, theme.DimStyle.Render(" \u00b7 ")) // middle dot ·
		bindingsLine = fmt.Sprintf("  %s  %s",
			theme.DimStyle.Render(fmt.Sprintf("%-9s", "Bindings")),
			summary)
	}

	// Assemble section
	var lines []string
	lines = append(lines, envLabel)
	lines = append(lines, workerLine)
	if urlLine != "" {
		lines = append(lines, urlLine)
	}
	if deployLine != "" {
		lines = append(lines, deployLine)
	}
	if bindingsLine != "" {
		lines = append(lines, bindingsLine)
	}

	return strings.Join(lines, "\n")
}

// renderBorder wraps content in a rounded border.
func (b ProjectBox) renderBorder(content string, width int, focused bool) string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(width - 2)

	if focused {
		boxStyle = boxStyle.BorderForeground(theme.ColorOrange)
	} else {
		boxStyle = boxStyle.BorderForeground(theme.ColorDarkGray)
	}

	return boxStyle.Render(content)
}
