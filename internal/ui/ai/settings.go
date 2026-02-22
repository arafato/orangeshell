package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// settingsSection identifies a section in the settings view.
// The visible sections change depending on the selected backend type.
type settingsSection int

const (
	sectionBackend settingsSection = iota // Backend type: Workers AI / HTTP Endpoint

	// Workers AI sections
	sectionModel  // Model preset (Fast / Balanced / Deep)
	sectionDeploy // Deploy / remove AI Worker

	// HTTP Endpoint sections
	sectionEndpoint   // URL input
	sectionProtocol   // openai / opencode
	sectionHTTPModel  // Model ID input
	sectionHTTPAPIKey // API key input
)

// settingsModel holds the state for the Settings mode.
type settingsModel struct {
	section        settingsSection      // currently focused section
	backendType    config.AIBackendType // "workers_ai" or "http"
	provider       config.AIProvider    // legacy: selected provider (kept for backward compat)
	modelPreset    config.AIModelPreset // Workers AI model preset
	workerURL      string               // deployed worker URL (empty = not deployed)
	deploying      bool                 // true while provisioning is in progress
	deployError    string               // last deploy error
	deployProgress string               // current provisioning step

	// HTTP Endpoint fields
	httpEndpoint string                // e.g. "http://localhost:11434"
	httpProtocol config.AIHTTPProtocol // "openai" or "opencode"
	httpModel    string                // model ID for HTTP backend
	httpAPIKey   string                // optional API key

	// Text input state for the currently focused HTTP field
	inputCursor int
}

func newSettingsModel() settingsModel {
	return settingsModel{
		section:      sectionBackend,
		backendType:  config.AIBackendWorkersAI,
		provider:     config.AIProviderWorkersAI,
		modelPreset:  config.AIModelBalanced,
		httpProtocol: config.AIHTTPProtocolOpenAI,
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

	// Load backend type (default to workers_ai for backward compat)
	if cfg.AIBackendType != "" {
		s.backendType = cfg.AIBackendType
	}
	if cfg.AIHTTPEndpoint != "" {
		s.httpEndpoint = cfg.AIHTTPEndpoint
	}
	if cfg.AIHTTPProtocol != "" {
		s.httpProtocol = cfg.AIHTTPProtocol
	}
	if cfg.AIHTTPModel != "" {
		s.httpModel = cfg.AIHTTPModel
	}
	if cfg.AIHTTPAPIKey != "" {
		s.httpAPIKey = cfg.AIHTTPAPIKey
	}
}

// visibleSections returns the ordered list of sections visible for the
// current backend type.
func (s settingsModel) visibleSections() []settingsSection {
	switch s.backendType {
	case config.AIBackendHTTP:
		return []settingsSection{sectionBackend, sectionEndpoint, sectionProtocol, sectionHTTPModel, sectionHTTPAPIKey}
	default: // Workers AI
		return []settingsSection{sectionBackend, sectionModel, sectionDeploy}
	}
}

// nextSection moves to the next visible section.
func (s *settingsModel) nextSection() {
	vis := s.visibleSections()
	for i, sec := range vis {
		if sec == s.section && i < len(vis)-1 {
			s.section = vis[i+1]
			s.inputCursor = len([]rune(s.currentFieldText()))
			return
		}
	}
}

// prevSection moves to the previous visible section.
func (s *settingsModel) prevSection() {
	vis := s.visibleSections()
	for i, sec := range vis {
		if sec == s.section && i > 0 {
			s.section = vis[i-1]
			s.inputCursor = len([]rune(s.currentFieldText()))
			return
		}
	}
}

// currentFieldText returns the text for the currently focused HTTP text input.
func (s settingsModel) currentFieldText() string {
	switch s.section {
	case sectionEndpoint:
		return s.httpEndpoint
	case sectionHTTPModel:
		return s.httpModel
	case sectionHTTPAPIKey:
		return s.httpAPIKey
	}
	return ""
}

// setCurrentFieldText sets the text for the currently focused HTTP text input.
func (s *settingsModel) setCurrentFieldText(text string) {
	switch s.section {
	case sectionEndpoint:
		s.httpEndpoint = text
	case sectionHTTPModel:
		s.httpModel = text
	case sectionHTTPAPIKey:
		s.httpAPIKey = text
	}
}

// isTextInputSection returns true if the current section is a text input field.
func (s settingsModel) isTextInputSection() bool {
	switch s.section {
	case sectionEndpoint, sectionHTTPModel, sectionHTTPAPIKey:
		return true
	}
	return false
}

// --- Messages ---

// AIConfigSaveMsg is emitted when the AI settings change and should be persisted.
type AIConfigSaveMsg struct {
	Provider     config.AIProvider
	ModelPreset  config.AIModelPreset
	BackendType  config.AIBackendType
	HTTPEndpoint string
	HTTPProtocol config.AIHTTPProtocol
	HTTPModel    string
	HTTPAPIKey   string
}

// AIProvisionRequestMsg is emitted when the user requests to deploy the AI Worker.
type AIProvisionRequestMsg struct{}

// AIDeprovisionRequestMsg is emitted when the user requests to remove the AI Worker.
type AIDeprovisionRequestMsg struct{}

// emitSave returns a tea.Cmd that emits AIConfigSaveMsg with current settings.
func (s settingsModel) emitSave() tea.Cmd {
	return func() tea.Msg {
		return AIConfigSaveMsg{
			Provider:     s.provider,
			ModelPreset:  s.modelPreset,
			BackendType:  s.backendType,
			HTTPEndpoint: s.httpEndpoint,
			HTTPProtocol: s.httpProtocol,
			HTTPModel:    s.httpModel,
			HTTPAPIKey:   s.httpAPIKey,
		}
	}
}

// --- Update ---

func (s settingsModel) update(msg tea.Msg) (settingsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle text input for HTTP fields first
		if s.isTextInputSection() {
			switch msg.String() {
			case "j", "down":
				// Save and move to next section
				cmd := s.emitSave()
				s.nextSection()
				return s, cmd
			case "k", "up":
				cmd := s.emitSave()
				s.prevSection()
				return s, cmd
			case "enter":
				// Save current field and move to next section
				cmd := s.emitSave()
				s.nextSection()
				return s, cmd
			case "backspace":
				text := s.currentFieldText()
				runes := []rune(text)
				if s.inputCursor > 0 {
					s.setCurrentFieldText(string(runes[:s.inputCursor-1]) + string(runes[s.inputCursor:]))
					s.inputCursor--
				}
				return s, nil
			case "delete":
				text := s.currentFieldText()
				runes := []rune(text)
				if s.inputCursor < len(runes) {
					s.setCurrentFieldText(string(runes[:s.inputCursor]) + string(runes[s.inputCursor+1:]))
				}
				return s, nil
			case "left":
				if s.inputCursor > 0 {
					s.inputCursor--
				}
				return s, nil
			case "right":
				runes := []rune(s.currentFieldText())
				if s.inputCursor < len(runes) {
					s.inputCursor++
				}
				return s, nil
			case "home", "ctrl+a":
				s.inputCursor = 0
				return s, nil
			case "end", "ctrl+e":
				s.inputCursor = len([]rune(s.currentFieldText()))
				return s, nil
			case "ctrl+u":
				text := s.currentFieldText()
				runes := []rune(text)
				s.setCurrentFieldText(string(runes[s.inputCursor:]))
				s.inputCursor = 0
				return s, nil
			case "ctrl+k":
				text := s.currentFieldText()
				runes := []rune(text)
				s.setCurrentFieldText(string(runes[:s.inputCursor]))
				return s, nil
			default:
				// Handle paste
				if msg.Paste {
					text := s.currentFieldText()
					runes := []rune(text)
					newRunes := make([]rune, 0, len(runes)+len(msg.Runes))
					newRunes = append(newRunes, runes[:s.inputCursor]...)
					newRunes = append(newRunes, msg.Runes...)
					newRunes = append(newRunes, runes[s.inputCursor:]...)
					s.setCurrentFieldText(string(newRunes))
					s.inputCursor += len(msg.Runes)
					return s, nil
				}
				// Insert typed character
				if utf8.RuneCountInString(msg.String()) == 1 {
					text := s.currentFieldText()
					runes := []rune(text)
					r := []rune(msg.String())
					newRunes := make([]rune, 0, len(runes)+1)
					newRunes = append(newRunes, runes[:s.inputCursor]...)
					newRunes = append(newRunes, r...)
					newRunes = append(newRunes, runes[s.inputCursor:]...)
					s.setCurrentFieldText(string(newRunes))
					s.inputCursor++
				}
				return s, nil
			}
		}

		// Non-text-input sections
		switch msg.String() {
		case "j", "down":
			s.nextSection()
		case "k", "up":
			s.prevSection()
		case "h", "left":
			switch s.section {
			case sectionBackend:
				s.backendType = prevBackendType(s.backendType)
				// Reset section to first visible section of the new backend
				s.section = sectionBackend
				return s, s.emitSave()
			case sectionModel:
				s.modelPreset = prevPreset(s.modelPreset)
				return s, s.emitSave()
			case sectionProtocol:
				s.httpProtocol = prevHTTPProtocol(s.httpProtocol)
				return s, s.emitSave()
			}
		case "l", "right":
			switch s.section {
			case sectionBackend:
				s.backendType = nextBackendType(s.backendType)
				s.section = sectionBackend
				return s, s.emitSave()
			case sectionModel:
				s.modelPreset = nextPreset(s.modelPreset)
				return s, s.emitSave()
			case sectionProtocol:
				s.httpProtocol = nextHTTPProtocol(s.httpProtocol)
				return s, s.emitSave()
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
	title := theme.TitleStyle.Render("AI Settings")
	subtitle := theme.DimStyle.Render("Configure AI-powered log analysis  (esc to return)")

	// Backend section (always visible)
	backendHeader := sectionHeader("Backend", s.section == sectionBackend)
	backendContent := s.renderBackend()

	parts := []string{title, subtitle, "", backendHeader, backendContent}

	switch s.backendType {
	case config.AIBackendHTTP:
		// HTTP Endpoint sections
		endpointHeader := sectionHeader("Endpoint URL", s.section == sectionEndpoint)
		endpointContent := s.renderTextInput(s.httpEndpoint, "http://localhost:11434", s.section == sectionEndpoint)

		protocolHeader := sectionHeader("Protocol", s.section == sectionProtocol)
		protocolContent := s.renderProtocol()

		modelHeader := sectionHeader("Model", s.section == sectionHTTPModel)
		modelContent := s.renderTextInput(s.httpModel, "e.g. anthropic/claude-sonnet-4-20250514", s.section == sectionHTTPModel)

		apiKeyHeader := sectionHeader("API Key", s.section == sectionHTTPAPIKey)
		apiKeyContent := s.renderAPIKeyInput()

		parts = append(parts,
			"", endpointHeader, endpointContent,
			"", protocolHeader, protocolContent,
			"", modelHeader, modelContent,
			"", apiKeyHeader, apiKeyContent,
		)

	default: // Workers AI
		modelHeader := sectionHeader("Model Preset", s.section == sectionModel)
		modelContent := s.renderModel()

		deployHeader := sectionHeader("Deployment", s.section == sectionDeploy)
		deployContent := s.renderDeploy()

		parts = append(parts,
			"", modelHeader, modelContent,
			"", deployHeader, deployContent,
		)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorDarkGray).
		Padding(1, 3).
		Width(66).
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

func (s settingsModel) renderBackend() string {
	types := []struct {
		bt   config.AIBackendType
		name string
		desc string
	}{
		{config.AIBackendWorkersAI, "Workers AI", "Deploys a proxy Worker to your Cloudflare account"},
		{config.AIBackendHTTP, "HTTP Endpoint", "OpenAI-compatible or OpenCode serve endpoint"},
	}

	var lines []string
	for _, t := range types {
		selected := s.backendType == t.bt
		line := radioItem(t.name, selected, s.section == sectionBackend)
		lines = append(lines, line)
		lines = append(lines, theme.DimStyle.Render("    "+t.desc))
	}
	if s.section == sectionBackend {
		lines = append(lines, theme.DimStyle.Render("  (h/l to change)"))
	}
	return strings.Join(lines, "\n")
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
		progress := "Deploying AI Worker..."
		if s.deployProgress != "" {
			progress = s.deployProgress
		}
		return lipgloss.NewStyle().Foreground(theme.ColorOrange).Render("  " + progress)
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

	if s.section == sectionDeploy {
		return lipgloss.NewStyle().Foreground(theme.ColorOrange).Render("  Press enter to deploy AI Worker")
	}
	return theme.DimStyle.Render("  AI Worker not deployed")
}

func (s settingsModel) renderProtocol() string {
	protocols := []struct {
		p    config.AIHTTPProtocol
		name string
		desc string
	}{
		{config.AIHTTPProtocolOpenAI, "OpenAI", "Standard /v1/chat/completions (Ollama, LM Studio, vLLM)"},
		{config.AIHTTPProtocolOpenCode, "OpenCode", "OpenCode serve session API"},
	}

	var lines []string
	for _, p := range protocols {
		selected := s.httpProtocol == p.p
		line := radioItem(p.name, selected, s.section == sectionProtocol)
		lines = append(lines, line)
	}
	if s.section == sectionProtocol {
		lines = append(lines, theme.DimStyle.Render("  (h/l to change)"))
	}
	return strings.Join(lines, "\n")
}

func (s settingsModel) renderTextInput(value, placeholder string, active bool) string {
	if !active {
		if value == "" {
			return theme.DimStyle.Render("  " + placeholder)
		}
		return theme.DimStyle.Render("  " + value)
	}

	// Active: show with cursor
	inputStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
	runes := []rune(value)
	cursor := s.inputCursor
	if cursor > len(runes) {
		cursor = len(runes)
	}

	if len(runes) == 0 {
		// Show placeholder with cursor
		cursorChar := lipgloss.NewStyle().Reverse(true).Render(" ")
		return "  " + cursorChar + theme.DimStyle.Render(placeholder)
	}

	before := string(runes[:cursor])
	var cursorChar string
	var after string
	if cursor < len(runes) {
		cursorChar = lipgloss.NewStyle().Reverse(true).Render(string(runes[cursor]))
		after = string(runes[cursor+1:])
	} else {
		cursorChar = lipgloss.NewStyle().Reverse(true).Render(" ")
		after = ""
	}

	return "  " + inputStyle.Render(before) + cursorChar + inputStyle.Render(after)
}

func (s settingsModel) renderAPIKeyInput() string {
	if !s.isTextInputSection() || s.section != sectionHTTPAPIKey {
		// Not focused — show masked or placeholder
		if s.httpAPIKey == "" {
			return theme.DimStyle.Render("  (optional)")
		}
		// Mask the key
		masked := s.httpAPIKey
		if len(masked) > 8 {
			masked = masked[:4] + strings.Repeat("*", len(masked)-8) + masked[len(masked)-4:]
		}
		return theme.DimStyle.Render("  " + masked)
	}
	// Active: show the text input with cursor (value shown in clear)
	return s.renderTextInput(s.httpAPIKey, "(optional)", true)
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

func nextBackendType(bt config.AIBackendType) config.AIBackendType {
	switch bt {
	case config.AIBackendWorkersAI:
		return config.AIBackendHTTP
	default:
		return config.AIBackendHTTP
	}
}

func prevBackendType(bt config.AIBackendType) config.AIBackendType {
	switch bt {
	case config.AIBackendHTTP:
		return config.AIBackendWorkersAI
	default:
		return config.AIBackendWorkersAI
	}
}

func nextHTTPProtocol(p config.AIHTTPProtocol) config.AIHTTPProtocol {
	switch p {
	case config.AIHTTPProtocolOpenAI:
		return config.AIHTTPProtocolOpenCode
	default:
		return config.AIHTTPProtocolOpenCode
	}
}

func prevHTTPProtocol(p config.AIHTTPProtocol) config.AIHTTPProtocol {
	switch p {
	case config.AIHTTPProtocolOpenCode:
		return config.AIHTTPProtocolOpenAI
	default:
		return config.AIHTTPProtocolOpenAI
	}
}
