package config

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/oarafat/orangeshell/internal/ui/confirmbox"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Category ---

// Category represents one of the four pill tabs.
type Category int

const (
	CategoryEnvVars      Category = iota // Environment Variables
	CategoryTriggers                     // Cron Triggers
	CategoryBindings                     // Bindings
	CategoryEnvironments                 // Environments
	categoryCount                        // sentinel for wraparound
)

func (c Category) label() string {
	switch c {
	case CategoryEnvVars:
		return "Env Variables"
	case CategoryTriggers:
		return "Triggers"
	case CategoryBindings:
		return "Bindings"
	case CategoryEnvironments:
		return "Environments"
	}
	return ""
}

func (c Category) zoneID() string {
	return fmt.Sprintf("cfg-cat-%d", c)
}

// --- Mode ---

type mode int

const (
	modeNormal    mode = iota // browsing list
	modeAdd                   // inline add flow
	modeEdit                  // inline edit flow
	modeDelete                // inline delete confirmation
	modeAddPreset             // triggers: preset picker
	modeAddCustom             // triggers: custom cron input
)

// --- ProjectEntry ---

// ProjectEntry holds a discovered wrangler project for the dropdown.
type ProjectEntry struct {
	Name       string
	ConfigPath string
	Config     *wcfg.WranglerConfig
}

// --- Messages emitted by this component ---

// SelectProjectMsg is emitted when the user selects a project from the dropdown.
type SelectProjectMsg struct {
	ConfigPath string
}

// NavigateToResourceMsg is emitted when the user presses enter on a navigable binding.
type NavigateToResourceMsg struct {
	ServiceName string // "KV", "R2", "D1", "Workers"
	ResourceID  string
}

// HelpEntry is a key-description pair for the help bar.
type HelpEntry struct {
	Key  string
	Desc string
}

// --- Model ---

// Model is the unified Configuration tab model.
type Model struct {
	width  int
	height int

	// Project dropdown
	projects       []ProjectEntry
	activeProject  int // index into projects, -1 if none
	dropdownOpen   bool
	dropdownCursor int

	// Category pill tabs
	activeCategory Category

	// Loaded config for active project
	config     *wcfg.WranglerConfig
	configPath string

	// --- Env Variables state ---
	envVars             []envVarItem // flat list across all envs
	envVarsCursor       int
	envVarsFilter       textinput.Model
	envVarsFilterActive bool
	envVarsScrollY      int

	// Env vars edit/add state
	evEditNameInput  textinput.Model
	evEditValueInput textinput.Model
	evEditEnvName    string   // target env for current edit/add
	evEditOrigName   string   // original var name (edit only)
	evAddFocusField  addField // which field is focused in add mode
	evEditEnvCursor  int      // env selector cursor in add mode
	evDeleteTarget   *envVarItem

	// --- Triggers state ---
	triggersCrons        []string
	triggersCursor       int
	triggersScrollY      int
	triggersPresetCursor int
	triggersCustomInput  textinput.Model
	triggersDeleteTarget string

	// --- Bindings state ---
	bindingItems         []bindingItem // flat list across all envs
	bindingsCursor       int
	bindingsScrollY      int
	bindingsDeleteTarget *bindingItem

	// --- Environments state ---
	envsList         []string
	envsCursor       int
	envsScrollY      int
	envsAddInput     textinput.Model
	envsDeleteTarget string

	// Current mode for the active category
	mode          mode
	errMsg        string
	confirmCursor int // 0 = No (default safe), 1 = Yes — used by delete confirmations
}

// addField tracks which field has focus in env var add mode.
type addField int

const (
	addFieldEnv addField = iota
	addFieldName
	addFieldValue
)

// --- Data types ---

type envVarItem struct {
	EnvName    string
	Name       string
	Value      string
	ConfigPath string
}

type bindingItem struct {
	EnvName    string
	Binding    wcfg.Binding
	ConfigPath string
}

// --- Constructor ---

// New creates a new Configuration tab model.
func New() Model {
	fi := textinput.New()
	fi.Placeholder = "Type to filter..."
	fi.CharLimit = 100
	fi.Width = 40
	fi.Prompt = "> "
	fi.PromptStyle = theme.SelectedItemStyle
	fi.TextStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
	fi.PlaceholderStyle = theme.DimStyle

	ni := textinput.New()
	ni.Placeholder = "VARIABLE_NAME"
	ni.CharLimit = 128
	ni.Width = 40
	ni.Prompt = "  "
	ni.TextStyle = theme.ValueStyle
	ni.PlaceholderStyle = theme.DimStyle

	vi := textinput.New()
	vi.Placeholder = "value"
	vi.CharLimit = 1024
	vi.Width = 60
	vi.Prompt = "  "
	vi.TextStyle = theme.ValueStyle
	vi.PlaceholderStyle = theme.DimStyle

	ci := textinput.New()
	ci.Placeholder = "*/5 * * * *"
	ci.CharLimit = 50
	ci.Width = 40

	ei := textinput.New()
	ei.Placeholder = "environment-name"
	ei.CharLimit = 64
	ei.Width = 40
	ei.Prompt = "  "
	ei.TextStyle = theme.ValueStyle
	ei.PlaceholderStyle = theme.DimStyle

	return Model{
		activeProject:       -1,
		activeCategory:      CategoryEnvVars,
		envVarsFilter:       fi,
		evEditNameInput:     ni,
		evEditValueInput:    vi,
		triggersCustomInput: ci,
		envsAddInput:        ei,
	}
}

// --- Setters ---

// SetSize updates the dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetProjects sets the available projects for the dropdown.
func (m *Model) SetProjects(projects []ProjectEntry) {
	m.projects = projects
	// If the active project was removed, clear selection
	if m.activeProject >= len(projects) {
		m.activeProject = -1
		m.config = nil
		m.configPath = ""
	}
	// Auto-select if only one project and none selected
	if m.activeProject == -1 && len(projects) == 1 {
		m.selectProject(0)
	}
}

// SelectProjectByPath selects the project with the given config path.
// Returns true if found.
func (m *Model) SelectProjectByPath(configPath string) bool {
	for i, p := range m.projects {
		if p.ConfigPath == configPath {
			m.selectProject(i)
			return true
		}
	}
	return false
}

// SetCategory switches to the given category.
func (m *Model) SetCategory(cat Category) {
	m.activeCategory = cat
	m.mode = modeNormal
	m.errMsg = ""
}

// ActiveCategory returns the currently active category.
func (m Model) ActiveCategory() Category {
	return m.activeCategory
}

// DropdownOpen returns whether the project dropdown is open.
func (m Model) DropdownOpen() bool {
	return m.dropdownOpen
}

// IsTextInputActive returns true when the config model is in a mode where
// printable keys (including numbers 1-4) should go to a text input rather
// than being intercepted as tab-switch shortcuts.
func (m Model) IsTextInputActive() bool {
	switch m.mode {
	case modeEdit, modeAdd, modeAddCustom:
		return true
	}
	if m.envVarsFilterActive {
		return true
	}
	return false
}

// HasProject returns whether a project is selected.
func (m Model) HasProject() bool {
	return m.activeProject >= 0 && m.activeProject < len(m.projects)
}

// ConfigPath returns the active project's config path.
func (m Model) ConfigPath() string {
	return m.configPath
}

// Config returns the active project's parsed wrangler config.
func (m Model) Config() *wcfg.WranglerConfig {
	return m.config
}

// --- Internal helpers ---

func (m *Model) selectProject(idx int) {
	if idx < 0 || idx >= len(m.projects) {
		return
	}
	m.activeProject = idx
	p := m.projects[idx]
	m.configPath = p.ConfigPath
	m.config = p.Config
	m.loadConfigData()
}

// loadConfigData rebuilds all category data from the active config.
func (m *Model) loadConfigData() {
	cfg := m.config
	if cfg == nil {
		m.envVars = nil
		m.triggersCrons = nil
		m.bindingItems = nil
		m.envsList = nil
		return
	}

	// Env vars
	m.envVars = m.buildEnvVars(cfg)
	m.envVarsCursor = 0
	m.envVarsScrollY = 0
	m.envVarsFilter.SetValue("")
	m.envVarsFilterActive = false

	// Triggers
	m.triggersCrons = cfg.CronTriggers()
	m.triggersCursor = 0
	m.triggersScrollY = 0

	// Bindings
	m.bindingItems = m.buildBindings(cfg)
	m.bindingsCursor = 0
	m.bindingsScrollY = 0

	// Environments
	m.envsList = cfg.EnvNames()
	m.envsCursor = 0
	m.envsScrollY = 0

	// Reset mode
	m.mode = modeNormal
	m.errMsg = ""
}

func (m Model) buildEnvVars(cfg *wcfg.WranglerConfig) []envVarItem {
	var result []envVarItem
	for _, envName := range cfg.EnvNames() {
		vars := cfg.EnvVars(envName)
		for name, value := range vars {
			result = append(result, envVarItem{
				EnvName:    envName,
				Name:       name,
				Value:      value,
				ConfigPath: m.configPath,
			})
		}
	}
	sortEnvVars(result)
	return result
}

func (m Model) buildBindings(cfg *wcfg.WranglerConfig) []bindingItem {
	var result []bindingItem
	for _, envName := range cfg.EnvNames() {
		bindings := cfg.EnvBindings(envName)
		for _, b := range bindings {
			result = append(result, bindingItem{
				EnvName:    envName,
				Binding:    b,
				ConfigPath: m.configPath,
			})
		}
	}
	sortBindings(result)
	return result
}

// ReloadConfig re-parses the active config and refreshes all data.
// Also resets mode back to normal (used after successful mutations).
func (m *Model) ReloadConfig() {
	if m.configPath == "" {
		return
	}
	cfg, err := wcfg.Parse(m.configPath)
	if err != nil {
		return
	}
	m.config = cfg
	// Also update the cached config in the projects list
	if m.activeProject >= 0 && m.activeProject < len(m.projects) {
		m.projects[m.activeProject].Config = cfg
	}

	// Preserve cursors where possible
	oldEnvCursor := m.envVarsCursor
	oldTrigCursor := m.triggersCursor
	oldBindCursor := m.bindingsCursor
	oldEnvsCursor := m.envsCursor

	m.loadConfigData()

	// Restore cursors (clamped)
	m.envVarsCursor = clamp(oldEnvCursor, 0, len(m.envVars)-1)
	m.triggersCursor = clamp(oldTrigCursor, 0, len(m.triggersCrons)-1)
	m.bindingsCursor = clamp(oldBindCursor, 0, len(m.bindingItems)-1)
	m.envsCursor = clamp(oldEnvsCursor, 0, len(m.envsList)-1)

	// Reset mode — mutation succeeded, return to browsing
	m.mode = modeNormal
	m.errMsg = ""
}

// SetError sets an error message and resets mode to normal.
// Used when a mutation command fails (e.g., save/delete returns an error).
func (m *Model) SetError(msg string) {
	m.errMsg = msg
	m.mode = modeNormal
}

// --- Update ---

// Update handles messages for the Configuration tab.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Dropdown takes priority when open
		if m.dropdownOpen {
			return m.updateDropdown(msg)
		}
		// No project selected — 's' opens dropdown
		if !m.HasProject() {
			return m.updateNoProject(msg)
		}
		// Route to active category
		return m.updateCategory(msg)

	case tea.MouseMsg:
		return m.updateMouse(msg)
	}

	// Forward non-key messages to text inputs for cursor blink
	var cmd tea.Cmd
	switch m.activeCategory {
	case CategoryEnvVars:
		cmd = m.forwardToEnvVarInputs(msg)
	case CategoryTriggers:
		if m.mode == modeAddCustom {
			m.triggersCustomInput, cmd = m.triggersCustomInput.Update(msg)
		}
	case CategoryEnvironments:
		if m.mode == modeAdd {
			m.envsAddInput, cmd = m.envsAddInput.Update(msg)
		}
	}
	return m, cmd
}

func (m Model) forwardToEnvVarInputs(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.mode {
	case modeNormal:
		if m.envVarsFilterActive {
			m.envVarsFilter, cmd = m.envVarsFilter.Update(msg)
		}
	case modeEdit:
		m.evEditValueInput, cmd = m.evEditValueInput.Update(msg)
	case modeAdd:
		switch m.evAddFocusField {
		case addFieldName:
			m.evEditNameInput, cmd = m.evEditNameInput.Update(msg)
		case addFieldValue:
			m.evEditValueInput, cmd = m.evEditValueInput.Update(msg)
		}
	}
	return cmd
}

// updateNoProject handles keys when no project is selected.
func (m Model) updateNoProject(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "s", "enter":
		if len(m.projects) > 0 {
			m.openDropdown()
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// --- Dropdown ---

func (m *Model) openDropdown() {
	m.dropdownOpen = true
	m.dropdownCursor = 0
	if m.activeProject >= 0 {
		m.dropdownCursor = m.activeProject
	}
}

func (m *Model) closeDropdown() {
	m.dropdownOpen = false
}

func (m Model) updateDropdown(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "s":
		m.closeDropdown()
		return m, nil
	case "j", "down":
		if m.dropdownCursor < len(m.projects)-1 {
			m.dropdownCursor++
		}
		return m, nil
	case "k", "up":
		if m.dropdownCursor > 0 {
			m.dropdownCursor--
		}
		return m, nil
	case "enter":
		if m.dropdownCursor >= 0 && m.dropdownCursor < len(m.projects) {
			m.selectProject(m.dropdownCursor)
			m.closeDropdown()
		}
		return m, nil
	}
	return m, nil
}

// --- Category routing ---

func (m Model) updateCategory(msg tea.KeyMsg) (Model, tea.Cmd) {
	key := msg.String()

	// Category switching with h/l (only in normal mode)
	if m.mode == modeNormal {
		switch key {
		case "h", "left":
			m.activeCategory = (m.activeCategory - 1 + categoryCount) % categoryCount
			m.mode = modeNormal
			m.errMsg = ""
			return m, nil
		case "l", "right":
			m.activeCategory = (m.activeCategory + 1) % categoryCount
			m.mode = modeNormal
			m.errMsg = ""
			return m, nil
		case "s":
			if len(m.projects) > 0 {
				m.openDropdown()
			}
			return m, nil
		}
	}

	switch m.activeCategory {
	case CategoryEnvVars:
		return m.updateEnvVars(msg)
	case CategoryTriggers:
		return m.updateTriggers(msg)
	case CategoryBindings:
		return m.updateBindings(msg)
	case CategoryEnvironments:
		return m.updateEnvironments(msg)
	}
	return m, nil
}

// --- Mouse ---

func (m Model) updateMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionRelease {
		return m, nil
	}

	// Check project dropdown header click
	if zone.Get("cfg-dropdown").InBounds(msg) {
		if m.dropdownOpen {
			m.closeDropdown()
		} else if len(m.projects) > 0 {
			m.openDropdown()
		}
		return m, nil
	}

	// Check dropdown item clicks
	if m.dropdownOpen {
		for i := range m.projects {
			if zone.Get(fmt.Sprintf("cfg-dd-%d", i)).InBounds(msg) {
				m.selectProject(i)
				m.closeDropdown()
				return m, nil
			}
		}
	}

	// Check category pill clicks
	for i := 0; i < int(categoryCount); i++ {
		cat := Category(i)
		if zone.Get(cat.zoneID()).InBounds(msg) {
			m.activeCategory = cat
			m.mode = modeNormal
			m.errMsg = ""
			return m, nil
		}
	}

	return m, nil
}

// --- View ---

// View renders the entire Configuration tab.
func (m Model) View() string {
	contentHeight := m.height
	if contentHeight < 1 {
		contentHeight = 1
	}

	if !m.HasProject() && len(m.projects) == 0 {
		// No projects discovered
		return m.viewNoProjects(contentHeight)
	}

	var sections []string

	// Project dropdown header
	sections = append(sections, m.viewDropdownHeader())

	// Dropdown overlay (if open)
	if m.dropdownOpen {
		sections = append(sections, m.viewDropdownList())
		content := strings.Join(sections, "\n")
		return m.padToHeight(content, contentHeight)
	}

	if !m.HasProject() {
		// Projects exist but none selected
		sections = append(sections, "")
		sections = append(sections, theme.DimStyle.Render("  Press s or enter to select a project"))
		content := strings.Join(sections, "\n")
		return m.padToHeight(content, contentHeight)
	}

	// Category pills
	sections = append(sections, "")
	sections = append(sections, m.viewCategoryPills())
	sections = append(sections, "")

	// Active category content
	switch m.activeCategory {
	case CategoryEnvVars:
		sections = append(sections, m.viewEnvVars()...)
	case CategoryTriggers:
		sections = append(sections, m.viewTriggers()...)
	case CategoryBindings:
		sections = append(sections, m.viewBindings()...)
	case CategoryEnvironments:
		sections = append(sections, m.viewEnvironments()...)
	}

	// Error
	if m.errMsg != "" {
		sections = append(sections, "")
		sections = append(sections, theme.ErrorStyle.Render("  "+m.errMsg))
	}

	content := strings.Join(sections, "\n")
	return m.padToHeight(content, contentHeight)
}

func (m Model) viewNoProjects(height int) string {
	title := theme.TitleStyle.Render("Configuration")
	subtitle := theme.DimStyle.Render("No wrangler projects found. Add a project from the Operations tab.")
	block := lipgloss.JoinVertical(lipgloss.Center, title, "", subtitle)
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, block)
}

func (m Model) viewDropdownHeader() string {
	var label string
	if m.HasProject() {
		p := m.projects[m.activeProject]
		if m.dropdownOpen {
			label = theme.SelectedItemStyle.Render(fmt.Sprintf("  ▼ %s", p.Name))
		} else {
			label = theme.TitleStyle.Render(fmt.Sprintf("  ▼ %s", p.Name))
		}
	} else {
		label = theme.DimStyle.Render("  ▶ Select a project")
	}
	return zone.Mark("cfg-dropdown", label)
}

func (m Model) viewDropdownList() string {
	var lines []string
	for i, p := range m.projects {
		cursor := "    "
		nameStyle := theme.NormalItemStyle
		if i == m.dropdownCursor {
			cursor = theme.SelectedItemStyle.Render("  > ")
			nameStyle = theme.SelectedItemStyle
		}
		line := zone.Mark(
			fmt.Sprintf("cfg-dd-%d", i),
			fmt.Sprintf("%s%s", cursor, nameStyle.Render(p.Name)),
		)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewCategoryPills() string {
	var pills []string
	for i := 0; i < int(categoryCount); i++ {
		cat := Category(i)
		pill := m.renderPill(cat.label(), cat == m.activeCategory)
		pills = append(pills, zone.Mark(cat.zoneID(), pill))
	}
	return "  " + lipgloss.JoinHorizontal(lipgloss.Top, pills...)
}

func (m Model) renderPill(label string, active bool) string {
	if active {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.ColorWhite).
			Background(theme.ColorOrange).
			Padding(0, 1).
			MarginRight(1).
			Render(label)
	}
	return lipgloss.NewStyle().
		Foreground(theme.ColorGray).
		Padding(0, 1).
		MarginRight(1).
		Render(label)
}

func (m Model) padToHeight(content string, height int) string {
	lines := strings.Split(content, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// --- HelpEntries ---

// HelpEntries returns context-sensitive help for the bottom bar.
func (m Model) HelpEntries() []HelpEntry {
	if m.dropdownOpen {
		return []HelpEntry{
			{"j/k", "navigate"},
			{"enter", "select"},
			{"esc", "close"},
		}
	}

	if !m.HasProject() {
		entries := []HelpEntry{
			{"s", "project"},
		}
		if len(m.projects) > 0 {
			entries = append(entries, HelpEntry{"enter", "select"})
		}
		entries = append(entries, HelpEntry{"q", "quit"})
		return entries
	}

	base := []HelpEntry{
		{"s", "project"},
		{"h/l", "category"},
	}

	switch m.activeCategory {
	case CategoryEnvVars:
		return m.helpEnvVars(base)
	case CategoryTriggers:
		return m.helpTriggers(base)
	case CategoryBindings:
		return m.helpBindings(base)
	case CategoryEnvironments:
		return m.helpEnvironments(base)
	}

	return append(base, HelpEntry{"q", "quit"})
}

// --- Sorting helpers ---

func sortEnvVars(items []envVarItem) {
	// Sort: default env first, then alphabetical by env name, then by var name
	n := len(items)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && lessEnvVar(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func lessEnvVar(a, b envVarItem) bool {
	if a.EnvName != b.EnvName {
		if a.EnvName == "default" {
			return true
		}
		if b.EnvName == "default" {
			return false
		}
		return a.EnvName < b.EnvName
	}
	return a.Name < b.Name
}

func sortBindings(items []bindingItem) {
	n := len(items)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && lessBinding(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func lessBinding(a, b bindingItem) bool {
	if a.EnvName != b.EnvName {
		if a.EnvName == "default" {
			return true
		}
		if b.EnvName == "default" {
			return false
		}
		return a.EnvName < b.EnvName
	}
	return a.Binding.Name < b.Binding.Name
}

func clamp(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max && max >= min {
		return max
	}
	return val
}

// viewDeleteConfirmBox renders a styled delete confirmation box using the
// shared confirmbox component. Returns a slice of lines for embedding in
// the category's view output.
func viewDeleteConfirmBox(title, detail string, cursor int) []string {
	box := confirmbox.Render(confirmbox.Params{
		Title:    "  " + title,
		Body:     []string{theme.DimStyle.Render("  " + detail)},
		Buttons:  confirmbox.ButtonsCursor,
		Cursor:   cursor,
		HelpText: "  esc cancel  |  enter confirm  |  h/l select",
	})
	return []string{"", box}
}
