package config

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Messages ---

// OpenBindingsWizardMsg is emitted when the user wants to add a binding via the popup wizard.
type OpenBindingsWizardMsg struct {
	ConfigPath string
	EnvName    string
	WorkerName string
}

// DeleteBindingMsg requests removing a binding from the wrangler config.
type DeleteBindingMsg struct {
	ConfigPath  string
	EnvName     string
	BindingName string
	BindingType string
}

// DeleteBindingDoneMsg delivers the result.
type DeleteBindingDoneMsg struct {
	Err error
}

// --- Bindings Update ---

func (m Model) updateBindings(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.mode {
	case modeNormal:
		return m.updateBindingsList(msg)
	case modeDelete:
		return m.updateBindingsDelete(msg)
	case modeAddBinding:
		return m.updateBindingsTypeSelector(msg)
	case modeAddBindingForm:
		return m.updateBindingsForm(msg)
	case modeAddBindingPicker:
		return m.updateBindingsPicker(msg)
	}
	return m, nil
}

func (m Model) updateBindingsList(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.bindingsCursor < len(m.bindingItems)-1 {
			m.bindingsCursor++
		}
		return m, nil
	case "k", "up":
		if m.bindingsCursor > 0 {
			m.bindingsCursor--
		}
		return m, nil
	case "a":
		if m.config == nil {
			return m, nil
		}
		// Open inline type selector
		envName := "default"
		if m.bindingsCursor >= 0 && m.bindingsCursor < len(m.bindingItems) {
			envName = m.bindingItems[m.bindingsCursor].EnvName
		}
		m.addBindingEnvName = envName
		m.addBindingTypeCursor = 0
		m.mode = modeAddBinding
		return m, nil
	case "d":
		if m.bindingsCursor >= 0 && m.bindingsCursor < len(m.bindingItems) {
			b := m.bindingItems[m.bindingsCursor]
			m.mode = modeDelete
			m.bindingsDeleteTarget = &b
			m.confirmCursor = 0
		}
		return m, nil
	case "enter":
		// Navigate to the resource in the Resources tab
		if m.bindingsCursor >= 0 && m.bindingsCursor < len(m.bindingItems) {
			b := m.bindingItems[m.bindingsCursor]
			navService := b.Binding.NavService()
			if navService != "" {
				return m, func() tea.Msg {
					return NavigateToResourceMsg{
						ServiceName: navService,
						ResourceID:  b.Binding.ResourceID,
					}
				}
			}
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) updateBindingsDelete(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "left", "h":
		if m.confirmCursor > 0 {
			m.confirmCursor--
		}
		return m, nil
	case "right", "l":
		if m.confirmCursor < 1 {
			m.confirmCursor++
		}
		return m, nil
	case "enter":
		if m.confirmCursor == 0 {
			// "No" selected — cancel
			m.mode = modeNormal
			m.bindingsDeleteTarget = nil
			return m, nil
		}
		// "Yes" selected — delete
		if m.bindingsDeleteTarget != nil {
			b := m.bindingsDeleteTarget
			return m, func() tea.Msg {
				return DeleteBindingMsg{
					ConfigPath:  b.ConfigPath,
					EnvName:     b.EnvName,
					BindingName: b.Binding.Name,
					BindingType: b.Binding.Type,
				}
			}
		}
		m.mode = modeNormal
		m.bindingsDeleteTarget = nil
		return m, nil
	case "esc":
		m.mode = modeNormal
		m.bindingsDeleteTarget = nil
		return m, nil
	}
	return m, nil
}

// --- Bindings View ---

func (m Model) viewBindings() []string {
	switch m.mode {
	case modeNormal:
		return m.viewBindingsList()
	case modeDelete:
		return m.viewBindingsDeleteConfirm()
	case modeAddBinding:
		return m.viewBindingsTypeSelector()
	case modeAddBindingForm:
		return m.viewBindingsForm()
	case modeAddBindingPicker:
		return m.viewBindingsPicker()
	}
	return nil
}

func (m Model) viewBindingsList() []string {
	var lines []string

	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %d binding(s)", len(m.bindingItems))))
	lines = append(lines, theme.SuccessStyle.Render("  + Add Binding (a)"))
	lines = append(lines, "")

	if len(m.bindingItems) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No bindings defined yet."))
		return lines
	}

	boxWidth := m.width - 6
	if boxWidth < 40 {
		boxWidth = 40
	}

	// Stacked per-environment sections
	prevEnv := ""
	for i, b := range m.bindingItems {
		if b.EnvName != prevEnv {
			if prevEnv != "" {
				lines = append(lines, "")
			}
			prevEnv = b.EnvName
			header := m.renderSectionHeader(b.EnvName, boxWidth)
			lines = append(lines, header)
		}

		cursor := "    "
		nameStyle := theme.NormalItemStyle
		if i == m.bindingsCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}

		// Type badge
		typeBadge := renderTypeBadge(b.Binding.TypeLabel())

		// Arrow for navigable bindings
		arrow := ""
		if b.Binding.NavService() != "" {
			arrow = " " + theme.ActionNavArrowStyle.Render("->")
		}

		// Resource ID
		resourceID := theme.DimStyle.Render(b.Binding.ResourceID)
		if len(b.Binding.ResourceID) > boxWidth-40 && boxWidth > 43 {
			resourceID = theme.DimStyle.Render(b.Binding.ResourceID[:boxWidth-43] + "...")
		}

		line := fmt.Sprintf("%s%s %s %s %s%s",
			cursor,
			typeBadge,
			nameStyle.Render(b.Binding.Name),
			theme.DimStyle.Render("→"),
			resourceID,
			arrow,
		)
		lines = append(lines, line)
	}

	return lines
}

func (m Model) viewBindingsDeleteConfirm() []string {
	if m.bindingsDeleteTarget == nil {
		return nil
	}
	b := m.bindingsDeleteTarget
	return viewDeleteConfirmBox(
		"Delete Binding",
		fmt.Sprintf("Remove %s [%s] from environment [%s]?", b.Binding.Name, b.Binding.TypeLabel(), b.EnvName),
		m.confirmCursor,
	)
}

// --- Bindings Help ---

func (m Model) helpBindings(base []HelpEntry) []HelpEntry {
	switch m.mode {
	case modeNormal:
		entries := append(base, HelpEntry{"j/k", "navigate"}, HelpEntry{"a", "add"})
		if len(m.bindingItems) > 0 {
			entries = append(entries, HelpEntry{"d", "delete"})
			entries = append(entries, HelpEntry{"enter", "open"})
		}
		return append(entries, HelpEntry{"q", "quit"})
	case modeDelete:
		return []HelpEntry{{"h/l", "select"}, {"enter", "confirm"}, {"esc", "cancel"}}
	case modeAddBinding:
		return []HelpEntry{{"j/k", "navigate"}, {"enter", "select"}, {"esc", "cancel"}}
	case modeAddBindingForm:
		return []HelpEntry{{"tab", "next field"}, {"shift+tab", "prev field"}, {"enter", "save"}, {"esc", "cancel"}}
	case modeAddBindingPicker:
		return []HelpEntry{{"j/k", "navigate"}, {"enter", "select"}, {"esc", "cancel"}}
	}
	return base
}

// --- Type badge rendering ---

func renderTypeBadge(label string) string {
	color := typeColor(label)
	return lipgloss.NewStyle().
		Foreground(theme.ColorWhite).
		Background(color).
		Padding(0, 0).
		Render(fmt.Sprintf("[%s]", label))
}

// --- Binding type catalog ---

// bindingTypeEntry describes a binding type available in the type selector.
type bindingTypeEntry struct {
	WriterType  string // writer key: "d1", "kv", "r2", "queue", "service", etc.
	Label       string // display label: "D1", "KV", "R2", "Queue", etc.
	Kind        string // "wizard" (D1/KV/R2/Queue), "form" (simple), "picker" (API list)
	Description string // short description for the type selector
}

// allBindingTypes is the ordered list of all supported binding types.
var allBindingTypes = []bindingTypeEntry{
	{"d1", "D1", "wizard", "D1 database"},
	{"kv", "KV", "wizard", "KV namespace"},
	{"r2", "R2", "wizard", "R2 bucket"},
	{"queue", "Queue", "wizard", "Queue producer"},
	{"service", "Service", "picker", "Worker service binding"},
	{"durable_object", "DO", "form", "Durable Object namespace"},
	{"ai", "AI", "form", "Workers AI binding (singleton)"},
	{"vectorize", "Vectorize", "picker", "Vectorize index"},
	{"hyperdrive", "Hyperdrive", "picker", "Hyperdrive config"},
	{"analytics_engine", "Analytics", "form", "Analytics Engine dataset"},
	{"browser", "Browser", "form", "Browser Rendering (singleton)"},
	{"images", "Images", "form", "Cloudflare Images (singleton)"},
	{"mtls_certificate", "mTLS", "picker", "mTLS certificate"},
	{"workflow", "Workflow", "form", "Workflow binding"},
	{"secrets_store_secret", "Secrets Store", "picker", "Secrets Store secret"},
}

// bindingFormField describes a single field in the inline binding form.
type bindingFormField struct {
	Name        string
	Placeholder string
	Required    bool
}

// bindingFormFields returns the field definitions for the inline form of a given type.
func bindingFormFields(writerType string) []bindingFormField {
	switch writerType {
	case "ai", "browser", "images":
		// Singletons: only binding name
		return []bindingFormField{{"binding", "AI (binding name)", true}}
	case "service":
		return []bindingFormField{
			{"binding", "MY_SERVICE (binding name)", true},
			{"service", "worker-name (target worker)", true},
			{"entrypoint", "MyEntrypoint (optional)", false},
		}
	case "durable_object":
		return []bindingFormField{
			{"binding", "MY_DO (binding name)", true},
			{"class_name", "MyDurableObject (class name)", true},
			{"script_name", "other-worker (optional, if external)", false},
		}
	case "vectorize":
		return []bindingFormField{
			{"binding", "MY_INDEX (binding name)", true},
			{"index_name", "my-index (index name)", true},
		}
	case "hyperdrive":
		return []bindingFormField{
			{"binding", "MY_HYPERDRIVE (binding name)", true},
			{"id", "config-id (hyperdrive config ID)", true},
		}
	case "analytics_engine":
		return []bindingFormField{
			{"binding", "MY_ANALYTICS (binding name)", true},
			{"dataset", "my-dataset (optional)", false},
		}
	case "mtls_certificate":
		return []bindingFormField{
			{"binding", "MY_CERT (binding name)", true},
			{"certificate_id", "cert-id (certificate ID)", true},
		}
	case "workflow":
		return []bindingFormField{
			{"binding", "MY_WORKFLOW (binding name)", true},
			{"name", "my-workflow (workflow name)", true},
			{"class_name", "MyWorkflow (must match exported class)", true},
			{"script_name", "other-worker (optional, if external)", false},
		}
	case "secrets_store_secret":
		return []bindingFormField{
			{"binding", "MY_SECRET (binding name)", true},
			{"store_id", "store-id (Secrets Store ID)", true},
			{"secret_name", "my-secret (secret name)", true},
		}
	}
	return nil
}

// pickerResourceType maps a writer type to the resource type string used in
// ListBindingResourcesMsg. Returns "" if the type doesn't use a picker.
func pickerResourceType(writerType string) string {
	switch writerType {
	case "service":
		return "service"
	case "vectorize":
		return "vectorize"
	case "hyperdrive":
		return "hyperdrive"
	case "mtls_certificate":
		return "mtls_certificate"
	case "secrets_store_secret":
		return "secrets_store"
	}
	return ""
}

// --- Type selector ---

func (m Model) updateBindingsTypeSelector(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.addBindingTypeCursor < len(allBindingTypes)-1 {
			m.addBindingTypeCursor++
		}
		return m, nil
	case "k", "up":
		if m.addBindingTypeCursor > 0 {
			m.addBindingTypeCursor--
		}
		return m, nil
	case "enter":
		if m.addBindingTypeCursor < 0 || m.addBindingTypeCursor >= len(allBindingTypes) {
			return m, nil
		}
		entry := allBindingTypes[m.addBindingTypeCursor]
		m.addBindingType = entry.WriterType

		switch entry.Kind {
		case "wizard":
			// D1/KV/R2/Queue — delegate to existing popup wizard
			m.mode = modeNormal
			configPath := m.configPath
			workerName := ""
			if m.config != nil {
				workerName = m.config.Name
			}
			envName := m.addBindingEnvName
			return m, func() tea.Msg {
				return OpenBindingsWizardMsg{
					ConfigPath: configPath,
					EnvName:    envName,
					WorkerName: workerName,
				}
			}
		case "form":
			// Simple form — set up text inputs
			m.initBindingForm(entry.WriterType)
			m.mode = modeAddBindingForm
			cmds := make([]tea.Cmd, 0, 1)
			if len(m.addBindingInputs) > 0 {
				cmds = append(cmds, m.addBindingInputs[0].Focus())
			}
			return m, tea.Batch(cmds...)
		case "picker":
			// Resource picker — fetch resources, then show picker
			m.initBindingForm(entry.WriterType)
			m.addBindingResources = nil
			m.addBindingResCursor = 0
			m.addBindingResLoading = true
			m.mode = modeAddBindingPicker
			resType := pickerResourceType(entry.WriterType)
			return m, func() tea.Msg {
				return ListBindingResourcesMsg{ResourceType: resType}
			}
		}
		return m, nil
	case "esc":
		m.mode = modeNormal
		return m, nil
	}
	return m, nil
}

func (m Model) viewBindingsTypeSelector() []string {
	var lines []string
	lines = append(lines, theme.TitleStyle.Render("  Add Binding"))
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Environment: %s", m.addBindingEnvName)))
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  Select binding type:"))
	lines = append(lines, "")

	for i, entry := range allBindingTypes {
		cursor := "    "
		nameStyle := theme.NormalItemStyle
		descStyle := theme.DimStyle
		if i == m.addBindingTypeCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}
		badge := renderTypeBadge(entry.Label)
		line := fmt.Sprintf("%s%s %s %s",
			cursor,
			badge,
			nameStyle.Render(entry.Label),
			descStyle.Render("- "+entry.Description),
		)
		lines = append(lines, line)
	}
	return lines
}

// --- Binding form (simple text inputs) ---

// initBindingForm sets up text inputs for the given binding type.
func (m *Model) initBindingForm(writerType string) {
	fields := bindingFormFields(writerType)
	m.addBindingInputs = make([]textinput.Model, len(fields))
	m.addBindingFieldNames = make([]string, len(fields))
	m.addBindingFocusField = 0

	for i, f := range fields {
		ti := textinput.New()
		ti.Placeholder = f.Placeholder
		ti.CharLimit = 256
		ti.Width = 50
		ti.Prompt = "  "
		ti.TextStyle = theme.ValueStyle
		ti.PlaceholderStyle = theme.DimStyle
		m.addBindingInputs[i] = ti
		m.addBindingFieldNames[i] = f.Name
	}
}

func (m Model) updateBindingsForm(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		return m, nil
	case "tab", "down":
		// Move to next field
		if m.addBindingFocusField < len(m.addBindingInputs)-1 {
			m.addBindingInputs[m.addBindingFocusField].Blur()
			m.addBindingFocusField++
			return m, m.addBindingInputs[m.addBindingFocusField].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		// Move to previous field
		if m.addBindingFocusField > 0 {
			m.addBindingInputs[m.addBindingFocusField].Blur()
			m.addBindingFocusField--
			return m, m.addBindingInputs[m.addBindingFocusField].Focus()
		}
		return m, nil
	case "enter":
		// Validate and submit
		return m.submitBindingForm()
	}

	// Forward to active input
	if m.addBindingFocusField < len(m.addBindingInputs) {
		var cmd tea.Cmd
		m.addBindingInputs[m.addBindingFocusField], cmd = m.addBindingInputs[m.addBindingFocusField].Update(msg)
		return m, cmd
	}
	return m, nil
}

// submitBindingForm validates and emits WriteDirectBindingMsg.
func (m Model) submitBindingForm() (Model, tea.Cmd) {
	fields := bindingFormFields(m.addBindingType)
	if fields == nil {
		m.errMsg = "Unknown binding type"
		return m, nil
	}

	// Validate required fields
	values := make(map[string]string)
	for i, f := range fields {
		val := strings.TrimSpace(m.addBindingInputs[i].Value())
		if f.Required && val == "" {
			m.errMsg = fmt.Sprintf("%s is required", f.Name)
			return m, nil
		}
		values[f.Name] = val
	}

	// Build BindingDef
	def := m.buildBindingDef(m.addBindingType, values)
	configPath := m.configPath
	envName := m.addBindingEnvName

	return m, func() tea.Msg {
		return WriteDirectBindingMsg{
			ConfigPath: configPath,
			EnvName:    envName,
			BindingDef: def,
		}
	}
}

// buildBindingDef constructs a wcfg.BindingDef from the form values.
func (m Model) buildBindingDef(writerType string, values map[string]string) wcfg.BindingDef {
	def := wcfg.BindingDef{
		Type:        writerType,
		BindingName: values["binding"],
	}

	switch writerType {
	case "ai", "browser", "images":
		// Singletons — binding name only
	case "service":
		def.ResourceName = values["service"]
		if ep := values["entrypoint"]; ep != "" {
			def.ExtraFields = map[string]string{"entrypoint": ep}
		}
	case "durable_object":
		def.ResourceID = values["class_name"]
		if sn := values["script_name"]; sn != "" {
			def.ExtraFields = map[string]string{"script_name": sn}
		}
	case "vectorize":
		def.ResourceName = values["index_name"]
	case "hyperdrive":
		def.ResourceID = values["id"]
	case "analytics_engine":
		if ds := values["dataset"]; ds != "" {
			def.ResourceName = ds
		}
	case "mtls_certificate":
		def.ResourceID = values["certificate_id"]
	case "workflow":
		def.ResourceName = values["name"]
		def.ResourceID = values["class_name"]
		if sn := values["script_name"]; sn != "" {
			def.ExtraFields = map[string]string{"script_name": sn}
		}
	case "secrets_store_secret":
		def.ResourceID = values["store_id"]
		def.ResourceName = values["secret_name"]
	}

	return def
}

func (m Model) viewBindingsForm() []string {
	var lines []string
	entry := m.currentTypeEntry()
	lines = append(lines, theme.TitleStyle.Render(fmt.Sprintf("  Add %s Binding", entry.Label)))
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Environment: %s", m.addBindingEnvName)))
	lines = append(lines, "")

	fields := bindingFormFields(m.addBindingType)
	for i, f := range fields {
		reqTag := ""
		if f.Required {
			reqTag = " *"
		}
		label := fmt.Sprintf("  %s%s:", f.Name, reqTag)
		if i == m.addBindingFocusField {
			lines = append(lines, theme.SelectedItemStyle.Render(label))
		} else {
			lines = append(lines, theme.LabelStyle.Render(label))
		}
		if i < len(m.addBindingInputs) {
			lines = append(lines, m.addBindingInputs[i].View())
		}
	}

	if m.errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, theme.ErrorStyle.Render("  "+m.errMsg))
	}

	return lines
}

// --- Binding resource picker ---

// SetBindingResources updates the resource list after async fetch completes.
func (m *Model) SetBindingResources(items []BindingResourceItem, err error) {
	m.addBindingResLoading = false
	if err != nil {
		m.errMsg = fmt.Sprintf("Failed to load resources: %v", err)
		return
	}
	m.addBindingResources = items
	m.addBindingResCursor = 0
}

func (m Model) updateBindingsPicker(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	// If still loading, only allow esc
	if m.addBindingResLoading {
		if key == "esc" {
			m.mode = modeNormal
			m.errMsg = ""
			return m, nil
		}
		return m, nil
	}

	// If no resources loaded but form inputs exist, fall through to form-like behavior
	// for types where the user can also type directly
	switch key {
	case "esc":
		m.mode = modeNormal
		m.errMsg = ""
		return m, nil
	case "j", "down":
		if m.addBindingResCursor < len(m.addBindingResources)-1 {
			m.addBindingResCursor++
		}
		return m, nil
	case "k", "up":
		if m.addBindingResCursor > 0 {
			m.addBindingResCursor--
		}
		return m, nil
	case "enter":
		if len(m.addBindingResources) == 0 {
			return m, nil
		}
		if m.addBindingResCursor < 0 || m.addBindingResCursor >= len(m.addBindingResources) {
			return m, nil
		}

		// Selected a resource — transition to form with pre-filled resource fields
		res := m.addBindingResources[m.addBindingResCursor]
		m.prefillBindingFormFromPicker(res)
		m.mode = modeAddBindingForm
		// Focus the first field (binding name)
		if len(m.addBindingInputs) > 0 {
			m.addBindingFocusField = 0
			return m, m.addBindingInputs[0].Focus()
		}
		return m, nil
	}
	return m, nil
}

// prefillBindingFormFromPicker fills the form inputs using the selected resource.
func (m *Model) prefillBindingFormFromPicker(res BindingResourceItem) {
	fields := bindingFormFields(m.addBindingType)
	if fields == nil {
		return
	}

	// Ensure inputs are initialized
	if len(m.addBindingInputs) != len(fields) {
		m.initBindingForm(m.addBindingType)
	}

	// Set binding name to a sensible default based on type and resource name
	defaultBinding := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(res.Name, "-", "_"), ".", "_"))

	switch m.addBindingType {
	case "service":
		// binding = uppercase(name), service = name
		m.setFormFieldValue("binding", defaultBinding)
		m.setFormFieldValue("service", res.Name)
	case "vectorize":
		m.setFormFieldValue("binding", defaultBinding)
		m.setFormFieldValue("index_name", res.Name)
	case "hyperdrive":
		m.setFormFieldValue("binding", defaultBinding)
		m.setFormFieldValue("id", res.ID)
	case "mtls_certificate":
		m.setFormFieldValue("binding", defaultBinding)
		m.setFormFieldValue("certificate_id", res.ID)
	case "secrets_store_secret":
		m.setFormFieldValue("binding", defaultBinding)
		m.setFormFieldValue("store_id", res.ID)
		m.setFormFieldValue("secret_name", res.Name)
	}
}

// setFormFieldValue sets a value on the form input matching the given field name.
func (m *Model) setFormFieldValue(fieldName, value string) {
	for i, name := range m.addBindingFieldNames {
		if name == fieldName && i < len(m.addBindingInputs) {
			m.addBindingInputs[i].SetValue(value)
			return
		}
	}
}

func (m Model) viewBindingsPicker() []string {
	var lines []string
	entry := m.currentTypeEntry()
	lines = append(lines, theme.TitleStyle.Render(fmt.Sprintf("  Add %s Binding", entry.Label)))
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Environment: %s", m.addBindingEnvName)))
	lines = append(lines, "")

	if m.addBindingResLoading {
		lines = append(lines, theme.DimStyle.Render("  Loading resources..."))
		return lines
	}

	if len(m.addBindingResources) == 0 {
		lines = append(lines, theme.DimStyle.Render("  No resources found."))
		lines = append(lines, theme.DimStyle.Render("  Press esc to go back."))
		return lines
	}

	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  Select a %s:", strings.ToLower(entry.Label))))
	lines = append(lines, "")

	for i, r := range m.addBindingResources {
		cursor := "    "
		nameStyle := theme.NormalItemStyle
		if i == m.addBindingResCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}
		detail := r.Name
		if r.ID != "" && r.ID != r.Name {
			detail = fmt.Sprintf("%s %s", r.Name, theme.DimStyle.Render("("+r.ID+")"))
		}
		lines = append(lines, fmt.Sprintf("%s%s", cursor, nameStyle.Render(detail)))
	}

	if m.errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, theme.ErrorStyle.Render("  "+m.errMsg))
	}

	return lines
}

// currentTypeEntry returns the bindingTypeEntry for m.addBindingType.
func (m Model) currentTypeEntry() bindingTypeEntry {
	for _, e := range allBindingTypes {
		if e.WriterType == m.addBindingType {
			return e
		}
	}
	return bindingTypeEntry{Label: m.addBindingType}
}

// --- Type badge rendering ---

func typeColor(label string) lipgloss.Color {
	switch label {
	case "KV":
		return theme.ColorBlue
	case "R2":
		return theme.ColorGreen
	case "D1":
		return theme.ColorOrange
	case "Service":
		return theme.ColorYellow
	case "DO":
		return theme.ColorRed
	case "AI":
		return lipgloss.Color("#A78BFA") // violet
	case "Queue (producer)", "Queue (consumer)":
		return lipgloss.Color("#F472B6") // pink
	case "Vectorize":
		return lipgloss.Color("#34D399") // emerald
	case "Hyperdrive":
		return lipgloss.Color("#60A5FA") // sky blue
	case "Analytics":
		return lipgloss.Color("#FBBF24") // amber
	case "Browser":
		return lipgloss.Color("#818CF8") // indigo
	case "Images":
		return lipgloss.Color("#FB923C") // light orange
	case "mTLS":
		return lipgloss.Color("#F87171") // light red
	case "Workflow":
		return lipgloss.Color("#2DD4BF") // teal
	case "Secrets Store":
		return lipgloss.Color("#C084FC") // purple
	default:
		return theme.ColorGray
	}
}
