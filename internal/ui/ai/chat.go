package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Chat message types ---

// chatMsg is a message in the conversation.
type chatMsg struct {
	role      ChatRole
	content   string
	timestamp time.Time
}

// permissionPrompt holds an active OpenCode permission request awaiting user confirmation.
type permissionPrompt struct {
	ID        string // permission ID for the API response
	SessionID string // session ID for the API response
	Title     string // human-readable description (e.g. "Edit file src/main.ts")
	Type      string // permission type (e.g. "edit", "bash")
}

// chatModel holds the chat conversation state.
type chatModel struct {
	messages    []chatMsg // conversation history
	input       string    // current input text
	inputCursor int       // cursor position in input
	scrollY     int       // scroll offset for message history
	streaming   bool      // true while receiving a streaming response
	streamBuf   string    // accumulated streaming response text

	// Permission prompt — non-nil when OpenCode is waiting for user confirmation.
	pendingPermission *permissionPrompt

	// Status text shown next to the spinner (e.g., "agent working..." during
	// OpenCode tool execution). Cleared when text arrives or stream ends.
	statusText string

	// Animated spinner shown during streaming.
	spin spinner.Model

	// lastMouseTime tracks when the last tea.MouseMsg was received.
	// Used to suppress stray typed characters (e.g. "[" from partially-parsed
	// CSI escape sequences) that arrive immediately after mouse wheel events.
	lastMouseTime time.Time

	// viewMaxScroll tracks the maximum scroll offset computed during the last View() pass.
	// Shared via pointer so that value-receiver View() methods can write it and
	// value-receiver Update() methods can read it for immediate clamping.
	// This prevents scrollY from drifting past the real max, which causes
	// delayed scroll response at boundaries and stray escape-sequence characters.
	viewMaxScroll *int
}

func newChatModel() chatModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)
	maxScroll := 0
	return chatModel{spin: s, viewMaxScroll: &maxScroll}
}

// --- Messages emitted to the app layer ---

// AIChatSendMsg is emitted when the user sends a message. The app layer
// constructs the full prompt (with context) and starts the streaming request.
type AIChatSendMsg struct {
	UserMessage string
}

// AIChatStreamChunkMsg delivers a text chunk from the streaming response.
type AIChatStreamChunkMsg struct {
	Text string
}

// AIChatStreamDoneMsg signals the streaming response is complete.
type AIChatStreamDoneMsg struct {
	Err error
}

// AIChatNewConversationMsg signals the user wants to start a new conversation.
type AIChatNewConversationMsg struct{}

// AIChatPermissionMsg delivers an OpenCode permission request to the chat model.
// The chat renders a yellow prompt and waits for the user to press y/a/n.
type AIChatPermissionMsg struct {
	ID        string // permission ID
	SessionID string // session ID
	Title     string // human-readable title
	Type      string // permission type (e.g. "edit", "bash")
}

// AIChatPermissionResolvedMsg clears a pending permission prompt (answered by us or another client).
type AIChatPermissionResolvedMsg struct {
	PermissionID string
}

// AIPermissionResponseMsg is emitted by the chat model when the user responds to a
// permission prompt. The app layer sends the HTTP POST to the OpenCode API.
type AIPermissionResponseMsg struct {
	PermissionID string // permission ID
	SessionID    string // session ID
	Response     string // "once", "always", or "reject"
}

// AIChatStatusMsg delivers a status update from the OpenCode agent (e.g., "busy"
// during tool execution). Shown next to the spinner in the chat separator.
type AIChatStatusMsg struct {
	Status string // e.g. "busy"
}

// --- Update ---

func (c chatModel) update(msg tea.Msg, focused bool) (chatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case AIChatStreamChunkMsg:
		c.streamBuf += msg.Text
		c.statusText = "" // text arriving — agent is no longer executing tools
		// Auto-scroll to bottom during streaming
		c.scrollToBottom()
		return c, nil

	case AIChatStatusMsg:
		c.statusText = msg.Status
		return c, nil

	case AIChatStreamDoneMsg:
		c.streaming = false
		c.statusText = ""
		c.pendingPermission = nil // clear any stale permission prompt
		if msg.Err != nil {
			c.messages = append(c.messages, chatMsg{
				role:      RoleAssistant,
				content:   fmt.Sprintf("Error: %v", msg.Err),
				timestamp: time.Now(),
			})
		} else if c.streamBuf != "" {
			c.messages = append(c.messages, chatMsg{
				role:      RoleAssistant,
				content:   c.streamBuf,
				timestamp: time.Now(),
			})
		}
		c.streamBuf = ""
		// Use a large value — view will clamp to the actual max
		c.scrollY = 999999
		return c, nil

	case AIChatPermissionMsg:
		c.pendingPermission = &permissionPrompt{
			ID:        msg.ID,
			SessionID: msg.SessionID,
			Title:     msg.Title,
			Type:      msg.Type,
		}
		c.scrollToBottom()
		return c, nil

	case AIChatPermissionResolvedMsg:
		if c.pendingPermission != nil && c.pendingPermission.ID == msg.PermissionID {
			c.pendingPermission = nil
		}
		return c, nil

	case spinner.TickMsg:
		if c.streaming {
			var cmd tea.Cmd
			c.spin, cmd = c.spin.Update(msg)
			return c, cmd
		}
		return c, nil

	case tea.MouseMsg:
		c.lastMouseTime = time.Now()
		if !focused {
			return c, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			c.clampScroll()
			c.scrollY -= 3
			if c.scrollY < 0 {
				c.scrollY = 0
			}
		case tea.MouseButtonWheelDown:
			c.clampScroll()
			c.scrollY += 3
			c.clampScroll()
		}
		return c, nil

	case tea.KeyMsg:
		if !focused {
			return c, nil
		}

		// Permission prompt intercepts y/a/n keys before normal input handling
		if c.pendingPermission != nil && c.streaming {
			perm := c.pendingPermission
			switch msg.String() {
			case "y":
				c.pendingPermission = nil
				return c, func() tea.Msg {
					return AIPermissionResponseMsg{
						PermissionID: perm.ID,
						SessionID:    perm.SessionID,
						Response:     "once",
					}
				}
			case "a":
				c.pendingPermission = nil
				return c, func() tea.Msg {
					return AIPermissionResponseMsg{
						PermissionID: perm.ID,
						SessionID:    perm.SessionID,
						Response:     "always",
					}
				}
			case "n":
				c.pendingPermission = nil
				return c, func() tea.Msg {
					return AIPermissionResponseMsg{
						PermissionID: perm.ID,
						SessionID:    perm.SessionID,
						Response:     "reject",
					}
				}
			}
			// Fall through — esc and pgup/pgdown still work
		}

		switch msg.String() {
		case "enter":
			if c.streaming {
				return c, nil // can't send while streaming
			}
			text := strings.TrimSpace(c.input)
			if text == "" {
				return c, nil
			}
			c.messages = append(c.messages, chatMsg{
				role:      RoleUser,
				content:   text,
				timestamp: time.Now(),
			})
			c.input = ""
			c.inputCursor = 0
			c.streaming = true
			c.streamBuf = ""
			c.pendingPermission = nil
			c.scrollToBottom()
			return c, func() tea.Msg { return AIChatSendMsg{UserMessage: text} }

		case "ctrl+n":
			return c, func() tea.Msg { return AIChatNewConversationMsg{} }

		case "backspace":
			runes := []rune(c.input)
			if c.inputCursor > 0 {
				c.input = string(runes[:c.inputCursor-1]) + string(runes[c.inputCursor:])
				c.inputCursor--
			}
		case "delete":
			runes := []rune(c.input)
			if c.inputCursor < len(runes) {
				c.input = string(runes[:c.inputCursor]) + string(runes[c.inputCursor+1:])
			}
		case "left":
			if c.inputCursor > 0 {
				c.inputCursor--
			}
		case "right":
			runes := []rune(c.input)
			if c.inputCursor < len(runes) {
				c.inputCursor++
			}
		case "home", "ctrl+a":
			c.inputCursor = 0
		case "end", "ctrl+e":
			c.inputCursor = len([]rune(c.input))
		case "ctrl+u":
			runes := []rune(c.input)
			c.input = string(runes[c.inputCursor:])
			c.inputCursor = 0
		case "ctrl+k":
			runes := []rune(c.input)
			c.input = string(runes[:c.inputCursor])

		// Scroll message history
		case "pgup":
			c.clampScroll()
			c.scrollY -= 10
			if c.scrollY < 0 {
				c.scrollY = 0
			}
		case "pgdown":
			c.clampScroll()
			c.scrollY += 10
			c.clampScroll()

		default:
			// Handle paste (bracketed paste delivers all chars in Runes)
			if msg.Paste {
				runes := []rune(c.input)
				newRunes := make([]rune, 0, len(runes)+len(msg.Runes))
				newRunes = append(newRunes, runes[:c.inputCursor]...)
				newRunes = append(newRunes, msg.Runes...)
				newRunes = append(newRunes, runes[c.inputCursor:]...)
				c.input = string(newRunes)
				c.inputCursor += len(msg.Runes)
				return c, nil
			}
			// Suppress stray characters from partially-parsed mouse escape sequences.
			// When the terminal sends SGR mouse events (e.g. \x1b[<65;30;10M) in rapid
			// bursts, some bytes may arrive as tea.KeyMsg instead of tea.MouseMsg.
			// These fragments (typically "[", "<", digits, "M", "m") appear within
			// ~10ms of a real mouse event. Drop them to prevent input corruption.
			if !c.lastMouseTime.IsZero() && time.Since(c.lastMouseTime) < 100*time.Millisecond {
				return c, nil
			}
			// Insert typed character (supports multi-byte Unicode)
			if len(msg.Runes) == 1 {
				runes := []rune(c.input)
				newRunes := make([]rune, 0, len(runes)+1)
				newRunes = append(newRunes, runes[:c.inputCursor]...)
				newRunes = append(newRunes, msg.Runes...)
				newRunes = append(newRunes, runes[c.inputCursor:]...)
				c.input = string(newRunes)
				c.inputCursor++
			}
		}
	}
	return c, nil
}

func (c *chatModel) scrollToBottom() {
	// Will be clamped during rendering
	c.scrollY = 999999
}

// clampScroll constrains scrollY to [0, viewMaxScroll] using the max scroll
// value computed during the previous View() pass. This prevents scrollY from
// drifting far past the real boundary, which would cause delayed response
// when scrolling back in the opposite direction.
func (c *chatModel) clampScroll() {
	if c.viewMaxScroll != nil && *c.viewMaxScroll >= 0 {
		if c.scrollY > *c.viewMaxScroll {
			c.scrollY = *c.viewMaxScroll
		}
	}
	if c.scrollY < 0 {
		c.scrollY = 0
	}
}

// newConversation clears the chat history.
func (c *chatModel) newConversation() {
	c.messages = nil
	c.input = ""
	c.inputCursor = 0
	c.scrollY = 0
	c.streaming = false
	c.streamBuf = ""
	c.pendingPermission = nil
	if c.viewMaxScroll != nil {
		*c.viewMaxScroll = 0
	}
}

// conversationMessages returns the messages formatted for the AI API.
func (c chatModel) conversationMessages() []ChatMessage {
	msgs := make([]ChatMessage, len(c.messages))
	for i, m := range c.messages {
		msgs[i] = ChatMessage{Role: m.role, Content: m.content}
	}
	return msgs
}

// --- View ---

func (c chatModel) view(w, h int, focused bool, provisioned bool, backendName string) string {
	var borderStyle lipgloss.Style
	if focused {
		borderStyle = theme.ActiveBorderStyle.
			Width(w - 2).
			Height(h - 2)
	} else {
		borderStyle = theme.BorderStyle.
			Width(w - 2).
			Height(h - 2)
	}

	innerW := w - 4 // border + padding
	innerH := h - 4

	if !provisioned {
		return borderStyle.Render(c.viewNotProvisioned(innerW, innerH, backendName))
	}

	// Split inner area: messages area + separator + [permission prompt] + input line.
	// When a permission prompt is active, the input area expands by 1 line.
	inputHeight := 1 // single-line input
	permissionHeight := 0
	if c.pendingPermission != nil {
		permissionHeight = 1
	}
	separatorHeight := 1
	messagesHeight := innerH - inputHeight - permissionHeight - separatorHeight
	if messagesHeight < 3 {
		messagesHeight = 3
	}

	// Render messages
	messagesView := c.renderMessages(innerW, messagesHeight, backendName)

	// Separator — with spinner in the lower-right when streaming
	sep := c.renderSeparator(innerW)

	// Permission prompt (yellow banner, only when pending)
	permView := ""
	if c.pendingPermission != nil {
		permView = c.renderPermissionPrompt(innerW)
	}

	// Input line
	inputView := c.renderInput(innerW, focused)

	// Compose vertically: messages + separator + [permission] + input
	parts := []string{messagesView, sep}
	if permView != "" {
		parts = append(parts, permView)
	}
	parts = append(parts, inputView)

	return borderStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left, parts...),
	)
}

func (c chatModel) viewNotProvisioned(w, h int, backendName string) string {
	title := theme.TitleStyle.Render("AI Log Analysis")
	hint := theme.DimStyle.Render(backendName)
	setupLine := theme.DimStyle.Render("Press ctrl+s to open settings and configure an AI backend.")

	msg := lipgloss.JoinVertical(lipgloss.Center,
		title, "", hint, "", setupLine,
	)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
}

func (c chatModel) renderMessages(w, h int, backendName string) string {
	if len(c.messages) == 0 && c.streamBuf == "" {
		// Empty state
		welcome := lipgloss.JoinVertical(lipgloss.Center,
			theme.TitleStyle.Render("AI Log Analysis"),
			theme.DimStyle.Render(backendName),
			"",
			theme.DimStyle.Render("Select context sources on the left, then"),
			theme.DimStyle.Render("type a message below to start analyzing logs."),
		)
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, welcome)
	}

	// Render each message
	var lines []string
	for _, msg := range c.messages {
		lines = append(lines, c.renderSingleMessage(msg, w)...)
		lines = append(lines, "") // blank line between messages
	}

	// Append streaming response if active
	if c.streaming && c.streamBuf != "" {
		lines = append(lines, renderRoleBadge(RoleAssistant))
		rendered := renderMarkdown(c.streamBuf, w-2)
		for _, line := range strings.Split(rendered, "\n") {
			lines = append(lines, line)
		}
		lines = append(lines, theme.DimStyle.Render("▍")) // streaming cursor
	}

	// Apply scroll
	totalLines := len(lines)
	maxScroll := totalLines - h
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Store maxScroll for the next Update() cycle so scrollY can be clamped
	// immediately, preventing drift and delayed boundary response.
	if c.viewMaxScroll != nil {
		*c.viewMaxScroll = maxScroll
	}

	scrollY := c.scrollY
	if scrollY > maxScroll {
		scrollY = maxScroll
	}
	if scrollY < 0 {
		scrollY = 0
	}

	// Slice visible window
	start := scrollY
	end := start + h
	if end > totalLines {
		end = totalLines
	}
	if start > totalLines {
		start = totalLines
	}

	visible := lines[start:end]

	// Pad to full height
	for len(visible) < h {
		visible = append(visible, "")
	}

	return strings.Join(visible, "\n")
}

func (c chatModel) renderSingleMessage(msg chatMsg, w int) []string {
	var lines []string
	lines = append(lines, renderRoleBadge(msg.role))

	if msg.role == RoleAssistant {
		// Render assistant messages as markdown with syntax highlighting
		rendered := renderMarkdown(msg.content, w-2)
		for _, line := range strings.Split(rendered, "\n") {
			lines = append(lines, line)
		}
	} else {
		// User messages: plain blue text
		wrapped := wordWrap(msg.content, w-2)
		style := lipgloss.NewStyle().Foreground(theme.ColorBlue)
		for _, line := range strings.Split(wrapped, "\n") {
			lines = append(lines, style.Render(line))
		}
	}

	return lines
}

func renderRoleBadge(role ChatRole) string {
	switch role {
	case RoleUser:
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.ColorBlue).
			Render("You")
	case RoleAssistant:
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.ColorOrange).
			Render("AI")
	default:
		return ""
	}
}

func (c chatModel) renderInput(w int, focused bool) string {
	prompt := lipgloss.NewStyle().
		Foreground(theme.ColorOrange).
		Bold(true).
		Render("> ")

	if c.streaming {
		if c.pendingPermission != nil {
			return prompt + lipgloss.NewStyle().Foreground(theme.ColorYellow).Render("awaiting confirmation... (esc to stop)")
		}
		spinnerStr := c.spin.View()
		var statusLabel string
		if c.statusText == "busy" {
			statusLabel = "agent working..."
		} else if c.streamBuf == "" {
			statusLabel = "thinking..."
		} else {
			statusLabel = "streaming..."
		}
		escHint := theme.DimStyle.Render(" (esc to stop)")
		return prompt + spinnerStr + " " + theme.DimStyle.Render(statusLabel) + escHint
	}

	inputStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
	if !focused {
		inputStyle = theme.DimStyle
	}

	// Show input with cursor (rune-based for Unicode support)
	maxInputWidth := w - 3 // prompt takes ~2 chars + 1 margin
	runes := []rune(c.input)
	cursor := c.inputCursor

	// Scroll input if it's wider than the display area
	displayRunes := runes
	if len(displayRunes) > maxInputWidth {
		start := cursor - maxInputWidth + 5
		if start < 0 {
			start = 0
		}
		end := start + maxInputWidth
		if end > len(displayRunes) {
			end = len(displayRunes)
			start = end - maxInputWidth
			if start < 0 {
				start = 0
			}
		}
		cursor = cursor - start
		displayRunes = displayRunes[start:end]
	}

	display := string(displayRunes)

	// Insert cursor
	if focused && cursor >= 0 && cursor <= len(displayRunes) {
		before := string(displayRunes[:cursor])
		afterRunes := displayRunes[cursor:]
		cursorChar := lipgloss.NewStyle().
			Reverse(true).
			Render(" ")
		if len(afterRunes) > 0 {
			cursorChar = lipgloss.NewStyle().
				Reverse(true).
				Render(string(afterRunes[0]))
			afterRunes = afterRunes[1:]
		}
		return prompt + inputStyle.Render(before) + cursorChar + inputStyle.Render(string(afterRunes))
	}

	return prompt + inputStyle.Render(display)
}

// renderSeparator renders the horizontal separator line between messages and the input area.
func (c chatModel) renderSeparator(w int) string {
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorDarkGray)
	return sepStyle.Render(strings.Repeat("─", w))
}

// renderPermissionPrompt renders the yellow permission confirmation banner.
// Shown below the separator when OpenCode is waiting for user confirmation.
func (c chatModel) renderPermissionPrompt(w int) string {
	if c.pendingPermission == nil {
		return ""
	}

	yellowStyle := lipgloss.NewStyle().Foreground(theme.ColorYellow)
	dimStyle := lipgloss.NewStyle().Foreground(theme.ColorGray)

	// Truncate title if it exceeds available width
	title := c.pendingPermission.Title
	hintStr := "  [y] allow  [a] always  [n] reject"
	maxTitleWidth := w - lipgloss.Width(hintStr) - 4 // 4 for prefix + margin
	if maxTitleWidth < 10 {
		maxTitleWidth = 10
	}
	titleRunes := []rune(title)
	if len(titleRunes) > maxTitleWidth {
		title = string(titleRunes[:maxTitleWidth-1]) + "\u2026" // ellipsis
	}

	prefix := yellowStyle.Bold(true).Render("\u26A0 ") // warning sign
	titleView := yellowStyle.Render(title)
	hintView := dimStyle.Render(hintStr)

	return prefix + titleView + hintView
}

// --- Markdown rendering ---

// renderMarkdown renders markdown text using glamour with a custom dark style
// that matches the orangeshell color palette. Returns the rendered lines.
// Falls back to plain word-wrapped text if glamour fails.
func renderMarkdown(content string, width int) string {
	if width <= 4 {
		width = 80
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return wordWrap(content, width)
	}

	rendered, err := r.Render(content)
	if err != nil {
		return wordWrap(content, width)
	}

	// glamour adds leading/trailing newlines from the Document block.
	// Strip them since we manage vertical spacing ourselves.
	rendered = strings.TrimRight(rendered, "\n")
	rendered = strings.TrimLeft(rendered, "\n")

	return rendered
}

// --- Helpers ---

// wordWrap wraps text at word boundaries to fit within maxWidth.
// Used for user messages and as a fallback when glamour fails.
func wordWrap(text string, maxWidth int) string {
	if maxWidth <= 0 {
		maxWidth = 80
	}

	var result strings.Builder
	for _, paragraph := range strings.Split(text, "\n") {
		if result.Len() > 0 {
			result.WriteString("\n")
		}

		words := strings.Fields(paragraph)
		if len(words) == 0 {
			continue
		}

		lineLen := 0
		for i, word := range words {
			wLen := len(word)
			if i > 0 && lineLen+1+wLen > maxWidth {
				result.WriteString("\n")
				lineLen = 0
			} else if i > 0 {
				result.WriteString(" ")
				lineLen++
			}
			result.WriteString(word)
			lineLen += wLen
		}
	}

	return result.String()
}
