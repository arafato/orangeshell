package ai

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// settingsSection identifies a section in the settings view.
type settingsSection int

const (
	sectionProvider settingsSection = iota
	sectionModel
	sectionDeploy
	sectionCount // sentinel
)

// settingsModel holds the state for the Settings mode.
type settingsModel struct {
	section     settingsSection      // currently focused section
	provider    config.AIProvider    // selected provider
	modelPreset config.AIModelPreset // selected model preset
	workerURL   string               // deployed worker URL (empty = not deployed)
	deploying   bool                 // true while provisioning is in progress
	deployError string               // last deploy error (empty = no error)
}

func newSettingsModel() settingsModel {
	return settingsModel{
		section:     sectionProvider,
		provider:    config.AIProviderWorkersAI,
		modelPreset: config.AIModelBalanced,
	}
}

// LoadFromConfig populates the settings model from the persistent config.
func (s *settingsModel) LoadFromConfig(cfg *config.Config) {
	if cfg.AIProvider != config.AIProviderNone {
		s.provider = cfg.AIProvider
	}
	if cfg.AIModelPreset != "" {
		s.modelPreset = cfg.AIModelPreset
	}
	s.workerURL = cfg.AIWorkerURL
}

// --- Messages ---

// AIConfigSaveMsg is emitted when the AI settings change and should be persisted.
type AIConfigSaveMsg struct {
	Provider    config.AIProvider
	ModelPreset config.AIModelPreset
}

// AIProvisionRequestMsg is emitted when the user requests to deploy the AI Worker.
type AIProvisionRequestMsg struct{}

// AIDeprovisionRequestMsg is emitted when the user requests to remove the AI Worker.
type AIDeprovisionRequestMsg struct{}

// --- Update ---

func (s settingsModel) update(msg tea.Msg) (settingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if s.section < sectionCount-1 {
				s.section++
			}
		case "k", "up":
			if s.section > 0 {
				s.section--
			}
		case "h", "left":
			switch s.section {
			case sectionModel:
				s.modelPreset = prevPreset(s.modelPreset)
				return s, func() tea.Msg {
					return AIConfigSaveMsg{Provider: s.provider, ModelPreset: s.modelPreset}
				}
			}
		case "l", "right":
			switch s.section {
			case sectionModel:
				s.modelPreset = nextPreset(s.modelPreset)
				return s, func() tea.Msg {
					return AIConfigSaveMsg{Provider: s.provider, ModelPreset: s.modelPreset}
				}
			}
		case "enter":
			switch s.section {
			case sectionDeploy:
				if s.workerURL == "" && !s.deploying {
					return s, func() tea.Msg { return AIProvisionRequestMsg{} }
				}
				if s.workerURL != "" && !s.deploying {
					return s, func() tea.Msg { return AIDeprovisionRequestMsg{} }
				}
			}
		}
	}
	return s, nil
}

// --- View ---

func (s settingsModel) view(w, h int) string {
	// Title
	title := theme.TitleStyle.Render("AI Settings")
	subtitle := theme.DimStyle.Render("Configure AI-powered log analysis  (esc to return)")

	// Provider section
	providerHeader := sectionHeader("Provider", s.section == sectionProvider)
	providerContent := s.renderProvider()

	// Model section
	modelHeader := sectionHeader("Model Preset", s.section == sectionModel)
	modelContent := s.renderModel()

	// Deploy section
	deployHeader := sectionHeader("Deployment", s.section == sectionDeploy)
	deployContent := s.renderDeploy()

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		subtitle,
		"",
		providerHeader,
		providerContent,
		"",
		modelHeader,
		modelContent,
		"",
		deployHeader,
		deployContent,
	)

	// Wrap in a box and center
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorDarkGray).
		Padding(1, 3).
		Width(60).
		Render(content)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

func sectionHeader(label string, active bool) string {
	style := lipgloss.NewStyle().Bold(true)
	if active {
		style = style.Foreground(theme.ColorOrange)
		return style.Render(fmt.Sprintf("> %s", label))
	}
	style = style.Foreground(theme.ColorWhite)
	return style.Render(fmt.Sprintf("  %s", label))
}

func (s settingsModel) renderProvider() string {
	workersAI := radioItem("Workers AI", s.provider == config.AIProviderWorkersAI, s.section == sectionProvider)
	workersDesc := theme.DimStyle.Render("    Deploys a proxy Worker to your Cloudflare account")
	anthropic := theme.DimStyle.Render("  " + radioOff + " Anthropic (coming soon)")

	return lipgloss.JoinVertical(lipgloss.Left,
		workersAI,
		workersDesc,
		anthropic,
	)
}

func (s settingsModel) renderModel() string {
	presets := []struct {
		preset config.AIModelPreset
		label  string
		model  string
	}{
		{config.AIModelFast, "Fast", "llama-3.1-8b-instruct-fast"},
		{config.AIModelBalanced, "Balanced", "llama-3.3-70b-instruct-fp8-fast"},
		{config.AIModelDeep, "Deep", "deepseek-r1-distill-qwen-32b"},
	}

	var lines []string
	for _, p := range presets {
		selected := s.modelPreset == p.preset
		label := fmt.Sprintf("%-9s %s", p.label, theme.DimStyle.Render("- "+p.model))
		line := radioItem(label, selected, s.section == sectionModel)
		lines = append(lines, line)
	}

	if s.section == sectionModel {
		lines = append(lines, theme.DimStyle.Render("  (h/l to change)"))
	}

	return strings.Join(lines, "\n")
}

func (s settingsModel) renderDeploy() string {
	if s.deploying {
		return lipgloss.NewStyle().Foreground(theme.ColorOrange).Render("  Deploying AI Worker...")
	}
	if s.deployError != "" {
		return theme.ErrorStyle.Render(fmt.Sprintf("  Error: %s", s.deployError))
	}
	if s.workerURL != "" {
		url := lipgloss.NewStyle().Foreground(theme.ColorGreen).Render(s.workerURL)
		status := lipgloss.NewStyle().Foreground(theme.ColorGreen).Bold(true).Render("  Deployed")
		line1 := fmt.Sprintf("%s  %s", status, url)
		var line2 string
		if s.section == sectionDeploy {
			line2 = lipgloss.NewStyle().Foreground(theme.ColorRed).Render("  Press enter to remove")
		} else {
			line2 = theme.DimStyle.Render("  Select to remove")
		}
		return lipgloss.JoinVertical(lipgloss.Left, line1, line2)
	}

	// Not deployed
	if s.section == sectionDeploy {
		return lipgloss.NewStyle().Foreground(theme.ColorOrange).Render("  Press enter to deploy AI Worker")
	}
	return theme.DimStyle.Render("  AI Worker not deployed")
}

// --- Helpers ---

const (
	radioOn  = "\u25cf" // ●
	radioOff = "\u25cb" // ○
)

func radioItem(label string, selected, sectionActive bool) string {
	if selected {
		style := lipgloss.NewStyle().Foreground(theme.ColorOrange)
		if sectionActive {
			style = style.Bold(true)
		}
		return style.Render(fmt.Sprintf("  %s %s", radioOn, label))
	}
	return theme.DimStyle.Render(fmt.Sprintf("  %s %s", radioOff, label))
}

func nextPreset(p config.AIModelPreset) config.AIModelPreset {
	switch p {
	case config.AIModelFast:
		return config.AIModelBalanced
	case config.AIModelBalanced:
		return config.AIModelDeep
	default:
		return config.AIModelDeep
	}
}

func prevPreset(p config.AIModelPreset) config.AIModelPreset {
	switch p {
	case config.AIModelDeep:
		return config.AIModelBalanced
	case config.AIModelBalanced:
		return config.AIModelFast
	default:
		return config.AIModelFast
	}
}
