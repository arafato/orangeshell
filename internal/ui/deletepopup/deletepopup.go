package deletepopup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Mode ---

// Mode controls what the popup is for.
type Mode int

const (
	ModeResource      Mode = iota // Delete a Cloudflare resource (API call)
	ModeBindingDelete             // Remove a binding from local wrangler config
)

// --- Steps ---

type step int

const (
	stepLoading  step = iota // Checking bindings (spinner while index is built)
	stepBlocked              // Resource can't be deleted (has bindings)
	stepConfirm              // Confirm deletion (yes/no)
	stepDeleting             // Deletion in progress (spinner)
	stepResult               // Show success/error
)

// --- Messages emitted by this component ---

// DeleteMsg requests the app to delete the resource via the service API.
type DeleteMsg struct {
	ServiceName string
	ResourceID  string
}

// DeleteDoneMsg delivers the result of the resource deletion attempt.
type DeleteDoneMsg struct {
	ServiceName string
	Err         error
}

// DeleteBindingMsg requests the app to remove a binding from the local wrangler config.
type DeleteBindingMsg struct {
	ConfigPath  string
	EnvName     string
	BindingName string
	BindingType string
}

// DeleteBindingDoneMsg delivers the result of the binding removal.
type DeleteBindingDoneMsg struct {
	Err error
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals the operation completed successfully. The app should
// refresh the relevant state (service cache for resource delete, config
// re-parse for binding delete).
type DoneMsg struct {
	ServiceName string // non-empty for resource delete
	ResourceID  string // non-empty for resource delete
	ConfigPath  string // non-empty for binding delete
}

// --- Model ---

// Model is the deletion confirmation popup state.
type Model struct {
	mode Mode
	step step

	// Resource delete fields (ModeResource)
	serviceName  string // e.g. "KV", "D1", "R2", "Queues"
	resourceID   string // primary identifier for the API call
	resourceName string // human-readable name for display

	// Bound workers that reference this resource (for warnings / blocked view)
	boundWorkers []service.BoundWorker

	// Binding delete fields (ModeBindingDelete)
	configPath  string // path to wrangler config file
	envName     string // environment name
	bindingName string // JS variable name (e.g. "MY_KV")
	bindingType string // normalized type (e.g. "kv_namespace")
	workerName  string // display name of the Worker

	// Confirm step
	confirmCursor int // 0 = No (default safe), 1 = Yes

	// Result
	resultMsg   string
	resultIsErr bool

	// Spinner for loading/deleting steps
	spinner spinner.Model
}

// --- Constructors ---

// newSpinner creates the shared spinner model.
func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)
	return s
}

// New creates a resource deletion popup. If boundWorkers is non-empty, the popup
// starts in the blocked state (resource can't be deleted while bindings exist).
func New(serviceName, resourceID, resourceName string, boundWorkers []service.BoundWorker) Model {
	initialStep := stepConfirm
	if len(boundWorkers) > 0 {
		initialStep = stepBlocked
	}

	return Model{
		mode:         ModeResource,
		step:         initialStep,
		serviceName:  serviceName,
		resourceID:   resourceID,
		resourceName: resourceName,
		boundWorkers: boundWorkers,
		spinner:      newSpinner(),
	}
}

// NewLoading creates a popup that starts in the loading state (spinner while
// the binding index is being built). Call SetBindingWarnings to transition
// to the confirmation or blocked step once the index is available.
func NewLoading(serviceName, resourceID, resourceName string) Model {
	return Model{
		mode:         ModeResource,
		step:         stepLoading,
		serviceName:  serviceName,
		resourceID:   resourceID,
		resourceName: resourceName,
		spinner:      newSpinner(),
	}
}

// NewBindingDelete creates a binding removal popup (local config modification).
func NewBindingDelete(configPath, envName, bindingName, bindingType, workerName string) Model {
	return Model{
		mode:        ModeBindingDelete,
		step:        stepConfirm,
		configPath:  configPath,
		envName:     envName,
		bindingName: bindingName,
		bindingType: bindingType,
		workerName:  workerName,
		spinner:     newSpinner(),
	}
}

// --- Accessors ---

// SetBindingWarnings transitions from stepLoading with the resolved binding data.
// If there are bound workers, goes to stepBlocked; otherwise to stepConfirm.
func (m *Model) SetBindingWarnings(boundWorkers []service.BoundWorker) {
	m.boundWorkers = boundWorkers
	if len(boundWorkers) > 0 {
		m.step = stepBlocked
	} else {
		m.step = stepConfirm
	}
}

// IsLoading returns true if the popup is waiting for the binding index.
func (m Model) IsLoading() bool {
	return m.step == stepLoading
}

// IsDeleting returns true if the popup is in the deleting state (spinner active).
func (m Model) IsDeleting() bool {
	return m.step == stepDeleting
}

// NeedsSpinner returns true if the popup has an active spinner animation.
func (m Model) NeedsSpinner() bool {
	return m.step == stepLoading || m.step == stepDeleting
}

// SpinnerTick returns the command to start the spinner ticking.
func (m Model) SpinnerTick() tea.Cmd {
	return m.spinner.Tick
}

// --- Update ---

// Update handles messages for the delete confirmation popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case DeleteDoneMsg:
		return m.handleResourceDeleteDone(msg)
	case DeleteBindingDoneMsg:
		return m.handleBindingDeleteDone(msg)
	case spinner.TickMsg:
		if m.step == stepLoading || m.step == stepDeleting {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		switch m.step {
		case stepLoading:
			return m.updateLoading(msg)
		case stepBlocked:
			return m.updateBlocked(msg)
		case stepConfirm:
			return m.updateConfirm(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}
	return m, nil
}

func (m Model) updateLoading(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "esc" {
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

func (m Model) updateBlocked(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "esc" || msg.String() == "enter" {
		return m, func() tea.Msg { return CloseMsg{} }
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
			return m, func() tea.Msg { return CloseMsg{} }
		}
		// "Yes" selected — proceed
		m.step = stepDeleting
		switch m.mode {
		case ModeResource:
			svcName := m.serviceName
			resID := m.resourceID
			return m, tea.Batch(
				m.spinner.Tick,
				func() tea.Msg {
					return DeleteMsg{ServiceName: svcName, ResourceID: resID}
				},
			)
		case ModeBindingDelete:
			cp := m.configPath
			en := m.envName
			bn := m.bindingName
			bt := m.bindingType
			return m, tea.Batch(
				m.spinner.Tick,
				func() tea.Msg {
					return DeleteBindingMsg{
						ConfigPath:  cp,
						EnvName:     en,
						BindingName: bn,
						BindingType: bt,
					}
				},
			)
		}
	}
	return m, nil
}

func (m Model) handleResourceDeleteDone(msg DeleteDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if msg.Err != nil {
		m.resultMsg = fmt.Sprintf("Failed to delete: %v", msg.Err)
		m.resultIsErr = true
		return m, nil
	}
	m.resultMsg = fmt.Sprintf("%s %q deleted", m.serviceName, m.resourceName)
	m.resultIsErr = false
	return m, nil
}

func (m Model) handleBindingDeleteDone(msg DeleteBindingDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if msg.Err != nil {
		m.resultMsg = fmt.Sprintf("Failed to remove binding: %v", msg.Err)
		m.resultIsErr = true
		return m, nil
	}
	m.resultMsg = fmt.Sprintf("Binding %q removed from %q", m.bindingName, m.envName)
	m.resultIsErr = false
	return m, nil
}

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter":
		if m.resultIsErr {
			return m, func() tea.Msg { return CloseMsg{} }
		}
		switch m.mode {
		case ModeResource:
			svcName := m.serviceName
			resID := m.resourceID
			return m, func() tea.Msg {
				return DoneMsg{ServiceName: svcName, ResourceID: resID}
			}
		case ModeBindingDelete:
			cp := m.configPath
			return m, func() tea.Msg {
				return DoneMsg{ConfigPath: cp}
			}
		}
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

	title := m.title()
	titleLine := theme.TitleStyle.Render(title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepLoading:
		body = fmt.Sprintf("  %s %s", m.spinner.View(), theme.DimStyle.Render("Checking bindings..."))
		help = "  esc close"
	case stepBlocked:
		body = m.viewBlocked()
		help = "  esc close  |  enter close"
	case stepConfirm:
		body = m.viewConfirm()
		help = "  esc close  |  enter confirm  |  h/l select"
	case stepDeleting:
		body = m.viewDeleting()
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

func (m Model) title() string {
	switch m.mode {
	case ModeResource:
		return fmt.Sprintf("  Delete %s Resource", m.serviceName)
	case ModeBindingDelete:
		return "  Remove Binding"
	}
	return "  Delete"
}

func (m Model) viewBlocked() string {
	var lines []string

	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Cannot delete %s resource %q",
		m.serviceName, m.resourceName)))
	lines = append(lines, "")

	warningStyle := lipgloss.NewStyle().Foreground(theme.ColorRed)
	lines = append(lines, warningStyle.Render("  This resource is bound to:"))
	for _, bw := range m.boundWorkers {
		lines = append(lines, warningStyle.Render(
			fmt.Sprintf("    - Worker %q (as %s)", bw.ScriptName, bw.BindingName)))
	}
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Remove these bindings first using 'd' on the"))
	lines = append(lines, theme.DimStyle.Render("  binding in the project view, then redeploy."))

	return strings.Join(lines, "\n")
}

func (m Model) viewConfirm() string {
	var lines []string

	switch m.mode {
	case ModeResource:
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Delete %s resource %q?",
			m.serviceName, m.resourceName)))
		lines = append(lines, "")
		lines = append(lines, theme.DimStyle.Render("  This action cannot be undone."))

	case ModeBindingDelete:
		envLabel := m.envName
		if envLabel == "" || envLabel == "default" {
			envLabel = "default"
		}
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Remove binding %q from %q?",
			m.bindingName, envLabel)))
		lines = append(lines, "")
		warningStyle := lipgloss.NewStyle().Foreground(theme.ColorRed)
		lines = append(lines, warningStyle.Render("  This modifies the local wrangler config."))
		lines = append(lines, warningStyle.Render("  Run 'deploy' afterwards to apply the change"))
		lines = append(lines, warningStyle.Render("  to Cloudflare."))
	}

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

func (m Model) viewDeleting() string {
	var label string
	switch m.mode {
	case ModeResource:
		label = fmt.Sprintf("Deleting %q...", m.resourceName)
	case ModeBindingDelete:
		label = fmt.Sprintf("Removing binding %q...", m.bindingName)
	}
	return fmt.Sprintf("  %s %s", m.spinner.View(), theme.DimStyle.Render(label))
}

func (m Model) viewResult() string {
	var lines []string
	if m.resultIsErr {
		for _, line := range strings.Split(m.resultMsg, "\n") {
			lines = append(lines, theme.ErrorStyle.Render("  "+line))
		}
	} else {
		lines = append(lines, theme.SuccessStyle.Render("  "+m.resultMsg))
	}
	return strings.Join(lines, "\n")
}
