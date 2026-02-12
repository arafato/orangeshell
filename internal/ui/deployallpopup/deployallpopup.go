package deployallpopup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Messages emitted by this component (handled by app.go) ---

// CloseMsg signals the popup should close.
type CloseMsg struct{}

// DoneMsg signals all deploys finished; the app should refresh deployment data.
type DoneMsg struct{}

// CancelMsg signals the user confirmed cancellation; the app should kill all runners.
type CancelMsg struct{}

// --- Messages received from app.go ---

// ProjectDoneMsg delivers the result of a single project deploy.
type ProjectDoneMsg struct {
	Index   int    // index into items
	Err     error  // nil on success
	LogPath string // path to log file (set by app on failure)
}

// --- Data model ---

// DeployStatus tracks the state of one project's deploy.
type DeployStatus int

const (
	StatusPending DeployStatus = iota
	StatusDeploying
	StatusSuccess
	StatusFailed
	StatusCancelled
)

// DeployItem represents one project being deployed.
type DeployItem struct {
	ProjectName string
	ConfigPath  string
	EnvName     string
	Status      DeployStatus
	ErrSummary  string // short error message (first line)
	LogPath     string // path to full log on failure
}

// --- Step ---

type step int

const (
	stepDeploying step = iota
	stepDone
)

// --- Model ---

// Model is the deploy-all popup state.
type Model struct {
	items         []DeployItem
	envName       string
	step          step
	confirmCancel bool // true when showing "cancel?" confirmation
	spinner       spinner.Model
	doneCount     int
	width         int
	height        int
}

// New creates a new deploy-all popup. Items should have Status = StatusPending.
func New(envName string, items []DeployItem) Model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)

	return Model{
		items:   items,
		envName: envName,
		step:    stepDeploying,
		spinner: s,
	}
}

// SpinnerInit returns the initial spinner tick command.
func (m Model) SpinnerInit() tea.Cmd {
	return m.spinner.Tick
}

// MarkDeploying sets all pending items to deploying.
func (m *Model) MarkDeploying() {
	for i := range m.items {
		if m.items[i].Status == StatusPending {
			m.items[i].Status = StatusDeploying
		}
	}
}

// IsDeploying returns true if any deploys are still in flight.
func (m Model) IsDeploying() bool {
	return m.step == stepDeploying
}

// --- Update ---

// Update handles messages for the deploy-all popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ProjectDoneMsg:
		if msg.Index >= 0 && msg.Index < len(m.items) {
			item := &m.items[msg.Index]
			if msg.Err != nil {
				item.Status = StatusFailed
				item.ErrSummary = firstLine(msg.Err.Error())
				item.LogPath = msg.LogPath
			} else {
				item.Status = StatusSuccess
			}
			m.doneCount++
		}

		// Check if all done
		if m.doneCount >= len(m.items) {
			m.step = stepDone
			return m, func() tea.Msg { return DoneMsg{} }
		}
		return m, nil

	case spinner.TickMsg:
		if m.step == stepDeploying {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		return m.updateKeys(msg)
	}

	return m, nil
}

func (m Model) updateKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Cancel confirmation overlay
	if m.confirmCancel {
		switch msg.String() {
		case "y", "Y":
			m.confirmCancel = false
			// Mark all deploying items as cancelled
			for i := range m.items {
				if m.items[i].Status == StatusDeploying || m.items[i].Status == StatusPending {
					m.items[i].Status = StatusCancelled
					m.doneCount++
				}
			}
			m.step = stepDone
			// Emit CancelMsg so app kills runners, then DoneMsg for refresh
			return m, tea.Batch(
				func() tea.Msg { return CancelMsg{} },
				func() tea.Msg { return DoneMsg{} },
			)
		case "n", "N", "esc":
			m.confirmCancel = false
			return m, nil
		}
		return m, nil
	}

	switch m.step {
	case stepDeploying:
		if msg.String() == "esc" {
			m.confirmCancel = true
		}
		return m, nil

	case stepDone:
		switch msg.String() {
		case "esc", "enter":
			return m, func() tea.Msg { return CloseMsg{} }
		}
		return m, nil
	}

	return m, nil
}

// --- View ---

// View renders the deploy-all popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 80 {
		popupWidth = 80
	}

	innerWidth := popupWidth - 6 // padding + border

	// Title
	title := theme.TitleStyle.Render(fmt.Sprintf("  Deploy All — %s", m.envName))
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", innerWidth))

	// Items
	var itemLines []string
	for _, item := range m.items {
		line := m.renderItem(item, innerWidth)
		itemLines = append(itemLines, line)
	}

	// Progress counter
	total := len(m.items)
	successCount := 0
	failCount := 0
	cancelCount := 0
	for _, item := range m.items {
		switch item.Status {
		case StatusSuccess:
			successCount++
		case StatusFailed:
			failCount++
		case StatusCancelled:
			cancelCount++
		}
	}

	var statusParts []string
	statusParts = append(statusParts, fmt.Sprintf("%d of %d complete", m.doneCount, total))
	if successCount > 0 {
		statusParts = append(statusParts, theme.SuccessStyle.Render(fmt.Sprintf("%d succeeded", successCount)))
	}
	if failCount > 0 {
		statusParts = append(statusParts, theme.ErrorStyle.Render(fmt.Sprintf("%d failed", failCount)))
	}
	if cancelCount > 0 {
		statusParts = append(statusParts, theme.DimStyle.Render(fmt.Sprintf("%d cancelled", cancelCount)))
	}
	progressLine := "  " + strings.Join(statusParts, "  ")

	// Help
	var help string
	switch m.step {
	case stepDeploying:
		help = theme.DimStyle.Render("  esc cancel")
	case stepDone:
		help = theme.DimStyle.Render("  esc close")
	}

	// Assemble
	var lines []string
	lines = append(lines, title)
	lines = append(lines, sep)
	lines = append(lines, itemLines...)
	lines = append(lines, sep)
	lines = append(lines, progressLine)

	// Cancel confirmation
	if m.confirmCancel {
		lines = append(lines, "")
		lines = append(lines, theme.ErrorStyle.Render("  Cancel all in-flight deploys?"))
		lines = append(lines, theme.ErrorStyle.Render("  This may lead to corrupted state. (y/n)"))
	}

	lines = append(lines, help)

	content := strings.Join(lines, "\n")

	popup := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorOrange).
		Padding(1, 2).
		Width(popupWidth).
		Render(content)

	return popup
}

func (m Model) renderItem(item DeployItem, width int) string {
	var icon string
	var statusText string

	switch item.Status {
	case StatusPending:
		icon = theme.DimStyle.Render("○")
		statusText = theme.DimStyle.Render("Pending")
	case StatusDeploying:
		icon = m.spinner.View()
		statusText = theme.LabelStyle.Render("Deploying...")
	case StatusSuccess:
		icon = theme.SuccessStyle.Render("✓")
		statusText = theme.SuccessStyle.Render("Successful")
	case StatusFailed:
		icon = theme.ErrorStyle.Render("✗")
		errText := "Failed"
		if item.ErrSummary != "" {
			errText = fmt.Sprintf("Failed — %s", item.ErrSummary)
		}
		statusText = theme.ErrorStyle.Render(errText)
		if item.LogPath != "" {
			statusText += "\n      " + theme.DimStyle.Render(item.LogPath)
		}
	case StatusCancelled:
		icon = theme.DimStyle.Render("–")
		statusText = theme.DimStyle.Render("Cancelled")
	}

	name := theme.NormalItemStyle.Render(fmt.Sprintf("%-20s", truncate(item.ProjectName, 20)))
	return fmt.Sprintf("  %s %s  %s", icon, name, statusText)
}

// --- Helpers ---

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
