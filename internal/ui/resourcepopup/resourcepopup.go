package resourcepopup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Steps ---

type step int

const (
	stepSelectType step = iota // Pick resource type
	stepFields                 // Enter name + type-specific fields
	stepCreating               // Running wrangler CLI (spinner)
	stepResult                 // Show success/error
)

// --- Resource type entries ---

type resourceTypeEntry struct {
	Code  string // Internal code: "d1", "kv", "r2", "queue", "vectorize", "hyperdrive"
	Label string // Human-readable label
	Desc  string // Short description
}

var resourceTypes = []resourceTypeEntry{
	{Code: "d1", Label: "D1 Database", Desc: "SQLite-compatible database"},
	{Code: "kv", Label: "KV Namespace", Desc: "Key-value storage"},
	{Code: "r2", Label: "R2 Bucket", Desc: "Object storage"},
	{Code: "queue", Label: "Queue", Desc: "Message queue"},
	{Code: "vectorize", Label: "Vectorize Index", Desc: "Vector search index"},
	{Code: "hyperdrive", Label: "Hyperdrive Config", Desc: "Database connection accelerator"},
}

// --- Extra field definitions per resource type ---

// fieldDef describes one text input field beyond the name.
type fieldDef struct {
	Key         string              // Map key for ExtraArgs (e.g., "dimensions")
	Label       string              // Displayed label
	Placeholder string              // Placeholder text
	Required    bool                // Whether the field must be non-empty
	Validate    func(string) string // Optional validation, returns error message or ""
}

// extraFieldDefs returns the type-specific fields for a given resource type.
// All types have a "name" field implicitly; these are additional fields.
func extraFieldDefs(resourceType string) []fieldDef {
	switch resourceType {
	case "vectorize":
		return []fieldDef{
			{
				Key:         "dimensions",
				Label:       "Dimensions",
				Placeholder: "768",
				Required:    true,
				Validate: func(s string) string {
					if !regexp.MustCompile(`^\d+$`).MatchString(s) {
						return "Must be a positive integer"
					}
					return ""
				},
			},
			{
				Key:         "metric",
				Label:       "Distance Metric",
				Placeholder: "cosine (cosine/euclidean/dot-product)",
				Required:    true,
				Validate: func(s string) string {
					switch s {
					case "cosine", "euclidean", "dot-product":
						return ""
					default:
						return "Must be cosine, euclidean, or dot-product"
					}
				},
			},
		}
	case "hyperdrive":
		return []fieldDef{
			{
				Key:         "connection-string",
				Label:       "Connection String",
				Placeholder: "postgres://user:pass@host:5432/db",
				Required:    true,
			},
		}
	}
	return nil
}

// --- Messages emitted by this component ---

// CreateResourceMsg requests the app to run wrangler CLI to create a resource.
type CreateResourceMsg struct {
	ResourceType string            // "d1", "kv", "r2", etc.
	Name         string            // Resource name
	ExtraArgs    map[string]string // Type-specific flags
}

// CreateResourceDoneMsg delivers the result of resource creation.
type CreateResourceDoneMsg struct {
	ResourceType string
	Name         string
	Success      bool
	Output       string // CLI output for display
	ResourceID   string // Parsed resource ID
}

// CloseMsg signals the popup should close without changes.
type CloseMsg struct{}

// DoneMsg signals the operation completed successfully. The app should
// refresh the relevant service cache.
type DoneMsg struct {
	ResourceType string // The type of resource that was created
	ServiceName  string // Service name for cache refresh (e.g., "KV", "D1")
}

// --- Model ---

// Model is the resource creation popup state.
type Model struct {
	step step

	// Type selection
	typeCursor int

	// Fields step
	resourceType string          // selected resource type code
	nameInput    textinput.Model // first input is always the name
	extraInputs  []textinput.Model
	extraDefs    []fieldDef
	focusedField int    // 0 = name, 1+ = extra fields
	inputErr     string // validation error

	// Creating step
	spinner spinner.Model

	// Result step
	resultMsg   string
	resultIsErr bool

	// Cached state
	resourceName string // set after fields step
}

// --- Constructor ---

// New creates a new resource creation popup.
func New() Model {
	ni := textinput.New()
	ni.Placeholder = "my-resource"
	ni.CharLimit = 63
	ni.Width = 40
	ni.Prompt = "  "
	ni.PromptStyle = theme.SelectedItemStyle
	ni.TextStyle = theme.ValueStyle
	ni.PlaceholderStyle = theme.DimStyle

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(theme.ColorOrange)

	return Model{
		step:      stepSelectType,
		nameInput: ni,
		spinner:   s,
	}
}

// --- Validation ---

var validResourceNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

func isValidResourceName(name string) bool {
	return validResourceNameRe.MatchString(name)
}

// --- Accessors ---

// IsCreating returns true when the popup is in the creating step (spinner active).
func (m Model) IsCreating() bool {
	return m.step == stepCreating
}

// --- Update ---

// Update handles messages for the resource creation popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case CreateResourceDoneMsg:
		return m.handleCreateDone(msg)
	case spinner.TickMsg:
		if m.step == stepCreating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyMsg:
		switch m.step {
		case stepSelectType:
			return m.updateSelectType(msg)
		case stepFields:
			return m.updateFields(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}

	// Forward non-key messages to focused text input (cursor blink, etc.)
	if m.step == stepFields {
		return m.forwardToFocusedInput(msg)
	}
	return m, nil
}

// --- Step: Select type ---

func (m Model) updateSelectType(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, func() tea.Msg { return CloseMsg{} }
	case "up", "k":
		if m.typeCursor > 0 {
			m.typeCursor--
		}
	case "down", "j":
		if m.typeCursor < len(resourceTypes)-1 {
			m.typeCursor++
		}
	case "enter":
		selected := resourceTypes[m.typeCursor]
		m.resourceType = selected.Code
		m.step = stepFields
		m.focusedField = 0
		m.inputErr = ""

		// Set up name input with appropriate placeholder
		m.nameInput.Placeholder = namePlaceholder(selected.Code)
		m.nameInput.SetValue("")
		m.nameInput.Focus()

		// Set up extra fields
		m.extraDefs = extraFieldDefs(selected.Code)
		m.extraInputs = make([]textinput.Model, len(m.extraDefs))
		for i, def := range m.extraDefs {
			ti := textinput.New()
			ti.Placeholder = def.Placeholder
			ti.CharLimit = 200
			ti.Width = 40
			ti.Prompt = "  "
			ti.PromptStyle = theme.DimStyle
			ti.TextStyle = theme.ValueStyle
			ti.PlaceholderStyle = theme.DimStyle
			m.extraInputs[i] = ti
		}

		return m, m.nameInput.Focus()
	}
	return m, nil
}

// namePlaceholder returns an appropriate placeholder for the name field.
func namePlaceholder(resourceType string) string {
	switch resourceType {
	case "d1":
		return "my-database"
	case "kv":
		return "my-namespace"
	case "r2":
		return "my-bucket"
	case "queue":
		return "my-queue"
	case "vectorize":
		return "my-index"
	case "hyperdrive":
		return "my-config"
	default:
		return "my-resource"
	}
}

// --- Step: Fields ---

func (m Model) updateFields(msg tea.KeyMsg) (Model, tea.Cmd) {
	totalFields := 1 + len(m.extraDefs) // name + extras

	switch msg.String() {
	case "esc":
		// Go back to type selection
		m.step = stepSelectType
		m.inputErr = ""
		return m, nil
	case "tab", "down":
		// Move to next field
		if m.focusedField < totalFields-1 {
			m.blurAll()
			m.focusedField++
			return m, m.focusField(m.focusedField)
		}
	case "shift+tab", "up":
		// Move to previous field
		if m.focusedField > 0 {
			m.blurAll()
			m.focusedField--
			return m, m.focusField(m.focusedField)
		}
	case "enter":
		// Validate all fields and submit
		return m.submitFields()
	}

	// Forward to focused text input
	return m.forwardKeyToFocusedInput(msg)
}

// blurAll removes focus from all inputs.
func (m *Model) blurAll() {
	m.nameInput.Blur()
	for i := range m.extraInputs {
		m.extraInputs[i].Blur()
	}
}

// focusField focuses the field at index idx (0=name, 1+=extras).
func (m *Model) focusField(idx int) tea.Cmd {
	if idx == 0 {
		m.nameInput.PromptStyle = theme.SelectedItemStyle
		return m.nameInput.Focus()
	}
	extraIdx := idx - 1
	if extraIdx < len(m.extraInputs) {
		m.extraInputs[extraIdx].PromptStyle = theme.SelectedItemStyle
		return m.extraInputs[extraIdx].Focus()
	}
	return nil
}

// forwardToFocusedInput forwards non-key messages to the focused text input.
func (m Model) forwardToFocusedInput(msg tea.Msg) (Model, tea.Cmd) {
	if m.focusedField == 0 {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	extraIdx := m.focusedField - 1
	if extraIdx < len(m.extraInputs) {
		var cmd tea.Cmd
		m.extraInputs[extraIdx], cmd = m.extraInputs[extraIdx].Update(msg)
		return m, cmd
	}
	return m, nil
}

// forwardKeyToFocusedInput forwards key messages to the focused text input.
func (m Model) forwardKeyToFocusedInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	m.inputErr = ""
	if m.focusedField == 0 {
		var cmd tea.Cmd
		m.nameInput, cmd = m.nameInput.Update(msg)
		return m, cmd
	}
	extraIdx := m.focusedField - 1
	if extraIdx < len(m.extraInputs) {
		var cmd tea.Cmd
		m.extraInputs[extraIdx], cmd = m.extraInputs[extraIdx].Update(msg)
		return m, cmd
	}
	return m, nil
}

// submitFields validates all fields and emits CreateResourceMsg on success.
func (m Model) submitFields() (Model, tea.Cmd) {
	name := strings.TrimSpace(m.nameInput.Value())
	if name == "" {
		m.inputErr = "Name cannot be empty"
		return m, nil
	}
	if !isValidResourceName(name) {
		m.inputErr = "Must start with a letter, only letters/digits/hyphens/underscores"
		return m, nil
	}

	extraArgs := make(map[string]string)
	for i, def := range m.extraDefs {
		val := strings.TrimSpace(m.extraInputs[i].Value())
		if def.Required && val == "" {
			m.inputErr = fmt.Sprintf("%s is required", def.Label)
			m.blurAll()
			m.focusedField = i + 1
			return m, m.focusField(m.focusedField)
		}
		if val != "" && def.Validate != nil {
			if errMsg := def.Validate(val); errMsg != "" {
				m.inputErr = fmt.Sprintf("%s: %s", def.Label, errMsg)
				m.blurAll()
				m.focusedField = i + 1
				return m, m.focusField(m.focusedField)
			}
		}
		if val != "" {
			extraArgs[def.Key] = val
		}
	}

	m.resourceName = name
	m.step = stepCreating
	rt := m.resourceType
	return m, tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			return CreateResourceMsg{
				ResourceType: rt,
				Name:         name,
				ExtraArgs:    extraArgs,
			}
		},
	)
}

// --- Step: Create done ---

func (m Model) handleCreateDone(msg CreateResourceDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if !msg.Success {
		output := msg.Output
		if len(output) > 500 {
			output = output[len(output)-500:]
		}
		m.resultMsg = fmt.Sprintf("Failed to create resource:\n%s", output)
		m.resultIsErr = true
		return m, nil
	}

	m.resultMsg = fmt.Sprintf("%s %q created", resourceTypeLabel(m.resourceType), msg.Name)
	m.resultIsErr = false
	return m, nil
}

// --- Step: Result ---

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter":
		if !m.resultIsErr {
			rt := m.resourceType
			return m, func() tea.Msg {
				return DoneMsg{
					ResourceType: rt,
					ServiceName:  serviceNameForType(rt),
				}
			}
		}
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

// --- View ---

// View renders the resource creation popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 80 {
		popupWidth = 80
	}

	title := "  Create Resource"
	titleLine := theme.TitleStyle.Render(title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("-", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepSelectType:
		body = m.viewSelectType()
		help = "  esc close  |  enter select  |  j/k navigate"
	case stepFields:
		body = m.viewFields()
		help = "  esc back  |  enter create  |  tab next field"
	case stepCreating:
		body = m.viewCreating()
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

func (m Model) viewSelectType() string {
	var lines []string
	lines = append(lines, theme.DimStyle.Render("  Select resource type to create:"))
	lines = append(lines, "")

	for i, rt := range resourceTypes {
		cursor := "  "
		if i == m.typeCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}
		label := theme.ActionItemStyle.Render(fmt.Sprintf("%-20s", rt.Label))
		desc := theme.ActionDescStyle.Render(rt.Desc)
		lines = append(lines, fmt.Sprintf("%s  %s  %s", cursor, label, desc))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewFields() string {
	var lines []string
	typeLabel := resourceTypeLabel(m.resourceType)
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Create new %s:", typeLabel)))
	lines = append(lines, "")

	// Name field
	nameLabel := "Name"
	if m.focusedField == 0 {
		lines = append(lines, theme.LabelStyle.Render(fmt.Sprintf("  %s:", nameLabel)))
	} else {
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %s:", nameLabel)))
	}
	lines = append(lines, m.nameInput.View())
	lines = append(lines, "")

	// Extra fields
	for i, def := range m.extraDefs {
		if m.focusedField == i+1 {
			lines = append(lines, theme.LabelStyle.Render(fmt.Sprintf("  %s:", def.Label)))
		} else {
			lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %s:", def.Label)))
		}
		lines = append(lines, m.extraInputs[i].View())
		lines = append(lines, "")
	}

	if m.inputErr != "" {
		lines = append(lines, theme.ErrorStyle.Render("  "+m.inputErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewCreating() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("  %s %s",
		m.spinner.View(),
		theme.DimStyle.Render(fmt.Sprintf("Creating %s %q...",
			resourceTypeLabel(m.resourceType), m.resourceName))))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Running wrangler CLI."))
	return strings.Join(lines, "\n")
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

// --- Helpers ---

// resourceTypeLabel returns a human-readable label for a resource type code.
func resourceTypeLabel(code string) string {
	for _, rt := range resourceTypes {
		if rt.Code == code {
			return rt.Label
		}
	}
	return code
}

// serviceNameForType maps resource type codes to service registry names.
func serviceNameForType(resourceType string) string {
	switch resourceType {
	case "d1":
		return "D1"
	case "kv":
		return "KV"
	case "r2":
		return "R2"
	case "queue":
		return "Queues"
	case "vectorize":
		return "Vectorize"
	case "hyperdrive":
		return "Hyperdrive"
	default:
		return ""
	}
}
