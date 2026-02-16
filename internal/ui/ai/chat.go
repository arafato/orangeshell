package ai

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Chat message types ---

// chatMsg is a message in the conversation.
type chatMsg struct {
	role      ChatRole
	content   string
	timestamp time.Time
}

// chatModel holds the chat conversation state.
type chatModel struct {
	messages    []chatMsg // conversation history
	input       string    // current input text
	inputCursor int       // cursor position in input
	scrollY     int       // scroll offset for message history
	streaming   bool      // true while receiving a streaming response
	streamBuf   string    // accumulated streaming response text
}

func newChatModel() chatModel {
	return chatModel{}
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

// --- Update ---

func (c chatModel) update(msg tea.Msg, focused bool) (chatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case AIChatStreamChunkMsg:
		c.streamBuf += msg.Text
		// Auto-scroll to bottom during streaming
		c.scrollToBottom()
		return c, nil

	case AIChatStreamDoneMsg:
		c.streaming = false
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

	case tea.KeyMsg:
		if !focused {
			return c, nil
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
			c.scrollY -= 10
			if c.scrollY < 0 {
				c.scrollY = 0
			}
		case "pgdown":
			c.scrollY += 10

		default:
			// Insert typed character (supports multi-byte Unicode)
			if utf8.RuneCountInString(msg.String()) == 1 {
				runes := []rune(c.input)
				r := []rune(msg.String())
				newRunes := make([]rune, 0, len(runes)+1)
				newRunes = append(newRunes, runes[:c.inputCursor]...)
				newRunes = append(newRunes, r...)
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

// newConversation clears the chat history.
func (c *chatModel) newConversation() {
	c.messages = nil
	c.input = ""
	c.inputCursor = 0
	c.scrollY = 0
	c.streaming = false
	c.streamBuf = ""
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

func (c chatModel) view(w, h int, focused bool, provisioned bool, preset config.AIModelPreset) string {
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
		return borderStyle.Render(c.viewNotProvisioned(innerW, innerH))
	}

	// Split inner area: messages area + input line
	inputHeight := 1 // single-line input
	separatorHeight := 1
	messagesHeight := innerH - inputHeight - separatorHeight
	if messagesHeight < 3 {
		messagesHeight = 3
	}

	// Render messages
	messagesView := c.renderMessages(innerW, messagesHeight, preset)

	// Separator
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).
		Render(strings.Repeat("─", innerW))

	// Input line
	inputView := c.renderInput(innerW, focused)

	return borderStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left, messagesView, sep, inputView),
	)
}

func (c chatModel) viewNotProvisioned(w, h int) string {
	title := theme.TitleStyle.Render("AI Log Analysis")
	hint := theme.DimStyle.Render("Workers AI")
	setupLine := theme.DimStyle.Render("Press 's' to open settings and deploy the AI Worker.")

	msg := lipgloss.JoinVertical(lipgloss.Center,
		title, "", hint, "", setupLine,
	)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
}

func (c chatModel) renderMessages(w, h int, preset config.AIModelPreset) string {
	if len(c.messages) == 0 && c.streamBuf == "" {
		// Empty state
		welcome := lipgloss.JoinVertical(lipgloss.Center,
			theme.TitleStyle.Render("AI Log Analysis"),
			theme.DimStyle.Render(ModelDisplayName(preset)),
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
	} else if c.streaming {
		lines = append(lines, theme.DimStyle.Render("Thinking..."))
	}

	// Apply scroll
	totalLines := len(lines)
	maxScroll := totalLines - h
	if maxScroll < 0 {
		maxScroll = 0
	}
	if c.scrollY > maxScroll {
		// c.scrollY can't be modified here (value receiver), but we clamp for display
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
		return prompt + theme.DimStyle.Render("(streaming response...)")
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

// --- Markdown rendering ---

// renderMarkdown renders markdown text using glamour with a custom dark style
// that matches the orangeshell color palette. Returns the rendered lines.
// Falls back to plain word-wrapped text if glamour fails.
func renderMarkdown(content string, width int) string {
	if width <= 4 {
		width = 80
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(orangeShellStyle()),
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

// orangeShellStyle returns a glamour style config customized for our TUI.
// Based on the dark style but with zero document margin (we control layout),
// and orange-accented headings to match the orangeshell palette.
func orangeShellStyle() ansi.StyleConfig {
	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: stringPtr("#FAFAFA"),
			},
			Margin: uintPtr(0),
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: stringPtr("#7D7D7D"),
			},
			Indent:      uintPtr(1),
			IndentToken: stringPtr("│ "),
		},
		List: ansi.StyleList{
			LevelIndent: 2,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       stringPtr("#F6821F"),
				Bold:        boolPtr(true),
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "# ",
				Color:  stringPtr("#F6821F"),
				Bold:   boolPtr(true),
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "## ",
				Color:  stringPtr("#F6821F"),
				Bold:   boolPtr(true),
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "### ",
				Color:  stringPtr("#F6821F"),
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "#### ",
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "##### ",
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "###### ",
				Color:  stringPtr("#7D7D7D"),
				Bold:   boolPtr(false),
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut: boolPtr(true),
		},
		Emph: ansi.StylePrimitive{
			Italic: boolPtr(true),
		},
		Strong: ansi.StylePrimitive{
			Bold: boolPtr(true),
		},
		HorizontalRule: ansi.StylePrimitive{
			Color:  stringPtr("#3A3A3A"),
			Format: "\n--------\n",
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
		},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{},
			Ticked:         "[✓] ",
			Unticked:       "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Color:     stringPtr("#729FCF"),
			Underline: boolPtr(true),
		},
		LinkText: ansi.StylePrimitive{
			Color: stringPtr("#729FCF"),
			Bold:  boolPtr(true),
		},
		Image: ansi.StylePrimitive{
			Color:     stringPtr("#729FCF"),
			Underline: boolPtr(true),
		},
		ImageText: ansi.StylePrimitive{
			Color:  stringPtr("#7D7D7D"),
			Format: "Image: {{.text}} →",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           stringPtr("#F6821F"),
				BackgroundColor: stringPtr("#2A2A2A"),
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: stringPtr("#C4C4C4"),
				},
				Margin: uintPtr(1),
			},
			Chroma: &ansi.Chroma{
				Text: ansi.StylePrimitive{
					Color: stringPtr("#C4C4C4"),
				},
				Error: ansi.StylePrimitive{
					Color:           stringPtr("#F1F1F1"),
					BackgroundColor: stringPtr("#EF2929"),
				},
				Comment: ansi.StylePrimitive{
					Color: stringPtr("#676767"),
				},
				CommentPreproc: ansi.StylePrimitive{
					Color: stringPtr("#FF875F"),
				},
				Keyword: ansi.StylePrimitive{
					Color: stringPtr("#729FCF"),
				},
				KeywordReserved: ansi.StylePrimitive{
					Color: stringPtr("#FF5FD2"),
				},
				KeywordNamespace: ansi.StylePrimitive{
					Color: stringPtr("#FF5F87"),
				},
				KeywordType: ansi.StylePrimitive{
					Color: stringPtr("#6E6ED8"),
				},
				Operator: ansi.StylePrimitive{
					Color: stringPtr("#EF8080"),
				},
				Punctuation: ansi.StylePrimitive{
					Color: stringPtr("#E8E8A8"),
				},
				Name: ansi.StylePrimitive{
					Color: stringPtr("#C4C4C4"),
				},
				NameBuiltin: ansi.StylePrimitive{
					Color: stringPtr("#FF8EC7"),
				},
				NameTag: ansi.StylePrimitive{
					Color: stringPtr("#B083EA"),
				},
				NameAttribute: ansi.StylePrimitive{
					Color: stringPtr("#7A7AE6"),
				},
				NameClass: ansi.StylePrimitive{
					Color:     stringPtr("#F1F1F1"),
					Underline: boolPtr(true),
					Bold:      boolPtr(true),
				},
				NameDecorator: ansi.StylePrimitive{
					Color: stringPtr("#FFFF87"),
				},
				NameFunction: ansi.StylePrimitive{
					Color: stringPtr("#73D216"),
				},
				LiteralNumber: ansi.StylePrimitive{
					Color: stringPtr("#6EEFC0"),
				},
				LiteralString: ansi.StylePrimitive{
					Color: stringPtr("#C69669"),
				},
				LiteralStringEscape: ansi.StylePrimitive{
					Color: stringPtr("#AFFFD7"),
				},
				GenericDeleted: ansi.StylePrimitive{
					Color: stringPtr("#EF2929"),
				},
				GenericEmph: ansi.StylePrimitive{
					Italic: boolPtr(true),
				},
				GenericInserted: ansi.StylePrimitive{
					Color: stringPtr("#73D216"),
				},
				GenericStrong: ansi.StylePrimitive{
					Bold: boolPtr(true),
				},
				GenericSubheading: ansi.StylePrimitive{
					Color: stringPtr("#7D7D7D"),
				},
				Background: ansi.StylePrimitive{
					BackgroundColor: stringPtr("#2A2A2A"),
				},
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{},
			},
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix: "\n  ",
		},
	}
}

func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }
func uintPtr(u uint) *uint       { return &u }

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
