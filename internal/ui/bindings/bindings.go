package bindings

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Steps ---

type step int

const (
	stepSelectType     step = iota // Pick resource type (D1, KV, R2, Queue)
	stepSelectResource             // Pick existing resource or "Create New" (project view only)
	stepNameInput                  // Enter name for new resource
	stepBindingName                // Enter JS binding name
	stepCreating                   // Creating resource (spinner)
	stepWriting                    // Writing config (spinner)
	stepResult                     // Show success/error, auto-close
)

// --- Resource item for the picker ---

// ResourceItem represents an existing resource shown in the resource list.
type ResourceItem struct {
	ID   string // Resource identifier (database_id, namespace_id, bucket_name, queue_id)
	Name string // Human-readable name
}

// --- Messages emitted by this component ---

// ListResourcesMsg requests the app to fetch existing resources for a type.
type ListResourcesMsg struct {
	ResourceType string // "d1", "kv", "r2", "queue"
}

// ResourcesLoadedMsg delivers the fetched resource list back to the popup.
type ResourcesLoadedMsg struct {
	ResourceType string
	Items        []ResourceItem
	Err          error
}

// CreateResourceMsg requests the app to create a new resource.
type CreateResourceMsg struct {
	ResourceType string
	Name         string
}

// CreateResourceDoneMsg delivers the result of resource creation.
type CreateResourceDoneMsg struct {
	ResourceType string
	Name         string
	Success      bool
	Output       string // CLI output for display
	ResourceID   string // Parsed resource ID (database_id, namespace_id, etc.)
}

// WriteBindingMsg requests the app to write a binding into the wrangler config.
type WriteBindingMsg struct {
	ConfigPath string
	EnvName    string
	Binding    wcfg.BindingDef
}

// WriteBindingDoneMsg delivers the result of writing a binding.
type WriteBindingDoneMsg struct {
	Success bool
	Err     error
}

// CloseMsg signals the popup should close.
type CloseMsg struct{}

// DoneMsg signals the binding operation completed (success). The app should
// re-parse the config to refresh the UI.
type DoneMsg struct {
	ConfigPath string
}

// --- Mode ---

// Mode controls whether we're in monorepo (create-only) or project (create + assign) mode.
type Mode int

const (
	ModeMonorepo Mode = iota // Create resources only (no worker context)
	ModeProject              // Create or assign existing resources to a worker
)

// --- Model ---

// Model is the binding popup wizard state.
type Model struct {
	mode Mode
	step step

	// Context (project mode)
	configPath string // path to wrangler config file
	envName    string // target environment
	workerName string // display name for the worker

	// Resource type selection
	resourceTypes []resourceTypeEntry
	typeCursor    int

	// Resource list (project mode)
	resources       []ResourceItem
	resourceCursor  int
	resourcesLoaded bool
	resourcesErr    error

	// Text inputs
	nameInput    textinput.Model // resource name (create) or binding name
	bindingInput textinput.Model // JS binding name
	inputErr     string          // validation error

	// Created resource info (set after successful creation)
	createdResourceID string

	// Result
	resultMsg   string
	resultIsErr bool

	// Dimensions
	width  int
	height int
}

// resourceTypeEntry is an entry in the type selection list.
type resourceTypeEntry struct {
	Type  string // "d1", "kv", "r2", "queue"
	Label string // "D1 Database"
	Desc  string // Short description
}

var defaultResourceTypes = []resourceTypeEntry{
	{Type: "d1", Label: "D1 Database", Desc: "SQLite database at the edge"},
	{Type: "kv", Label: "KV Namespace", Desc: "Key-value storage"},
	{Type: "r2", Label: "R2 Bucket", Desc: "S3-compatible object storage"},
	{Type: "queue", Label: "Queue", Desc: "Message queue (producer binding)"},
}

// NewMonorepo creates a binding popup in monorepo mode (create-only).
func NewMonorepo() Model {
	return newModel(ModeMonorepo, "", "", "")
}

// NewProject creates a binding popup in project mode (create + assign).
func NewProject(configPath, envName, workerName string) Model {
	return newModel(ModeProject, configPath, envName, workerName)
}

func newModel(mode Mode, configPath, envName, workerName string) Model {
	ni := textinput.New()
	ni.Placeholder = "my-resource-name"
	ni.CharLimit = 63
	ni.Width = 40
	ni.Prompt = "  "
	ni.PromptStyle = theme.SelectedItemStyle
	ni.TextStyle = theme.ValueStyle
	ni.PlaceholderStyle = theme.DimStyle

	bi := textinput.New()
	bi.Placeholder = "MY_BINDING"
	bi.CharLimit = 63
	bi.Width = 40
	bi.Prompt = "  "
	bi.PromptStyle = theme.SelectedItemStyle
	bi.TextStyle = theme.ValueStyle
	bi.PlaceholderStyle = theme.DimStyle

	return Model{
		mode:          mode,
		step:          stepSelectType,
		configPath:    configPath,
		envName:       envName,
		workerName:    workerName,
		resourceTypes: defaultResourceTypes,
		nameInput:     ni,
		bindingInput:  bi,
	}
}

// SetSize updates the dimensions available for rendering.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SelectedType returns the currently selected resource type string.
func (m Model) SelectedType() string {
	if m.typeCursor >= 0 && m.typeCursor < len(m.resourceTypes) {
		return m.resourceTypes[m.typeCursor].Type
	}
	return ""
}

// --- Update ---

// Update handles messages for the binding popup.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ResourcesLoadedMsg:
		return m.handleResourcesLoaded(msg)
	case CreateResourceDoneMsg:
		return m.handleCreateDone(msg)
	case WriteBindingDoneMsg:
		return m.handleWriteDone(msg)
	case tea.KeyMsg:
		switch m.step {
		case stepSelectType:
			return m.updateSelectType(msg)
		case stepSelectResource:
			return m.updateSelectResource(msg)
		case stepNameInput:
			return m.updateNameInput(msg)
		case stepBindingName:
			return m.updateBindingName(msg)
		case stepResult:
			return m.updateResult(msg)
		}
	}

	// Forward non-key messages to active text inputs (cursor blink, etc.)
	var cmd tea.Cmd
	switch m.step {
	case stepNameInput:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case stepBindingName:
		m.bindingInput, cmd = m.bindingInput.Update(msg)
	}
	return m, cmd
}

// --- Step: Select Type ---

func (m Model) updateSelectType(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+n":
		return m, func() tea.Msg { return CloseMsg{} }
	case "up", "k":
		if m.typeCursor > 0 {
			m.typeCursor--
		}
	case "down", "j":
		if m.typeCursor < len(m.resourceTypes)-1 {
			m.typeCursor++
		}
	case "enter":
		if m.mode == ModeMonorepo {
			// Monorepo: go straight to name input for creation
			m.step = stepNameInput
			m.nameInput.SetValue("")
			m.inputErr = ""
			return m, m.nameInput.Focus()
		}
		// Project mode: fetch existing resources for this type
		m.step = stepSelectResource
		m.resources = nil
		m.resourceCursor = 0
		m.resourcesLoaded = false
		m.resourcesErr = nil
		resType := m.resourceTypes[m.typeCursor].Type
		return m, func() tea.Msg { return ListResourcesMsg{ResourceType: resType} }
	}
	return m, nil
}

// --- Step: Select Resource ---

func (m Model) updateSelectResource(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Go back to type selection
		m.step = stepSelectType
		return m, nil
	case "up", "k":
		if m.resourceCursor > 0 {
			m.resourceCursor--
		}
	case "down", "j":
		max := len(m.resources) // +1 for "Create New" which is at index 0
		if m.resourceCursor < max {
			m.resourceCursor++
		}
	case "enter":
		if !m.resourcesLoaded {
			return m, nil
		}
		if m.resourceCursor == 0 {
			// "Create New" selected
			m.step = stepNameInput
			m.nameInput.SetValue("")
			m.inputErr = ""
			return m, m.nameInput.Focus()
		}
		// Existing resource selected — go to binding name input
		idx := m.resourceCursor - 1 // offset by "Create New"
		if idx >= 0 && idx < len(m.resources) {
			res := m.resources[idx]
			m.step = stepBindingName
			suggested := wcfg.SuggestBindingName(res.Name)
			m.bindingInput.SetValue(suggested)
			m.inputErr = ""
			return m, m.bindingInput.Focus()
		}
	}
	return m, nil
}

func (m Model) handleResourcesLoaded(msg ResourcesLoadedMsg) (Model, tea.Cmd) {
	m.resourcesLoaded = true
	if msg.Err != nil {
		m.resourcesErr = msg.Err
		return m, nil
	}
	m.resources = msg.Items
	return m, nil
}

// --- Step: Name Input (create new resource) ---

func (m Model) updateNameInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.mode == ModeMonorepo {
			m.step = stepSelectType
		} else {
			m.step = stepSelectResource
		}
		m.inputErr = ""
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			m.inputErr = "Name cannot be empty"
			return m, nil
		}
		if strings.HasPrefix(name, "-") {
			m.inputErr = "Name cannot start with -"
			return m, nil
		}
		if m.mode == ModeMonorepo {
			// Create resource only (no binding assignment)
			m.step = stepCreating
			resType := m.resourceTypes[m.typeCursor].Type
			return m, func() tea.Msg {
				return CreateResourceMsg{ResourceType: resType, Name: name}
			}
		}
		// Project mode: create resource, then assign binding
		m.step = stepCreating
		resType := m.resourceTypes[m.typeCursor].Type
		return m, func() tea.Msg {
			return CreateResourceMsg{ResourceType: resType, Name: name}
		}
	}

	// Forward to text input
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	m.inputErr = ""
	return m, cmd
}

func (m Model) handleCreateDone(msg CreateResourceDoneMsg) (Model, tea.Cmd) {
	if !msg.Success {
		m.step = stepResult
		m.resultMsg = fmt.Sprintf("Failed to create %s:\n%s",
			wcfg.ResourceTypeLabel(msg.ResourceType), msg.Output)
		m.resultIsErr = true
		return m, nil
	}

	// Store the created resource ID (parsed by the app layer from CLI output)
	m.createdResourceID = msg.ResourceID

	if m.mode == ModeMonorepo {
		// Done — just show success
		m.step = stepResult
		m.resultMsg = fmt.Sprintf("Created %s %q",
			wcfg.ResourceTypeLabel(msg.ResourceType), msg.Name)
		m.resultIsErr = false
		return m, nil
	}

	// Project mode: now ask for binding name
	m.step = stepBindingName
	suggested := wcfg.SuggestBindingName(msg.Name)
	m.bindingInput.SetValue(suggested)
	m.inputErr = ""
	return m, m.bindingInput.Focus()
}

// --- Step: Binding Name Input ---

func (m Model) updateBindingName(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Go back to resource selection (or type selection if create path)
		if m.mode == ModeMonorepo {
			m.step = stepSelectType
		} else {
			m.step = stepSelectResource
		}
		m.inputErr = ""
		return m, nil
	case "enter":
		bindingName := strings.TrimSpace(m.bindingInput.Value())
		if bindingName == "" {
			m.inputErr = "Binding name cannot be empty"
			return m, nil
		}
		// Validate: must be a valid JS identifier-like name
		if !isValidBindingName(bindingName) {
			m.inputErr = "Must be alphanumeric/underscores, start with letter or _"
			return m, nil
		}

		// Build the binding definition and request writing
		bd := m.buildBindingDef(bindingName)
		m.step = stepWriting
		return m, func() tea.Msg {
			return WriteBindingMsg{
				ConfigPath: m.configPath,
				EnvName:    m.envName,
				Binding:    bd,
			}
		}
	}

	// Forward to text input
	var cmd tea.Cmd
	m.bindingInput, cmd = m.bindingInput.Update(msg)
	m.inputErr = ""
	return m, cmd
}

// buildBindingDef constructs a BindingDef from the current state.
func (m Model) buildBindingDef(bindingName string) wcfg.BindingDef {
	resType := m.resourceTypes[m.typeCursor].Type

	// Determine resource ID and name based on whether we created or selected
	var resourceID, resourceName string

	if m.resourceCursor == 0 || m.mode == ModeMonorepo {
		// Created new — use the name input value and the parsed resource ID
		resourceName = strings.TrimSpace(m.nameInput.Value())
		if m.createdResourceID != "" {
			resourceID = m.createdResourceID
		} else {
			// Fallback: for R2 and Queue, the name IS the identifier
			resourceID = resourceName
		}
	} else {
		// Selected existing resource
		idx := m.resourceCursor - 1
		if idx >= 0 && idx < len(m.resources) {
			resourceID = m.resources[idx].ID
			resourceName = m.resources[idx].Name
		}
	}

	return wcfg.BindingDef{
		Type:         resType,
		BindingName:  bindingName,
		ResourceID:   resourceID,
		ResourceName: resourceName,
	}
}

func (m Model) handleWriteDone(msg WriteBindingDoneMsg) (Model, tea.Cmd) {
	m.step = stepResult
	if !msg.Success {
		m.resultMsg = fmt.Sprintf("Failed to write binding: %v", msg.Err)
		m.resultIsErr = true
		return m, nil
	}

	resType := m.resourceTypes[m.typeCursor].Type
	m.resultMsg = fmt.Sprintf("Added %s binding %q",
		wcfg.ResourceTypeLabel(resType),
		strings.TrimSpace(m.bindingInput.Value()))
	m.resultIsErr = false

	// Don't emit DoneMsg yet — let the user see the success screen first.
	// DoneMsg is emitted when they press Enter/Esc in updateResult.
	return m, nil
}

// --- Step: Result ---

func (m Model) updateResult(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "ctrl+n":
		if !m.resultIsErr && m.configPath != "" {
			// Success with a config path — signal the app to reload config
			return m, func() tea.Msg { return DoneMsg{ConfigPath: m.configPath} }
		}
		return m, func() tea.Msg { return CloseMsg{} }
	}
	return m, nil
}

// --- View ---

// View renders the binding popup.
func (m Model) View(termWidth, termHeight int) string {
	popupWidth := termWidth * 2 / 3
	if popupWidth < 50 {
		popupWidth = 50
	}
	if popupWidth > 90 {
		popupWidth = 90
	}

	var title string
	switch m.mode {
	case ModeMonorepo:
		title = "  Create Resource"
	case ModeProject:
		if m.workerName != "" {
			title = fmt.Sprintf("  Add Binding — %s", m.workerName)
		} else {
			title = "  Add Binding"
		}
	}

	titleLine := theme.TitleStyle.Render(title)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", popupWidth-4))

	var body string
	var help string

	switch m.step {
	case stepSelectType:
		body = m.viewSelectType(popupWidth)
		help = "  esc close  |  enter select  |  j/k navigate"
	case stepSelectResource:
		body = m.viewSelectResource(popupWidth)
		help = "  esc back  |  enter select  |  j/k navigate"
	case stepNameInput:
		body = m.viewNameInput(popupWidth)
		help = "  esc back  |  enter confirm"
	case stepBindingName:
		body = m.viewBindingName(popupWidth)
		help = "  esc back  |  enter confirm"
	case stepCreating:
		body = m.viewCreating()
		help = ""
	case stepWriting:
		body = m.viewWriting()
		help = ""
	case stepResult:
		body = m.viewResult(popupWidth)
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

func (m Model) viewSelectType(popupWidth int) string {
	subtitle := ""
	if m.mode == ModeMonorepo {
		subtitle = theme.DimStyle.Render("  Select a resource type to create")
	} else {
		subtitle = theme.DimStyle.Render("  Select a resource type to bind")
	}

	var lines []string
	lines = append(lines, subtitle, "")

	for i, rt := range m.resourceTypes {
		cursor := "  "
		if i == m.typeCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		label := theme.ActionItemStyle.Render(fmt.Sprintf("%-16s", rt.Label))
		desc := theme.ActionDescStyle.Render(rt.Desc)
		lines = append(lines, fmt.Sprintf("%s  %s  %s", cursor, label, desc))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewSelectResource(popupWidth int) string {
	resType := m.resourceTypes[m.typeCursor]

	subtitle := theme.DimStyle.Render(fmt.Sprintf("  Select %s or create new", resType.Label))

	var lines []string
	lines = append(lines, subtitle, "")

	if !m.resourcesLoaded {
		lines = append(lines, theme.DimStyle.Render("  Loading..."))
		return strings.Join(lines, "\n")
	}

	if m.resourcesErr != nil {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("  Error: %v", m.resourcesErr)))
		return strings.Join(lines, "\n")
	}

	// "Create New" is always the first item
	totalItems := 1 + len(m.resources)

	// Scrolling: show up to maxVisible items
	maxVisible := 12
	if maxVisible > totalItems {
		maxVisible = totalItems
	}

	// Calculate scroll window
	scrollStart := 0
	if m.resourceCursor >= maxVisible {
		scrollStart = m.resourceCursor - maxVisible + 1
	}
	scrollEnd := scrollStart + maxVisible
	if scrollEnd > totalItems {
		scrollEnd = totalItems
		scrollStart = scrollEnd - maxVisible
		if scrollStart < 0 {
			scrollStart = 0
		}
	}

	for i := scrollStart; i < scrollEnd; i++ {
		cursor := "  "
		if i == m.resourceCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		if i == 0 {
			// "Create New" entry
			label := theme.SuccessStyle.Render("+ Create New...")
			lines = append(lines, fmt.Sprintf("%s  %s", cursor, label))
		} else {
			res := m.resources[i-1]
			name := theme.ActionItemStyle.Render(fmt.Sprintf("%-24s", truncateName(res.Name, 24)))
			id := theme.DimStyle.Render(truncateName(res.ID, 20))
			lines = append(lines, fmt.Sprintf("%s  %s  %s", cursor, name, id))
		}
	}

	if totalItems > maxVisible {
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  (%d total)", totalItems-1)))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewNameInput(popupWidth int) string {
	resType := m.resourceTypes[m.typeCursor]

	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Enter name for new %s:", resType.Label)))
	lines = append(lines, "")
	lines = append(lines, m.nameInput.View())

	if m.inputErr != "" {
		lines = append(lines, theme.ErrorStyle.Render("  "+m.inputErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewBindingName(popupWidth int) string {
	resType := m.resourceTypes[m.typeCursor]

	// Determine what resource we're binding
	var resourceDesc string
	if m.resourceCursor == 0 || m.mode == ModeMonorepo {
		resourceDesc = strings.TrimSpace(m.nameInput.Value())
	} else if m.resourceCursor > 0 && m.resourceCursor-1 < len(m.resources) {
		resourceDesc = m.resources[m.resourceCursor-1].Name
	}

	var lines []string
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Binding name for %s %q:", resType.Label, resourceDesc)))
	lines = append(lines, theme.DimStyle.Render("  This is the JS variable name (e.g. env.MY_DB)"))
	lines = append(lines, "")
	lines = append(lines, m.bindingInput.View())

	if m.inputErr != "" {
		lines = append(lines, theme.ErrorStyle.Render("  "+m.inputErr))
	}

	return strings.Join(lines, "\n")
}

func (m Model) viewCreating() string {
	resType := m.resourceTypes[m.typeCursor]
	name := strings.TrimSpace(m.nameInput.Value())
	return theme.DimStyle.Render(fmt.Sprintf("  Creating %s %q...", resType.Label, name))
}

func (m Model) viewWriting() string {
	return theme.DimStyle.Render("  Writing binding to config...")
}

func (m Model) viewResult(popupWidth int) string {
	var lines []string
	if m.resultIsErr {
		// Show error, potentially multi-line
		for _, line := range strings.Split(m.resultMsg, "\n") {
			lines = append(lines, theme.ErrorStyle.Render("  "+line))
		}
	} else {
		lines = append(lines, theme.SuccessStyle.Render("  "+m.resultMsg))
	}
	return strings.Join(lines, "\n")
}

// --- Helpers ---

func isValidBindingName(name string) bool {
	if len(name) == 0 {
		return false
	}
	first := name[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		return false
	}
	for _, c := range name[1:] {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func truncateName(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
