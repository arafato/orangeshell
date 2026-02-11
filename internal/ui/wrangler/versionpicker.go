package wrangler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// PickerMode defines the type of version picker operation.
type PickerMode int

const (
	// PickerModeDeploy selects a single version to deploy at 100%.
	PickerModeDeploy PickerMode = iota
	// PickerModeGradual selects two versions with a traffic split.
	PickerModeGradual
)

// pickerStep tracks the current step in the picker flow.
type pickerStep int

const (
	stepLoading    pickerStep = iota // Waiting for versions to load
	stepSelectA                      // Select first (or only) version
	stepSelectB                      // Select second version (gradual only)
	stepPercentage                   // Enter traffic percentage (gradual only)
)

// DeployVersionMsg is emitted when the user selects a single version to deploy at 100%.
type DeployVersionMsg struct {
	VersionID string
	EnvName   string
}

// GradualDeployMsg is emitted when the user configures a gradual deployment.
type GradualDeployMsg struct {
	VersionA    string
	VersionB    string
	PercentageA int // 0-100; PercentageB = 100 - PercentageA
	EnvName     string
}

// VersionPickerCloseMsg is emitted when the user presses Esc to close the picker.
type VersionPickerCloseMsg struct{}

// VersionPicker is an overlay component for selecting versions to deploy.
type VersionPicker struct {
	mode     PickerMode
	envName  string
	step     pickerStep
	versions []wcfg.Version

	cursor    int // cursor within version list
	selectedA int // index of first selected version (-1 = none)
	selectedB int // index of second selected version (-1 = none)

	pctInput textinput.Model // percentage text input for gradual mode
	pctErr   string          // validation error for percentage input

	width  int
	height int
}

// NewVersionPicker creates a new version picker in loading state.
func NewVersionPicker(mode PickerMode, envName string) VersionPicker {
	ti := textinput.New()
	ti.Placeholder = "50"
	ti.CharLimit = 3
	ti.Width = 5

	return VersionPicker{
		mode:      mode,
		envName:   envName,
		step:      stepLoading,
		selectedA: -1,
		selectedB: -1,
		pctInput:  ti,
	}
}

// SetVersions populates the picker with versions and transitions to selection.
func (p *VersionPicker) SetVersions(versions []wcfg.Version) {
	p.versions = versions
	p.step = stepSelectA
	p.cursor = 0
}

// IsLoading returns true if the picker is waiting for versions.
func (p VersionPicker) IsLoading() bool {
	return p.step == stepLoading
}

// Update handles key events for the version picker.
func (p VersionPicker) Update(msg tea.Msg) (VersionPicker, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch p.step {
		case stepSelectA, stepSelectB:
			return p.updateSelect(msg)
		case stepPercentage:
			return p.updatePercentage(msg)
		}
	}
	return p, nil
}

// updateSelect handles key events during version selection steps.
func (p VersionPicker) updateSelect(msg tea.KeyMsg) (VersionPicker, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// In stepSelectB, go back to stepSelectA
		if p.step == stepSelectB {
			p.step = stepSelectA
			p.selectedA = -1
			p.cursor = 0
			return p, nil
		}
		return p, func() tea.Msg { return VersionPickerCloseMsg{} }

	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
			// In stepSelectB, skip over the already-selected version A
			if p.step == stepSelectB && p.cursor == p.selectedA {
				if p.cursor > 0 {
					p.cursor--
				} else {
					p.cursor++ // can't go past, undo
				}
			}
		}

	case "down", "j":
		if p.cursor < len(p.versions)-1 {
			p.cursor++
			// In stepSelectB, skip over the already-selected version A
			if p.step == stepSelectB && p.cursor == p.selectedA {
				if p.cursor < len(p.versions)-1 {
					p.cursor++
				} else {
					p.cursor-- // can't go past, undo
				}
			}
		}

	case "enter":
		if len(p.versions) == 0 {
			return p, nil
		}

		if p.mode == PickerModeDeploy {
			// Single select — emit deploy message
			v := p.versions[p.cursor]
			return p, func() tea.Msg {
				return DeployVersionMsg{VersionID: v.ID, EnvName: p.envName}
			}
		}

		// Gradual mode
		if p.step == stepSelectA {
			p.selectedA = p.cursor
			p.step = stepSelectB
			// Move cursor to first non-selected version
			p.cursor = 0
			if p.cursor == p.selectedA {
				p.cursor = 1
			}
			return p, nil
		}

		if p.step == stepSelectB {
			p.selectedB = p.cursor
			p.step = stepPercentage
			p.pctInput.SetValue("50")
			p.pctErr = ""
			return p, p.pctInput.Focus()
		}
	}

	return p, nil
}

// updatePercentage handles key events during percentage input.
func (p VersionPicker) updatePercentage(msg tea.KeyMsg) (VersionPicker, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Go back to version B selection
		p.step = stepSelectB
		p.selectedB = -1
		p.cursor = 0
		if p.cursor == p.selectedA {
			p.cursor = 1
		}
		p.pctErr = ""
		return p, nil

	case "enter":
		// Validate and emit
		val := strings.TrimSpace(p.pctInput.Value())
		pct, err := strconv.Atoi(val)
		if err != nil || pct < 0 || pct > 100 {
			p.pctErr = "Enter a number 0-100"
			return p, nil
		}
		p.pctErr = ""
		vA := p.versions[p.selectedA]
		vB := p.versions[p.selectedB]
		return p, func() tea.Msg {
			return GradualDeployMsg{
				VersionA:    vA.ID,
				VersionB:    vB.ID,
				PercentageA: pct,
				EnvName:     p.envName,
			}
		}
	}

	// Forward to textinput
	var cmd tea.Cmd
	p.pctInput, cmd = p.pctInput.Update(msg)
	return p, cmd
}

// View renders the version picker overlay.
func (p VersionPicker) View(termWidth, termHeight int, spinnerView string) string {
	popupWidth := termWidth / 2
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 75 {
		popupWidth = 75
	}

	// Title based on mode and step
	var title string
	switch {
	case p.mode == PickerModeDeploy:
		title = "Deploy Version"
	case p.step == stepSelectA:
		title = "Gradual Deployment  (1/3) Select first version"
	case p.step == stepSelectB:
		title = "Gradual Deployment  (2/3) Select second version"
	case p.step == stepPercentage:
		title = "Gradual Deployment  (3/3) Set traffic split"
	default:
		title = "Gradual Deployment"
	}

	titleRendered := theme.TitleStyle.Render("  " + title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", popupWidth-4))

	var bodyLines []string

	switch p.step {
	case stepLoading:
		bodyLines = append(bodyLines, fmt.Sprintf("  %s Loading versions...", spinnerView))

	case stepSelectA, stepSelectB:
		bodyLines = p.renderVersionList(popupWidth)

	case stepPercentage:
		bodyLines = p.renderPercentageInput(popupWidth)
	}

	// Help line
	var help string
	switch p.step {
	case stepLoading:
		help = "  esc cancel"
	case stepSelectA:
		help = "  esc cancel  |  enter select  |  j/k navigate"
	case stepSelectB:
		help = "  esc back  |  enter select  |  j/k navigate"
	case stepPercentage:
		help = "  esc back  |  enter confirm"
	}
	helpRendered := theme.DimStyle.Render(help)

	lines := []string{titleRendered, sep}
	lines = append(lines, bodyLines...)
	lines = append(lines, sep, helpRendered)

	content := strings.Join(lines, "\n")

	popup := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	return popup
}

// renderVersionList renders the scrollable version list for selection steps.
func (p VersionPicker) renderVersionList(popupWidth int) []string {
	if len(p.versions) == 0 {
		return []string{theme.DimStyle.Render("  No versions found")}
	}

	var lines []string

	for i, v := range p.versions {
		// In stepSelectB, show version A as already selected (dimmed with checkmark)
		if p.step == stepSelectB && i == p.selectedA {
			line := fmt.Sprintf("  %s  %s  %s  %s",
				theme.SuccessStyle.Render("*"),
				theme.ActionDisabledStyle.Render(v.ShortID()),
				theme.ActionDisabledStyle.Render(fmt.Sprintf("#%d", v.Number)),
				theme.ActionDisabledStyle.Render(v.RelativeTime()))
			lines = append(lines, line)
			continue
		}

		cursor := "  "
		if i == p.cursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		idStyle := theme.ActionItemStyle
		if i == p.cursor {
			idStyle = theme.SelectedItemStyle
		}

		// Format: > abc12345  #3  2 hours ago  via upload
		detail := v.RelativeTime()
		if v.TriggeredBy != "" {
			detail += "  via " + v.TriggeredBy
		}

		line := fmt.Sprintf("%s  %s  %s  %s",
			cursor,
			idStyle.Render(v.ShortID()),
			theme.LabelStyle.Render(fmt.Sprintf("#%d", v.Number)),
			theme.DimStyle.Render(detail))
		lines = append(lines, line)
	}

	return lines
}

// renderPercentageInput renders the traffic split input step.
func (p VersionPicker) renderPercentageInput(popupWidth int) []string {
	var lines []string

	vA := p.versions[p.selectedA]
	vB := p.versions[p.selectedB]

	// Show selected versions
	lines = append(lines, theme.ActionSectionStyle.Render("  Selected Versions"))
	lines = append(lines, fmt.Sprintf("  A: %s  %s",
		theme.ActionItemStyle.Render(vA.ShortID()),
		theme.LabelStyle.Render(fmt.Sprintf("#%d", vA.Number))))
	lines = append(lines, fmt.Sprintf("  B: %s  %s",
		theme.ActionItemStyle.Render(vB.ShortID()),
		theme.LabelStyle.Render(fmt.Sprintf("#%d", vB.Number))))
	lines = append(lines, "")

	// Percentage input
	lines = append(lines, theme.ActionSectionStyle.Render("  Traffic Split"))
	lines = append(lines, fmt.Sprintf("  Version A percentage: %s", p.pctInput.View()))

	// Live preview
	val := strings.TrimSpace(p.pctInput.Value())
	pct, err := strconv.Atoi(val)
	if err == nil && pct >= 0 && pct <= 100 {
		pctB := 100 - pct
		barWidth := popupWidth - 16
		if barWidth < 20 {
			barWidth = 20
		}
		filledA := barWidth * pct / 100
		filledB := barWidth - filledA

		barA := strings.Repeat("█", filledA)
		barB := strings.Repeat("░", filledB)

		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("  %s %s",
			theme.SelectedItemStyle.Render(barA),
			theme.DimStyle.Render(barB)))
		lines = append(lines, fmt.Sprintf("  %s  %s",
			theme.SelectedItemStyle.Render(fmt.Sprintf("A: %d%%", pct)),
			theme.DimStyle.Render(fmt.Sprintf("B: %d%%", pctB))))
	}

	if p.pctErr != "" {
		lines = append(lines, "")
		lines = append(lines, "  "+theme.ErrorStyle.Render(p.pctErr))
	}

	return lines
}
