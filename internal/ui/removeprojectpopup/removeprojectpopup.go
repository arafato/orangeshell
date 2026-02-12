package removeprojectpopup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Steps ---

type step int

const (
	stepConfirm  step = iota // Confirm removal (yes/no)
	stepDeleting             // Removal in progress (spinner)
	stepResult               // Show success/error
)

// --- Messages emitted by this component ---

// RemoveProjectMsg requests the app to delete the project directory.
type RemoveProjectMsg struct {
	DirPath string
}

// RemoveProjectDoneMsg delivers the result of the removal attempt.
type RemoveProjectDoneMsg struct {
	Err error
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals the project was removed successfully.
// The app should rescan the project list.
type DoneMsg struct{}

// --- Model ---

// Model is the remove-project confirmation popup state.
type Model struct {
	step step

	projectName string // display name of the project
	relPath     string // relative path (shown in warning)
	dirPath     string // absolute path to delete

	confirmCursor int // 0 = No (default safe), 1 = Yes

	resultMsg   string
	resultIsErr bool

	spinner spinner.Model
}

// --- Constructor ---

// New creates a remove-project confirmation popup.
func New(projectName, relPath, dirPath string) Model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)

	return Model{
		step:        stepConfirm,
		projectName: projectName,
		relPath:     relPath,
		dirPath:     dirPath,
		spinner:     s,
	}
}

// --- Accessors ---

// NeedsSpinner returns true if the popup has an active spinner animation.
func (m Model) NeedsSpinner() bool {
	return m.step == stepDeleting
}

// SpinnerTick returns the command to start the spinner ticking.
func (m Model) SpinnerTick() tea.Cmd {
	return m.spinner.Tick
}

// --- Update ---

// Update handles messages for the remove-project popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case RemoveProjectDoneMsg:
		return m.handleDone(msg)
	case spinner.TickMsg:
		if m.step == stepDeleting {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		switch m.step {
		case stepConfirm:
			return m.updateConfirm(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return CloseMsg{} }
	case "left", "h":
		if m.confirmCursor > 0 {
			m.confirmCursor--
		}
	case "right", "l":
		if m.confirmCursor < 1 {
			m.confirmCursor++
		}
	case "enter":
		if m.confirmCursor == 0 {
			// "No" selected — close
			return m, func() tea.Msg { return CloseMsg{} }
		}
		// "Yes" selected — proceed with removal
		m.step = stepDeleting
		dirPath := m.dirPath
		return m, tea.Batch(
			m.spinner.Tick,
			func() tea.Msg {
				return RemoveProjectMsg{DirPath: dirPath}
			},
		)
	}
	return m, nil
}

func (m Model) handleDone(msg RemoveProjectDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if msg.Err != nil {
		m.resultMsg = fmt.Sprintf("Failed to remove project: %v", msg.Err)
		m.resultIsErr = true
		return m, nil
	}
	m.resultMsg = fmt.Sprintf("Project %q removed", m.projectName)
	m.resultIsErr = false
	return m, nil
}

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter":
		if m.resultIsErr {
			return m, func() tea.Msg { return CloseMsg{} }
		}
		return m, func() tea.Msg { return DoneMsg{} }
	}
	return m, nil
}

// --- View ---

// View renders the popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 70 {
		popupWidth = 70
	}

	titleLine := theme.TitleStyle.Render("  Remove Project")
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepConfirm:
		body = m.viewConfirm()
		help = "  esc close  |  enter confirm  |  h/l select"
	case stepDeleting:
		body = fmt.Sprintf("  %s %s", m.spinner.View(),
			theme.DimStyle.Render(fmt.Sprintf("Removing %q...", m.projectName)))
		help = ""
	case stepResult:
		body = m.viewResult()
		help = "  esc close  |  enter close"
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

func (m Model) viewConfirm() string {
	var lines []string

	lines = append(lines,
		theme.DimStyle.Render(fmt.Sprintf("  Remove project %q?", m.projectName)))
	lines = append(lines, "")
	lines = append(lines,
		theme.DimStyle.Render("  This will permanently delete the directory:"))
	lines = append(lines,
		lipgloss.NewStyle().Foreground(theme.ColorWhite).Render(
			fmt.Sprintf("    %s/", m.relPath)))
	lines = append(lines, "")

	warningStyle := lipgloss.NewStyle().Foreground(theme.ColorRed)
	lines = append(lines,
		warningStyle.Render("  This action cannot be undone."))
	lines = append(lines, "")
	lines = append(lines,
		theme.DimStyle.Render("  Workers deployed from this project will NOT"))
	lines = append(lines,
		theme.DimStyle.Render("  be affected on Cloudflare."))
	lines = append(lines, "")

	// Buttons
	noStyle := theme.DimStyle
	yesStyle := theme.DimStyle
	if m.confirmCursor == 0 {
		noStyle = lipgloss.NewStyle().
			Background(theme.ColorDarkGray).
			Foreground(theme.ColorWhite).
			Padding(0, 2)
	} else {
		yesStyle = lipgloss.NewStyle().
			Background(theme.ColorRed).
			Foreground(theme.ColorWhite).
			Padding(0, 2)
	}

	noBtn := noStyle.Render("No")
	yesBtn := yesStyle.Render("Yes")
	lines = append(lines, fmt.Sprintf("  %s   %s", noBtn, yesBtn))

	return strings.Join(lines, "\n")
}

func (m Model) viewResult() string {
	if m.resultIsErr {
		var lines []string
		for _, line := range strings.Split(m.resultMsg, "\n") {
			lines = append(lines, theme.ErrorStyle.Render("  "+line))
		}
		return strings.Join(lines, "\n")
	}
	return theme.SuccessStyle.Render("  " + m.resultMsg)
}
