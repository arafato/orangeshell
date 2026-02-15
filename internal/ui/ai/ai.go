// Package ai implements the AI tab — a dual-pane view with a context panel on
// the left (~30%) showing selectable log sources, and a chat panel on the right
// (~70%) for conversational AI-powered log analysis using Workers AI.
package ai

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Constants ---

const (
	leftPaneRatio = 30 // context panel gets ~30% of width
)

// --- Focus ---

// FocusPane identifies which pane has keyboard focus.
type FocusPane int

const (
	FocusContext FocusPane = iota // Context panel (log source selection)
	FocusChat                     // Chat panel (conversation)
)

// --- Mode ---

// Mode identifies which view is shown within the AI tab.
type Mode int

const (
	ModeChat     Mode = iota // Chat + context panel (default)
	ModeSettings             // AI provider/model settings
)

// --- Model ---

// Model is the Bubble Tea model for the AI tab.
type Model struct {
	focus    FocusPane
	mode     Mode
	settings settingsModel
	chat     chatModel
	context  contextModel
	width    int
	height   int

	// workerSecret is kept in memory (loaded from config) for API calls.
	workerSecret string
}

// New creates a new AI tab model.
func New() Model {
	return Model{
		focus:    FocusChat,
		mode:     ModeChat,
		settings: newSettingsModel(),
		chat:     newChatModel(),
		context:  newContextModel(),
	}
}

// SetSize updates the available dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// LoadConfig populates the AI tab state from the persistent config.
func (m *Model) LoadConfig(cfg *config.Config) {
	m.settings.LoadFromConfig(cfg)
	m.workerSecret = cfg.AIWorkerSecret
}

// SetWorkerURL updates the deployed worker URL (called after provisioning).
func (m *Model) SetWorkerURL(url string) {
	m.settings.workerURL = url
}

// SetWorkerSecret updates the worker auth secret.
func (m *Model) SetWorkerSecret(secret string) {
	m.workerSecret = secret
}

// SetDeploying sets the deploying state.
func (m *Model) SetDeploying(deploying bool) {
	m.settings.deploying = deploying
}

// SetDeployError sets the last deploy error.
func (m *Model) SetDeployError(err string) {
	m.settings.deployError = err
}

// IsProvisioned returns true if the AI Worker has been deployed.
func (m Model) IsProvisioned() bool {
	return m.settings.workerURL != ""
}

// WorkerURL returns the deployed AI Worker URL.
func (m Model) WorkerURL() string {
	return m.settings.workerURL
}

// Secret returns the AI Worker auth secret.
func (m Model) Secret() string {
	return m.workerSecret
}

// ModelPreset returns the selected model preset.
func (m Model) ModelPreset() config.AIModelPreset {
	return m.settings.modelPreset
}

// Focus returns the currently focused pane.
func (m Model) Focus() FocusPane {
	return m.focus
}

// CurrentMode returns the current view mode.
func (m Model) CurrentMode() Mode {
	return m.mode
}

// IsTextInputActive returns true when the chat input field has text or is
// actively streaming, so number keys should be typed rather than switching tabs.
// When the input is empty and not streaming, number keys switch tabs as normal.
func (m Model) IsTextInputActive() bool {
	if m.mode != ModeChat || m.focus != FocusChat {
		return false
	}
	return m.chat.input != "" || m.chat.streaming
}

// IsStreaming returns true while the AI is generating a response.
func (m Model) IsStreaming() bool {
	return m.chat.streaming
}

// SetContextSources updates the available context sources (from monitoring grid panes).
func (m *Model) SetContextSources(sources []ContextSource) {
	m.context.SetSources(sources)
}

// SelectedContextIDs returns the script IDs of selected context sources.
func (m Model) SelectedContextIDs() []string {
	return m.context.SelectedScriptIDs()
}

// ConversationMessages returns the chat history formatted for the AI API.
func (m Model) ConversationMessages() []ChatMessage {
	return m.chat.conversationMessages()
}

// --- Update ---

// Update handles messages for the AI tab.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	// Always route streaming messages to the chat model, regardless of mode
	switch msg.(type) {
	case AIChatStreamChunkMsg, AIChatStreamDoneMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.update(msg, true)
		return m, cmd
	}

	// Route to settings if in settings mode
	if m.mode == ModeSettings {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "esc" {
			m.mode = ModeChat
			return m, nil
		}
		var cmd tea.Cmd
		m.settings, cmd = m.settings.update(msg)
		return m, cmd
	}

	// Chat mode — handle global keys first
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "tab":
			if m.focus == FocusContext {
				m.focus = FocusChat
			} else {
				m.focus = FocusContext
			}
			return m, nil
		case "s":
			// Enter settings if: (a) context pane is focused, or (b) chat input is empty
			if m.focus != FocusChat || m.chat.input == "" {
				m.mode = ModeSettings
				return m, nil
			}
			// If chat has text, fall through to chat model (type 's' in input)
		}
	}

	// Route to the focused pane
	var cmd tea.Cmd
	switch m.focus {
	case FocusChat:
		m.chat, cmd = m.chat.update(msg, true)
	case FocusContext:
		m.context, cmd = m.context.update(msg, true)
	}
	return m, cmd
}

// NewConversation clears the chat history.
func (m *Model) NewConversation() {
	m.chat.newConversation()
}

// --- View ---

// View renders the AI tab.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	switch m.mode {
	case ModeSettings:
		return m.truncateToHeight(m.settings.view(m.width, m.height))
	default:
		return m.viewChatMode()
	}
}

// truncateToHeight ensures the view output is exactly m.height lines.
func (m Model) truncateToHeight(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// viewChatMode renders the dual-pane chat + context view.
func (m Model) viewChatMode() string {
	contentHeight := m.height
	if contentHeight < 1 {
		contentHeight = 1
	}

	leftWidth := m.width * leftPaneRatio / 100
	rightWidth := m.width - leftWidth - 1 // -1 for separator

	if leftWidth < 10 {
		leftWidth = 10
	}
	if rightWidth < 20 {
		rightWidth = 20
	}

	leftContent := m.context.view(leftWidth, contentHeight, m.focus == FocusContext)
	rightContent := m.chat.view(rightWidth, contentHeight, m.focus == FocusChat, m.IsProvisioned(), m.settings.modelPreset)

	// Vertical separator — exactly contentHeight lines
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorDarkGray)
	var sepLines []string
	for i := 0; i < contentHeight; i++ {
		sepLines = append(sepLines, sepStyle.Render("│"))
	}
	sep := strings.Join(sepLines, "\n")

	result := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, sep, rightContent)

	// Truncate to exact contentHeight to prevent overflow from lipgloss padding
	lines := strings.Split(result, "\n")
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	return strings.Join(lines, "\n")
}

// --- Help ---

// HelpEntry is a key-description pair for the help bar.
type HelpEntry struct {
	Key  string
	Desc string
}

// HelpEntries returns the context-sensitive help entries for the AI tab.
func (m Model) HelpEntries() []HelpEntry {
	switch m.mode {
	case ModeSettings:
		return []HelpEntry{
			{"j/k", "navigate"},
			{"h/l", "change"},
			{"enter", "deploy"},
			{"esc", "back"},
		}
	default:
		entries := []HelpEntry{
			{"tab", "switch pane"},
		}
		if m.focus == FocusContext {
			entries = append(entries,
				HelpEntry{"j/k", "navigate"},
				HelpEntry{"space", "toggle"},
				HelpEntry{"a/n", "all/none"},
				HelpEntry{"s", "settings"},
			)
		}
		if m.focus == FocusChat {
			entries = append(entries,
				HelpEntry{"enter", "send"},
				HelpEntry{"ctrl+n", "new chat"},
				HelpEntry{"pgup/dn", "scroll"},
			)
		}
		entries = append(entries, HelpEntry{"ctrl+h", "home"})
		return entries
	}
}
