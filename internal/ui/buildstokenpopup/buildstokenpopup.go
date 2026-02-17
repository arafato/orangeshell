package buildstokenpopup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// zone ID for the clickable copy button
const copyZoneID = "builds-token-copy-cmd"

// --- Steps ---

type step int

const (
	stepInput      step = iota // Enter API token
	stepValidating             // Testing token against API
	stepResult                 // Show success or error
)

// --- Messages emitted by this component ---

// CloseMsg signals the popup should close without saving.
type CloseMsg struct{}

// TokenSavedMsg signals the popup completed successfully with a valid token.
type TokenSavedMsg struct {
	Token string
}

// CopyCommandMsg requests the app to copy the curl command to the clipboard.
type CopyCommandMsg struct {
	Text string
}

// validateTokenMsg is internal — carries the result of the async token validation.
type validateTokenMsg struct {
	token string
	err   error
}

// --- Model ---

// Model is the builds API token popup state.
type Model struct {
	step step

	accountID string // current Cloudflare account ID

	tokenInput textinput.Model
	inputErr   string // validation/API error message

	token string // successfully validated token
}

// New creates a new builds token popup.
func New(accountID string) Model {
	ti := textinput.New()
	ti.Placeholder = "paste your API token here"
	ti.CharLimit = 200
	ti.Width = 50
	ti.Prompt = "  "
	ti.PromptStyle = theme.SelectedItemStyle
	ti.TextStyle = theme.ValueStyle
	ti.PlaceholderStyle = theme.DimStyle
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	ti.Focus()

	return Model{
		step:       stepInput,
		accountID:  accountID,
		tokenInput: ti,
	}
}

// Init returns the initial command (cursor blink).
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// curlCommand builds the raw curl command string for clipboard copy.
func (m Model) curlCommand() string {
	return fmt.Sprintf(
		`curl -X POST https://api.cloudflare.com/client/v4/user/tokens `+
			`-H "X-Auth-Email: $CF_EMAIL" `+
			`-H "X-Auth-Key: $CF_API_KEY" `+
			`-H "Content-Type: application/json" `+
			`-d '{"name":"orangeshell-builds","policies":[{"effect":"allow","resources":{"com.cloudflare.api.account.%s":"*"},"permission_groups":[{"id":"ad99c5ae555e45c4bef5bdf2678388ba"}]}]}'`,
		m.accountID,
	)
}

// --- Update ---

// Update handles messages for the builds token popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case validateTokenMsg:
		return m.handleValidateResult(msg)
	case tea.KeyMsg:
		switch m.step {
		case stepInput:
			return m.updateInput(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	case tea.MouseMsg:
		if m.step == stepInput {
			if z := zone.Get(copyZoneID); z != nil && z.InBounds(msg) {
				cmd := m.curlCommand()
				return m, func() tea.Msg { return CopyCommandMsg{Text: cmd} }
			}
		}
	}

	// Forward non-key messages to text input (cursor blink, etc.)
	if m.step == stepInput {
		var cmd tea.Cmd
		m.tokenInput, cmd = m.tokenInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

// --- Input step ---

func (m Model) updateInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+p":
		return m, func() tea.Msg { return CloseMsg{} }
	case "enter":
		token := strings.TrimSpace(m.tokenInput.Value())
		if token == "" {
			m.inputErr = "Token cannot be empty"
			return m, nil
		}

		// Transition to validating
		m.step = stepValidating
		m.inputErr = ""
		accountID := m.accountID

		return m, func() tea.Msg {
			client := api.NewBuildsClient(accountID, "", "", token)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err := client.VerifyAuth(ctx)
			return validateTokenMsg{token: token, err: err}
		}
	}

	// Forward to text input
	var cmd tea.Cmd
	m.tokenInput, cmd = m.tokenInput.Update(msg)
	m.inputErr = ""
	return m, cmd
}

func (m Model) handleValidateResult(msg validateTokenMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		// Token didn't work — go back to input with error
		m.step = stepInput
		m.inputErr = "Token authentication failed. Ensure it has 'Workers CI Read' permission."
		return m, nil
	}

	// Token works!
	m.step = stepResult
	m.token = msg.token
	return m, nil
}

// --- Result step ---

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "ctrl+p":
		return m, func() tea.Msg { return TokenSavedMsg{Token: m.token} }
	}
	return m, nil
}

// --- View ---

// View renders the builds token popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := 76
	if termWidth < 84 {
		popupWidth = termWidth - 8
	}
	if popupWidth < 60 {
		popupWidth = 60
	}

	titleLine := theme.TitleStyle.Render("  Builds API Token Required")
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepInput:
		body = m.viewInput()
		help = "  esc cancel  |  enter validate & save"
	case stepValidating:
		body = m.viewValidating()
		help = ""
	case stepResult:
		body = m.viewResult()
		help = "  enter close"
	}

	helpLine := theme.DimStyle.Render(help)

	var lines []string
	lines = append(lines, titleLine, sep)
	lines = append(lines, body)
	if help != "" {
		lines = append(lines, sep, helpLine)
	}

	content := strings.Join(lines, "\n")

	popup := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	return popup
}

// --- Sub-views ---

func (m Model) viewInput() string {
	var lines []string

	lines = append(lines, theme.DimStyle.Render("  The Workers Builds API requires a scoped API Token"))
	lines = append(lines, theme.DimStyle.Render("  with 'Workers CI Read' permission. This permission"))
	lines = append(lines, theme.DimStyle.Render("  is not available in the dashboard UI."))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Run this curl command to create the token:"))
	lines = append(lines, "")

	// Render the curl command with syntax highlighting
	cmdStyle := lipgloss.NewStyle().Foreground(theme.ColorYellow)
	lines = append(lines, cmdStyle.Render(
		"  curl -X POST \\"))
	lines = append(lines, cmdStyle.Render(
		"    https://api.cloudflare.com/client/v4/user/tokens \\"))
	lines = append(lines, cmdStyle.Render(
		"    -H \"X-Auth-Email: $CF_EMAIL\" \\"))
	lines = append(lines, cmdStyle.Render(
		"    -H \"X-Auth-Key: $CF_API_KEY\" \\"))
	lines = append(lines, cmdStyle.Render(
		"    -H \"Content-Type: application/json\" \\"))
	lines = append(lines, cmdStyle.Render(
		"    -d '{\"name\":\"orangeshell-builds\",\"policies\":[{\"effect\":"))
	lines = append(lines, cmdStyle.Render(fmt.Sprintf(
		"    \"allow\",\"resources\":{\"com.cloudflare.api.account.%s\":", m.accountID)))
	lines = append(lines, cmdStyle.Render(
		"    \"*\"},\"permission_groups\":[{\"id\":"))
	lines = append(lines, cmdStyle.Render(
		"    \"ad99c5ae555e45c4bef5bdf2678388ba\"}]}]}'"))

	lines = append(lines, "")

	// Clickable copy button
	copyBtn := lipgloss.NewStyle().
		Background(theme.ColorOrange).
		Foreground(theme.ColorBg).
		Bold(true).
		Padding(0, 1).
		Render(" Copy ")
	lines = append(lines, "  "+zone.Mark(copyZoneID, copyBtn))

	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Copy the 'value' field from the response and paste below."))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  API Token:"))
	lines = append(lines, m.tokenInput.View())

	if m.inputErr != "" {
		lines = append(lines, "")
		lines = append(lines, theme.ErrorStyle.Render("  "+m.inputErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewValidating() string {
	return theme.DimStyle.Render("  Validating token...")
}

func (m Model) viewResult() string {
	var lines []string
	lines = append(lines, theme.SuccessStyle.Render("  Token validated and saved."))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Build history and git metadata will now"))
	lines = append(lines, theme.DimStyle.Render("  appear in the version history table."))
	return strings.Join(lines, "\n")
}
