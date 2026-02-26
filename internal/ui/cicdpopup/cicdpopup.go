package cicdpopup

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Steps ---

type step int

const (
	stepDetecting    step = iota // Auto-detecting git repo
	stepNoGit                    // No git repo found — error state
	stepNoInstall                // GitHub/GitLab installation not found
	stepCheckInstall             // Checking if installation exists (via config_autofill)
	stepConfigure                // Form: branch, build cmd, deploy cmd, root dir, watch paths
	stepReview                   // Summary before applying
	stepApplying                 // Spinner — creating connection + trigger
	stepResult                   // Success or error
)

// --- Form fields ---

type field int

const (
	fieldBranch field = iota
	fieldBuildCmd
	fieldDeployCmd
	fieldRootDir
	fieldPathIncludes
	fieldPathExcludes
	fieldBuildCaching
	fieldCount // sentinel — total number of fields
)

// --- Messages emitted by this component ---

// CheckInstallMsg requests the app to call GetConfigAutofill to verify the
// GitHub/GitLab installation and fetch auto-detected config.
type CheckInstallMsg struct {
	Provider          string // "github" or "gitlab"
	ProviderAccountID string // owner
	RepoID            string // repo name
	Branch            string
	RootDir           string
}

// CheckInstallDoneMsg delivers the result of the installation check.
type CheckInstallDoneMsg struct {
	Autofill *api.ConfigAutofill
	Err      error
}

// SetupCICDMsg requests the app to create the repo connection, build token, and trigger.
type SetupCICDMsg struct {
	Provider          string
	ProviderAccountID string // owner
	RepoName          string
	ScriptName        string
	TriggerName       string
	BranchIncludes    []string
	BranchExcludes    []string
	PathIncludes      []string
	PathExcludes      []string
	BuildCommand      string
	DeployCommand     string
	RootDirectory     string
	BuildCaching      bool
}

// SetupCICDDoneMsg delivers the result of the CI/CD setup.
type SetupCICDDoneMsg struct {
	Trigger *api.Trigger
	Err     error
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals CI/CD was set up successfully. The app should refresh build data.
type DoneMsg struct {
	ScriptName string
}

// --- Model ---

// Model is the CI/CD setup wizard popup state.
type Model struct {
	step step

	// Git info (detected from the project directory)
	gitInfo    *wcfg.GitInfo
	scriptName string // worker script name
	envName    string // wrangler environment name

	// Form field values
	fields     [fieldCount]string
	fieldFocus field
	fieldErr   string // validation error for current field

	// Build caching toggle
	buildCaching bool

	// Auto-detected config (from config_autofill API)
	autofill *api.ConfigAutofill

	// Dashboard URL for missing installation
	dashboardURL string

	// Spinner (checking/applying steps)
	spinner spinner.Model

	// Result
	resultMsg     string
	resultIsErr   bool
	resultTrigger *api.Trigger
}

// New creates a new CI/CD setup wizard popup.
// gitInfo comes from the project's local git detection.
// scriptName is the resolved worker name for the current environment.
// envName is the wrangler environment name (or "default").
// projectDir is the absolute path to the project directory.
// repoRoot is the git repo root (may differ from projectDir in monorepos).
func New(gitInfo *wcfg.GitInfo, scriptName, envName, projectDir string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)

	// Calculate root directory relative to git repo root
	rootDir := "."
	if gitInfo != nil && gitInfo.IsRepo && gitInfo.RepoRoot != "" {
		rel, err := filepath.Rel(gitInfo.RepoRoot, projectDir)
		if err == nil && rel != "." {
			rootDir = rel
		}
	}

	m := Model{
		scriptName:   scriptName,
		envName:      envName,
		gitInfo:      gitInfo,
		spinner:      s,
		buildCaching: true,
	}

	// Check if git info is valid
	if gitInfo == nil || !gitInfo.IsRepo {
		m.step = stepNoGit
		return m
	}

	if gitInfo.ProviderType == "" || gitInfo.Owner == "" || gitInfo.RepoName == "" {
		m.step = stepNoGit
		m.resultMsg = "Could not detect GitHub or GitLab remote from git config"
		return m
	}

	// Pre-fill form fields from git info
	m.fields[fieldBranch] = gitInfo.Branch
	m.fields[fieldBuildCmd] = ""
	m.fields[fieldDeployCmd] = "npx wrangler deploy"
	m.fields[fieldRootDir] = rootDir
	m.fields[fieldPathIncludes] = "*"
	m.fields[fieldPathExcludes] = ""

	// Start with auto-detection: check installation via config_autofill
	m.step = stepCheckInstall

	return m
}

// Init returns the initial command (spinner tick + installation check request).
func (m Model) Init() tea.Cmd {
	if m.step == stepCheckInstall {
		return tea.Batch(
			m.spinner.Tick,
			func() tea.Msg {
				return CheckInstallMsg{
					Provider:          m.gitInfo.ProviderType,
					ProviderAccountID: m.gitInfo.Owner,
					RepoID:            m.gitInfo.RepoName,
					Branch:            m.gitInfo.Branch,
					RootDir:           m.fields[fieldRootDir],
				}
			},
		)
	}
	return nil
}

// IsWorking returns true when the popup needs spinner ticks.
func (m Model) IsWorking() bool {
	return m.step == stepCheckInstall || m.step == stepApplying
}

// Update handles messages for the CI/CD popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case CheckInstallDoneMsg:
		return m.handleCheckInstallDone(msg)
	case SetupCICDDoneMsg:
		return m.handleSetupDone(msg)
	case spinner.TickMsg:
		if m.IsWorking() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleCheckInstallDone(msg CheckInstallDoneMsg) (Model, tea.Cmd) {
	if msg.Err != nil {
		if api.IsAuthError(msg.Err) {
			// Auth error — the token lacks CI Read/Write scope.
			// The app layer should handle re-provisioning before we get here,
			// but if it happens, show an error.
			m.step = stepResult
			m.resultIsErr = true
			m.resultMsg = fmt.Sprintf("Authentication error: %v\nYour token may lack Workers CI permissions.", msg.Err)
			return m, nil
		}
		// Non-auth error — likely the GitHub/GitLab installation doesn't exist
		m.step = stepNoInstall
		accountID := "" // app layer will fill the dashboard URL
		m.dashboardURL = fmt.Sprintf("https://dash.cloudflare.com/%s/workers/builds", accountID)
		return m, nil
	}

	// Installation exists �� pre-fill form from autofill data
	m.autofill = msg.Autofill
	if msg.Autofill != nil {
		if buildCmd, ok := msg.Autofill.Scripts["build"]; ok && buildCmd != "" {
			m.fields[fieldBuildCmd] = buildCmd
		}
	}

	m.step = stepConfigure
	m.fieldFocus = fieldBranch
	return m, nil
}

func (m Model) handleSetupDone(msg SetupCICDDoneMsg) (Model, tea.Cmd) {
	if msg.Err != nil {
		m.step = stepResult
		m.resultIsErr = true
		m.resultMsg = fmt.Sprintf("CI/CD setup failed: %v", msg.Err)
		return m, nil
	}
	m.step = stepResult
	m.resultIsErr = false
	m.resultTrigger = msg.Trigger
	m.resultMsg = fmt.Sprintf("CI/CD connected! Pushes to %s will trigger builds.",
		strings.Join(msg.Trigger.BranchIncludes, ", "))
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.step {
	case stepNoGit:
		return m.handleNoGitKeys(msg)
	case stepNoInstall:
		return m.handleNoInstallKeys(msg)
	case stepConfigure:
		return m.handleConfigureKeys(msg)
	case stepReview:
		return m.handleReviewKeys(msg)
	case stepResult:
		return m.handleResultKeys(msg)
	}
	return m, nil
}

func (m Model) handleNoGitKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter":
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

func (m Model) handleNoInstallKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return CloseMsg{} }
	case "enter":
		// Open dashboard URL in browser (the app layer handles this via OpenBrowserMsg)
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

func (m Model) handleConfigureKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	// Handle paste
	if msg.Paste {
		text := string(msg.Runes)
		m.appendToField(text)
		return m, nil
	}

	switch key {
	case "esc":
		return m, func() tea.Msg { return CloseMsg{} }
	case "tab", "down":
		m.fieldErr = ""
		m.fieldFocus++
		if m.fieldFocus >= fieldCount {
			m.fieldFocus = 0
		}
		return m, nil
	case "shift+tab", "up":
		m.fieldErr = ""
		m.fieldFocus--
		if m.fieldFocus < 0 {
			m.fieldFocus = fieldCount - 1
		}
		return m, nil
	case "enter":
		// On the build caching field, toggle instead of advancing
		if m.fieldFocus == fieldBuildCaching {
			m.buildCaching = !m.buildCaching
			return m, nil
		}
		// Validate and advance to review
		if err := m.validate(); err != "" {
			m.fieldErr = err
			return m, nil
		}
		m.step = stepReview
		return m, nil
	case " ":
		// Space toggles build caching when focused
		if m.fieldFocus == fieldBuildCaching {
			m.buildCaching = !m.buildCaching
			return m, nil
		}
		m.appendToField(" ")
		return m, nil
	case "backspace":
		m.deleteFromField()
		return m, nil
	default:
		// Character input — only for text fields
		if m.fieldFocus != fieldBuildCaching && len(msg.Runes) == 1 {
			m.appendToField(string(msg.Runes))
		}
	}
	return m, nil
}

func (m Model) handleReviewKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.step = stepConfigure
		return m, nil
	case "enter":
		// Start applying
		m.step = stepApplying
		return m, tea.Batch(
			m.spinner.Tick,
			func() tea.Msg {
				return SetupCICDMsg{
					Provider:          m.gitInfo.ProviderType,
					ProviderAccountID: m.gitInfo.Owner,
					RepoName:          m.gitInfo.RepoName,
					ScriptName:        m.scriptName,
					TriggerName:       fmt.Sprintf("%s deploy", m.scriptName),
					BranchIncludes:    splitCommaSeparated(m.fields[fieldBranch]),
					BranchExcludes:    nil,
					PathIncludes:      splitCommaSeparated(m.fields[fieldPathIncludes]),
					PathExcludes:      splitCommaSeparated(m.fields[fieldPathExcludes]),
					BuildCommand:      m.fields[fieldBuildCmd],
					DeployCommand:     m.fields[fieldDeployCmd],
					RootDirectory:     m.fields[fieldRootDir],
					BuildCaching:      m.buildCaching,
				}
			},
		)
	}
	return m, nil
}

func (m Model) handleResultKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter":
		if m.resultIsErr {
			return m, func() tea.Msg { return CloseMsg{} }
		}
		return m, func() tea.Msg { return DoneMsg{ScriptName: m.scriptName} }
	}
	return m, nil
}

// --- Field manipulation (pointer receivers) ---

func (m *Model) appendToField(s string) {
	if m.fieldFocus >= 0 && int(m.fieldFocus) < len(m.fields) && m.fieldFocus != fieldBuildCaching {
		m.fields[m.fieldFocus] += s
		m.fieldErr = ""
	}
}

func (m *Model) deleteFromField() {
	if m.fieldFocus >= 0 && int(m.fieldFocus) < len(m.fields) && m.fieldFocus != fieldBuildCaching {
		f := m.fields[m.fieldFocus]
		if len(f) > 0 {
			m.fields[m.fieldFocus] = f[:len(f)-1]
			m.fieldErr = ""
		}
	}
}

func (m Model) validate() string {
	if strings.TrimSpace(m.fields[fieldBranch]) == "" {
		return "Branch is required"
	}
	if strings.TrimSpace(m.fields[fieldDeployCmd]) == "" {
		return "Deploy command is required"
	}
	return ""
}

// SetDashboardURL sets the dashboard URL for the no-installation error state.
func (m *Model) SetDashboardURL(url string) {
	m.dashboardURL = url
}

// --- View ---

// View renders the CI/CD popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 60 {
		popupWidth = 60
	}
	if popupWidth > 90 {
		popupWidth = 90
	}
	contentWidth := popupWidth - 6 // padding + borders

	var content string
	switch m.step {
	case stepNoGit:
		content = m.viewNoGit(contentWidth)
	case stepCheckInstall:
		content = m.viewCheckInstall(contentWidth)
	case stepNoInstall:
		content = m.viewNoInstall(contentWidth)
	case stepConfigure:
		content = m.viewConfigure(contentWidth)
	case stepReview:
		content = m.viewReview(contentWidth)
	case stepApplying:
		content = m.viewApplying(contentWidth)
	case stepResult:
		content = m.viewResult(contentWidth)
	default:
		content = ""
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	return box
}

func (m Model) viewNoGit(w int) string {
	title := theme.TitleStyle.Render("Setup CI/CD")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))

	msg := m.resultMsg
	if msg == "" {
		msg = "No Git repository found."
	}

	body := theme.ErrorStyle.Render(msg)

	explanation := theme.SubtitleStyle.Render(
		"Workers Builds CI/CD requires a Git repository with a\n" +
			"GitHub or GitLab remote to trigger automated deployments.\n\n" +
			"To get started:\n\n" +
			"  1. Initialize a git repository:  " + theme.ValueStyle.Render("git init") + "\n" +
			"  2. Add a remote:                 " + theme.ValueStyle.Render("git remote add origin <url>") + "\n" +
			"  3. Push your code:               " + theme.ValueStyle.Render("git push -u origin main") + "\n" +
			"  4. Run this wizard again")

	help := theme.DimStyle.Render("esc close")

	return lipgloss.JoinVertical(lipgloss.Left, title, sep, "", body, "", explanation, "", help)
}

func (m Model) viewCheckInstall(w int) string {
	title := theme.TitleStyle.Render("Setup CI/CD")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))
	body := fmt.Sprintf("%s Checking %s/%s/%s...",
		m.spinner.View(),
		m.gitInfo.ProviderType, m.gitInfo.Owner, m.gitInfo.RepoName)

	return lipgloss.JoinVertical(lipgloss.Left, title, sep, "", body)
}

func (m Model) viewNoInstall(w int) string {
	title := theme.TitleStyle.Render("Setup CI/CD")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))

	providerLabel := "GitHub"
	installPage := "github.com/settings/installations"
	if m.gitInfo.ProviderType == "gitlab" {
		providerLabel = "GitLab"
		installPage = "gitlab.com/-/profile/applications"
	}

	header := theme.ErrorStyle.Render(
		fmt.Sprintf("%s integration not found for this Cloudflare account.", providerLabel))

	explanation := theme.SubtitleStyle.Render(
		fmt.Sprintf("Before connecting a repository, you need to authorize\n"+
			"Cloudflare to access your %s account. This is a one-time\n"+
			"setup done through the Cloudflare dashboard.\n\n"+
			"How to set it up:\n\n"+
			"  1. Go to the Cloudflare dashboard:\n"+
			"     %s\n\n"+
			"  2. Navigate to Workers & Pages > Overview > your worker\n"+
			"  3. Go to Settings > Builds > Connect to Git\n"+
			"  4. Authorize the Cloudflare %s App\n"+
			"  5. Select which repositories to grant access to\n"+
			"  6. Return here and run this wizard again\n\n"+
			"You can verify the installation at:\n"+
			"  %s",
			providerLabel,
			theme.LabelStyle.Render(m.dashboardURL),
			providerLabel,
			theme.DimStyle.Render(installPage)))

	help := theme.DimStyle.Render("esc close")

	return lipgloss.JoinVertical(lipgloss.Left, title, sep, "", header, "", explanation, "", help)
}

func (m Model) viewConfigure(w int) string {
	title := theme.TitleStyle.Render("Setup CI/CD")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))

	repoInfo := theme.SubtitleStyle.Render(fmt.Sprintf(
		"%s/%s/%s → %s",
		m.gitInfo.ProviderType, m.gitInfo.Owner, m.gitInfo.RepoName,
		m.scriptName))

	// Render each field
	var fields []string
	fieldDefs := []struct {
		idx   field
		label string
		hint  string
	}{
		{fieldBranch, "Branch", "branch to trigger builds on (comma-separated for multiple)"},
		{fieldBuildCmd, "Build command", "e.g. npm run build (leave empty to skip)"},
		{fieldDeployCmd, "Deploy command", "e.g. npx wrangler deploy"},
		{fieldRootDir, "Root directory", "relative to repo root (. for root)"},
		{fieldPathIncludes, "Watch paths (include)", "file patterns to trigger builds (* for all)"},
		{fieldPathExcludes, "Watch paths (exclude)", "file patterns to skip (e.g. docs/*, *.md)"},
	}

	for _, fd := range fieldDefs {
		isFocused := m.fieldFocus == fd.idx
		fields = append(fields, m.renderField(fd.label, m.fields[fd.idx], fd.hint, isFocused, w))
	}

	// Build caching toggle
	cachingLabel := "Build caching"
	cachingValue := "enabled"
	if !m.buildCaching {
		cachingValue = "disabled"
	}
	isCachingFocused := m.fieldFocus == fieldBuildCaching
	fields = append(fields, m.renderToggle(cachingLabel, cachingValue, isCachingFocused))

	// Error message
	var errLine string
	if m.fieldErr != "" {
		errLine = "\n" + theme.ErrorStyle.Render(m.fieldErr)
	}

	help := theme.DimStyle.Render("tab/shift+tab navigate • enter submit • esc close")

	parts := []string{title, sep, repoInfo, ""}
	parts = append(parts, fields...)
	if errLine != "" {
		parts = append(parts, errLine)
	}
	parts = append(parts, "", help)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) viewReview(w int) string {
	title := theme.TitleStyle.Render("Review CI/CD Configuration")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))

	providerLabel := "GitHub"
	if m.gitInfo.ProviderType == "gitlab" {
		providerLabel = "GitLab"
	}

	lines := []string{
		fmt.Sprintf("  Repository    %s %s/%s", providerLabel, m.gitInfo.Owner, m.gitInfo.RepoName),
		fmt.Sprintf("  Worker        %s", m.scriptName),
		fmt.Sprintf("  Branch        %s", m.fields[fieldBranch]),
	}
	if m.fields[fieldBuildCmd] != "" {
		lines = append(lines, fmt.Sprintf("  Build cmd     %s", m.fields[fieldBuildCmd]))
	}
	lines = append(lines,
		fmt.Sprintf("  Deploy cmd    %s", m.fields[fieldDeployCmd]),
		fmt.Sprintf("  Root dir      %s", m.fields[fieldRootDir]),
	)
	if m.fields[fieldPathIncludes] != "" {
		lines = append(lines, fmt.Sprintf("  Watch incl    %s", m.fields[fieldPathIncludes]))
	}
	if m.fields[fieldPathExcludes] != "" {
		lines = append(lines, fmt.Sprintf("  Watch excl    %s", m.fields[fieldPathExcludes]))
	}
	cachingStr := "enabled"
	if !m.buildCaching {
		cachingStr = "disabled"
	}
	lines = append(lines, fmt.Sprintf("  Build cache   %s", cachingStr))

	body := strings.Join(lines, "\n")
	help := theme.DimStyle.Render("enter confirm • esc back")

	return lipgloss.JoinVertical(lipgloss.Left, title, sep, "", body, "", help)
}

func (m Model) viewApplying(w int) string {
	title := theme.TitleStyle.Render("Setup CI/CD")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))
	body := fmt.Sprintf("%s Setting up CI/CD for %s...", m.spinner.View(), m.scriptName)

	return lipgloss.JoinVertical(lipgloss.Left, title, sep, "", body)
}

func (m Model) viewResult(w int) string {
	title := theme.TitleStyle.Render("Setup CI/CD")
	sep := theme.DimStyle.Render(strings.Repeat("─", w))

	var body string
	if m.resultIsErr {
		body = theme.ErrorStyle.Render(m.resultMsg)
	} else {
		body = theme.SuccessStyle.Render(m.resultMsg)
	}

	help := theme.DimStyle.Render("enter/esc close")

	return lipgloss.JoinVertical(lipgloss.Left, title, sep, "", body, "", help)
}

// --- Field rendering helpers ---

func (m Model) renderField(label, value, hint string, focused bool, maxWidth int) string {
	labelStyle := theme.SubtitleStyle
	valueStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
	cursor := " "
	if focused {
		labelStyle = theme.LabelStyle
		cursor = "▏"
	}

	renderedLabel := labelStyle.Render(fmt.Sprintf("  %-22s", label))
	renderedValue := valueStyle.Render(value + cursor)

	line := renderedLabel + renderedValue
	if focused && hint != "" {
		line += "\n" + theme.DimStyle.Render("  "+hint)
	}
	return line
}

func (m Model) renderToggle(label, value string, focused bool) string {
	labelStyle := theme.SubtitleStyle
	if focused {
		labelStyle = theme.LabelStyle
	}

	valueStyle := theme.SuccessStyle
	if value == "disabled" {
		valueStyle = theme.DimStyle
	}

	renderedLabel := labelStyle.Render(fmt.Sprintf("  %-22s", label))
	renderedValue := valueStyle.Render(value)

	line := renderedLabel + renderedValue
	if focused {
		line += "\n" + theme.DimStyle.Render("  space/enter toggle")
	}
	return line
}

// --- Helpers ---

func splitCommaSeparated(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
