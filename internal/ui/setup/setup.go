package setup

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/auth"
	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// step tracks which screen the setup wizard is on.
type step int

const (
	stepAuthMethod step = iota
	stepCredentials
	stepValidating
	stepSelectAccount
	stepDone
)

// credField tracks which credential input field is focused.
type credField int

const (
	fieldEmail credField = iota
	fieldAPIKey
	fieldAPIToken
	fieldAccountID
)

// Messages for async operations
type validateResultMsg struct {
	err      error
	accounts []api.Account
}

type oauthDoneMsg struct {
	err error
}

// Model is the setup wizard shown on first run.
type Model struct {
	step       step
	authChoice int // 0=API Key, 1=API Token, 2=OAuth
	authLabels []string

	// Credential inputs (simple string buffers)
	email     string
	apiKey    string
	apiToken  string
	accountID string
	credField credField

	// Account selection
	accounts   []api.Account
	accountIdx int

	// State
	err     error
	loading bool
	cfg     *config.Config
	done    bool

	width  int
	height int
}

// New creates a new setup wizard model.
func New(cfg *config.Config) Model {
	return Model{
		step:       stepAuthMethod,
		authLabels: []string{"API Key + Email", "API Token", "OAuth (Browser Login)"},
		cfg:        cfg,
		email:      cfg.Email,
		apiKey:     cfg.APIKey,
		apiToken:   cfg.APIToken,
		accountID:  cfg.AccountID,
	}
}

// Done returns true when setup is complete.
func (m Model) Done() bool {
	return m.done
}

// Config returns the configured config.
func (m Model) Config() *config.Config {
	return m.cfg
}

// SetSize updates dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Init returns the initial command.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case validateResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.step = stepCredentials
			return m, nil
		}
		m.accounts = msg.accounts
		if len(m.accounts) == 0 {
			m.err = fmt.Errorf("no accounts found for this user")
			m.step = stepCredentials
			return m, nil
		}
		// If we already have a matching account ID, skip selection
		if m.accountID != "" {
			for _, acc := range m.accounts {
				if acc.ID == m.accountID {
					m.finalize(acc)
					return m, nil
				}
			}
		}
		// If only one account, auto-select
		if len(m.accounts) == 1 {
			m.finalize(m.accounts[0])
			return m, nil
		}
		m.step = stepSelectAccount
		m.accountIdx = 0
		return m, nil

	case oauthDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.step = stepAuthMethod
			return m, nil
		}
		// OAuth tokens are now stored in config, proceed to validate
		return m, m.validateCmd()

	case tea.KeyMsg:
		// Clear error on any keypress
		m.err = nil
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	}

	switch m.step {
	case stepAuthMethod:
		return m.handleAuthMethodKeys(msg)
	case stepCredentials:
		return m.handleCredentialKeys(msg)
	case stepSelectAccount:
		return m.handleAccountKeys(msg)
	}

	return m, nil
}

func (m Model) handleAuthMethodKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.authChoice > 0 {
			m.authChoice--
		}
	case "down", "j":
		if m.authChoice < len(m.authLabels)-1 {
			m.authChoice++
		}
	case "enter":
		switch m.authChoice {
		case 0: // API Key
			m.cfg.AuthMethod = config.AuthMethodAPIKey
			m.step = stepCredentials
			m.credField = fieldEmail
		case 1: // API Token
			m.cfg.AuthMethod = config.AuthMethodAPIToken
			m.step = stepCredentials
			m.credField = fieldAPIToken
		case 2: // OAuth
			m.cfg.AuthMethod = config.AuthMethodOAuth
			m.loading = true
			m.step = stepValidating
			return m, m.oauthLoginCmd()
		}
	}
	return m, nil
}

func (m Model) handleCredentialKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "tab", "down":
		m.advanceField()
		return m, nil
	case "shift+tab", "up":
		m.retreatField()
		return m, nil
	case "enter":
		if m.isCredentialsComplete() {
			m.loading = true
			m.step = stepValidating
			return m, m.validateCmd()
		}
		m.advanceField()
		return m, nil
	case "esc":
		m.step = stepAuthMethod
		return m, nil
	case "backspace":
		m.deleteChar()
		return m, nil
	default:
		// Append printable characters
		if len(key) == 1 {
			m.appendChar(key)
		}
	}

	return m, nil
}

func (m Model) handleAccountKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.accountIdx > 0 {
			m.accountIdx--
		}
	case "down", "j":
		if m.accountIdx < len(m.accounts)-1 {
			m.accountIdx++
		}
	case "enter":
		m.finalize(m.accounts[m.accountIdx])
	case "esc":
		m.step = stepCredentials
	}
	return m, nil
}

func (m *Model) advanceField() {
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		switch m.credField {
		case fieldEmail:
			m.credField = fieldAPIKey
		case fieldAPIKey:
			m.credField = fieldAccountID
		}
	case config.AuthMethodAPIToken:
		if m.credField == fieldAPIToken {
			m.credField = fieldAccountID
		}
	}
}

func (m *Model) retreatField() {
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		switch m.credField {
		case fieldAPIKey:
			m.credField = fieldEmail
		case fieldAccountID:
			m.credField = fieldAPIKey
		}
	case config.AuthMethodAPIToken:
		if m.credField == fieldAccountID {
			m.credField = fieldAPIToken
		}
	}
}

func (m *Model) appendChar(ch string) {
	switch m.credField {
	case fieldEmail:
		m.email += ch
	case fieldAPIKey:
		m.apiKey += ch
	case fieldAPIToken:
		m.apiToken += ch
	case fieldAccountID:
		m.accountID += ch
	}
}

func (m *Model) deleteChar() {
	switch m.credField {
	case fieldEmail:
		if len(m.email) > 0 {
			m.email = m.email[:len(m.email)-1]
		}
	case fieldAPIKey:
		if len(m.apiKey) > 0 {
			m.apiKey = m.apiKey[:len(m.apiKey)-1]
		}
	case fieldAPIToken:
		if len(m.apiToken) > 0 {
			m.apiToken = m.apiToken[:len(m.apiToken)-1]
		}
	case fieldAccountID:
		if len(m.accountID) > 0 {
			m.accountID = m.accountID[:len(m.accountID)-1]
		}
	}
}

func (m Model) isCredentialsComplete() bool {
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		return m.email != "" && m.apiKey != ""
	case config.AuthMethodAPIToken:
		return m.apiToken != ""
	}
	return false
}

func (m *Model) finalize(acc api.Account) {
	m.cfg.AccountID = acc.ID
	m.cfg.Email = m.email
	m.cfg.APIKey = m.apiKey
	m.cfg.APIToken = m.apiToken

	// Save config to disk
	_ = m.cfg.Save()

	m.step = stepDone
	m.done = true
}

// validateCmd creates the authenticator and validates credentials + fetches accounts.
func (m Model) validateCmd() tea.Cmd {
	return func() tea.Msg {
		// Apply credential inputs to config
		cfg := m.cfg
		cfg.Email = m.email
		cfg.APIKey = m.apiKey
		cfg.APIToken = m.apiToken
		cfg.AccountID = m.accountID

		authenticator, err := auth.New(cfg)
		if err != nil {
			return validateResultMsg{err: err}
		}

		ctx := context.Background()
		if err := authenticator.Validate(ctx); err != nil {
			return validateResultMsg{err: err}
		}

		client, err := api.NewClient(authenticator, cfg)
		if err != nil {
			return validateResultMsg{err: err}
		}

		accounts, err := client.ListAccounts(ctx)
		if err != nil {
			return validateResultMsg{err: err}
		}

		return validateResultMsg{accounts: accounts}
	}
}

// oauthLoginCmd starts the OAuth browser login flow.
func (m Model) oauthLoginCmd() tea.Cmd {
	return func() tea.Msg {
		oauthAuth := auth.NewOAuthAuth(m.cfg)
		ctx := context.Background()
		err := oauthAuth.Login(ctx)
		return oauthDoneMsg{err: err}
	}
}

// View renders the setup wizard.
func (m Model) View() string {
	var content string

	switch m.step {
	case stepAuthMethod:
		content = m.viewAuthMethod()
	case stepCredentials:
		content = m.viewCredentials()
	case stepValidating:
		content = m.viewValidating()
	case stepSelectAccount:
		content = m.viewSelectAccount()
	case stepDone:
		content = m.viewDone()
	}

	// Center the content
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		content)
}

func (m Model) viewAuthMethod() string {
	s := theme.TitleStyle.Render("orangeshell Setup") + "\n\n"
	s += theme.DimStyle.Render("Select authentication method:") + "\n\n"

	for i, label := range m.authLabels {
		cursor := "  "
		style := theme.NormalItemStyle
		if i == m.authChoice {
			cursor = theme.SelectedItemStyle.Render("> ")
			style = theme.SelectedItemStyle
		}
		s += cursor + style.Render(label) + "\n"
	}

	s += "\n" + theme.DimStyle.Render("up/down select  |  enter confirm  |  q quit")

	if m.err != nil {
		s += "\n\n" + theme.ErrorStyle.Render(m.err.Error())
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 3).
		Render(s)
}

func (m Model) viewCredentials() string {
	s := theme.TitleStyle.Render("Enter Credentials") + "\n\n"

	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		s += m.renderField("Email", m.email, m.credField == fieldEmail) + "\n"
		s += m.renderField("API Key", m.apiKey, m.credField == fieldAPIKey) + "\n"
		s += m.renderField("Account ID", m.accountID, m.credField == fieldAccountID) + "\n"
		s += "\n" + theme.DimStyle.Render("Account ID is optional — you can select from a list")
	case config.AuthMethodAPIToken:
		s += m.renderField("API Token", m.apiToken, m.credField == fieldAPIToken) + "\n"
		s += m.renderField("Account ID", m.accountID, m.credField == fieldAccountID) + "\n"
		s += "\n" + theme.DimStyle.Render("Account ID is optional — you can select from a list")
	}

	s += "\n\n" + theme.DimStyle.Render("tab/down next field, enter submit, esc back")

	if m.err != nil {
		s += "\n\n" + theme.ErrorStyle.Render(m.err.Error())
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 3).
		Width(60).
		Render(s)
}

func (m Model) renderField(label, value string, focused bool) string {
	labelStr := theme.LabelStyle.Render(fmt.Sprintf("  %s: ", label))

	inputStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
	indicator := " "
	if focused {
		inputStyle = inputStyle.Underline(true)
		indicator = theme.SelectedItemStyle.Render(">")
	}

	displayValue := value
	// Mask sensitive fields
	if (label == "API Key" || label == "API Token") && len(value) > 4 {
		displayValue = maskString(value)
	}
	if displayValue == "" {
		displayValue = theme.DimStyle.Render("(empty)")
	} else {
		displayValue = inputStyle.Render(displayValue)
	}

	cursor := ""
	if focused {
		cursor = theme.SelectedItemStyle.Render("_")
	}

	return fmt.Sprintf("%s%s%s%s", indicator, labelStr, displayValue, cursor)
}

func (m Model) viewValidating() string {
	s := theme.TitleStyle.Render("orangeshell") + "\n\n"
	if m.cfg.AuthMethod == config.AuthMethodOAuth {
		s += theme.DimStyle.Render("Opening browser for authentication...\n")
		s += theme.DimStyle.Render("Waiting for authorization callback...")
	} else {
		s += theme.DimStyle.Render("Validating credentials...")
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 3).
		Render(s)
}

func (m Model) viewSelectAccount() string {
	s := theme.TitleStyle.Render("Select Account") + "\n\n"

	for i, acc := range m.accounts {
		cursor := "  "
		style := theme.NormalItemStyle
		if i == m.accountIdx {
			cursor = theme.SelectedItemStyle.Render("> ")
			style = theme.SelectedItemStyle
		}
		s += fmt.Sprintf("%s%s %s\n", cursor,
			style.Render(acc.Name),
			theme.DimStyle.Render(fmt.Sprintf("(%s)", acc.ID)))
	}

	s += "\n" + theme.DimStyle.Render("up/down to select, enter to confirm")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 3).
		Render(s)
}

func (m Model) viewDone() string {
	s := theme.SuccessStyle.Render("Setup complete!") + "\n\n"
	s += theme.DimStyle.Render("Loading dashboard...")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorGreen).
		Padding(1, 3).
		Render(s)
}

func maskString(s string) string {
	if len(s) <= 4 {
		return s
	}
	masked := ""
	for i := 0; i < len(s)-4; i++ {
		masked += "*"
	}
	return masked + s[len(s)-4:]
}
