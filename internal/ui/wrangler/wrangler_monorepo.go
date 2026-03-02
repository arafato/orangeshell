package wrangler

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// SetProjects sets up the monorepo project list, parsing each config.
func (m *Model) SetProjects(projects []wcfg.ProjectInfo, rootName, rootDir string) {
	m.configLoading = false
	m.rootName = rootName
	m.rootDir = rootDir

	// Use rootDir for relative paths if available, otherwise fall back to CWD
	baseDir := rootDir
	if baseDir == "" {
		baseDir, _ = filepath.Abs(".")
	}

	m.projects = make([]projectEntry, len(projects))
	for i, p := range projects {
		cfg, err := wcfg.Parse(p.ConfigPath)

		// Compute relative path from root dir
		relPath, _ := filepath.Rel(baseDir, p.Dir)
		if relPath == "" {
			relPath = "."
		}

		name := filepath.Base(p.Dir)

		box := ProjectBox{
			Name:              name,
			RelPath:           relPath,
			Config:            cfg,
			Err:               err,
			Deployments:       make(map[string]*DeploymentDisplay),
			DeploymentFetched: make(map[string]bool),
			Index:             i,
		}

		m.projects[i] = projectEntry{
			box:        box,
			config:     cfg,
			configPath: p.ConfigPath,
			gitInfo:    p.Git,
		}
	}

	m.projectCursor = 0
	m.projectScrollY = 0
	m.activeProject = -1
}

// SetProjectDeployment updates deployment data for a specific project and environment.
// If the user is currently drilled into this project, the live envBoxes are also updated
// so the UI reflects the change immediately.
func (m *Model) SetProjectDeployment(projectIndex int, envName string, dep *DeploymentDisplay, subdomain string) {
	if projectIndex < 0 || projectIndex >= len(m.projects) {
		return
	}
	m.projects[projectIndex].box.Deployments[envName] = dep
	if m.projects[projectIndex].box.DeploymentFetched == nil {
		m.projects[projectIndex].box.DeploymentFetched = make(map[string]bool)
	}
	m.projects[projectIndex].box.DeploymentFetched[envName] = true
	if subdomain != "" {
		m.projects[projectIndex].box.Subdomain = subdomain
	}

	// Sync to live envBoxes when drilled into this project
	if m.activeProject == projectIndex {
		for i := range m.envBoxes {
			if m.envBoxes[i].EnvName == envName {
				m.envBoxes[i].Deployment = dep
				m.envBoxes[i].DeploymentFetched = true
				if subdomain != "" {
					m.envBoxes[i].Subdomain = subdomain
				}
				break
			}
		}
	}
}

// ProjectConfigs returns (config, configPath) pairs for all projects.
// Used by app.go to schedule deployment fetches.
func (m Model) ProjectConfigs() [](struct {
	Config     *wcfg.WranglerConfig
	ConfigPath string
}) {
	result := make([](struct {
		Config     *wcfg.WranglerConfig
		ConfigPath string
	}), len(m.projects))
	for i, p := range m.projects {
		result[i].Config = p.config
		result[i].ConfigPath = p.configPath
	}
	return result
}

// ProjectDirNames returns the directory basenames of all projects in the monorepo.
func (m Model) ProjectDirNames() []string {
	names := make([]string, len(m.projects))
	for i, p := range m.projects {
		names[i] = p.box.Name
	}
	return names
}

// AllEnvNames returns the union of env names across all monorepo projects.
// Returns nil in single-project mode.
func (m Model) AllEnvNames() []string {
	if !m.IsMonorepo() {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	for _, p := range m.projects {
		if p.config == nil {
			continue
		}
		for _, name := range p.config.EnvNames() {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

// WorkerList returns a flat list of all known workers across all projects/environments.
// In monorepo mode, it iterates all projects. In single-project mode, it uses the loaded config.
func (m Model) WorkerList() []WorkerInfo {
	if m.IsMonorepo() {
		var workers []WorkerInfo
		for _, p := range m.projects {
			if p.config == nil {
				continue
			}
			projectName := p.box.Name
			for _, envName := range p.config.EnvNames() {
				scriptName := p.config.ResolvedEnvName(envName)
				if scriptName != "" {
					workers = append(workers, WorkerInfo{
						ProjectName: projectName,
						EnvName:     envName,
						ScriptName:  scriptName,
					})
				}
			}
		}
		return workers
	}
	// Single project mode
	if m.config == nil {
		return nil
	}
	projectName := m.config.Name
	var workers []WorkerInfo
	for _, envName := range m.config.EnvNames() {
		scriptName := m.config.ResolvedEnvName(envName)
		if scriptName != "" {
			workers = append(workers, WorkerInfo{
				ProjectName: projectName,
				EnvName:     envName,
				ScriptName:  scriptName,
			})
		}
	}
	return workers
}

// updateProjectList handles 2D grid navigation on the monorepo project list.
// Projects are arranged in a 2-column grid. The linear cursor maps to:
//
//	column = cursor % 2, row = cursor / 2
func (m Model) updateProjectList(msg tea.KeyMsg) (Model, tea.Cmd) {
	n := len(m.projects)
	switch msg.String() {
	case "up", "k":
		// Move up one row (stride of 2)
		if m.projectCursor-2 >= 0 {
			m.projectCursor -= 2
			m.adjustProjectScroll()
		}
	case "down", "j":
		// Move down one row (stride of 2)
		if m.projectCursor+2 < n {
			m.projectCursor += 2
			m.adjustProjectScroll()
		} else if m.projectCursor+2 >= n && m.projectCursor%2 == 0 && m.projectCursor+1 < n {
			// On left column of last full row, but there's an item on the right in this row
			// and no row below — stay put
		}
	case "left", "h":
		// Move to left column if currently on the right
		if m.projectCursor%2 == 1 {
			m.projectCursor--
			m.adjustProjectScroll()
		}
	case "right", "l":
		// Move to right column if currently on the left and right exists
		if m.projectCursor%2 == 0 && m.projectCursor+1 < n {
			m.projectCursor++
			m.adjustProjectScroll()
		}
	case "enter":
		if m.projectCursor >= 0 && m.projectCursor < n {
			return m.drillIntoProject(m.projectCursor)
		}
	}
	return m, nil
}

// drillIntoProject sets the active project and switches to the single-project view.
func (m Model) drillIntoProject(idx int) (Model, tea.Cmd) {
	entry := m.projects[idx]
	m.activeProject = idx

	// Load this project's config into the single-project view fields
	m.config = entry.config
	m.configPath = entry.configPath
	m.configErr = nil
	m.focusedEnv = 0
	m.insideBox = false
	m.scrollY = 0

	if entry.config != nil {
		m.envNames = entry.config.EnvNames()
		m.envBoxes = make([]EnvBox, len(m.envNames))
		for i, name := range m.envNames {
			m.envBoxes[i] = NewEnvBox(entry.config, name, i)
			// Copy deployment data from the project box into the env box
			if dep, ok := entry.box.Deployments[name]; ok {
				m.envBoxes[i].Deployment = dep
			}
			if entry.box.DeploymentFetched[name] {
				m.envBoxes[i].DeploymentFetched = true
			}
			if entry.box.Subdomain != "" {
				m.envBoxes[i].Subdomain = entry.box.Subdomain
			}
			// Copy Access protection badge from the project box
			if entry.box.AccessBadges[name] {
				m.envBoxes[i].AccessProtected = true
			}
			// Copy CI/CD badge from the project box
			if entry.box.CICDBadges[name] {
				m.envBoxes[i].CICDConnected = true
			}
		}
	} else {
		m.envNames = nil
		m.envBoxes = nil
	}

	return m, nil
}

// adjustProjectScroll ensures the focused project row is visible in the scroll window.
// projectScrollY is a line offset. We estimate ~12 lines per grid row (box height + spacer)
// and convert between row index and line offset.
func (m *Model) adjustProjectScroll() {
	const estRowHeight = 12 // estimated lines per grid row (box + spacer)
	const headerLines = 3   // title + separator + blank line

	focusedRow := m.projectCursor / 2

	// Estimate the line offset where this row starts
	rowStartLine := headerLines + focusedRow*estRowHeight
	rowEndLine := rowStartLine + estRowHeight

	// How many content lines are visible
	visibleHeight := m.height - 4 // approximate content area
	if visibleHeight < estRowHeight {
		visibleHeight = estRowHeight
	}

	// If focused row is above the current scroll window, scroll up
	if rowStartLine < m.projectScrollY {
		m.projectScrollY = rowStartLine
	}
	// If focused row is below the current scroll window, scroll down
	if rowEndLine > m.projectScrollY+visibleHeight {
		m.projectScrollY = rowEndLine - visibleHeight
	}

	if m.projectScrollY < 0 {
		m.projectScrollY = 0
	}
}

// viewProjectList renders the monorepo project list view as a 2-column grid.
func (m Model) viewProjectList(contentHeight, boxWidth int, title, sep string) string {
	// Monorepo title uses the root name with project count inline
	monoTitle := theme.TitleStyle.Render(fmt.Sprintf("  %s", m.rootName)) +
		"  " + theme.DimStyle.Render(fmt.Sprintf("%d projects", len(m.projects)))

	helpText := theme.DimStyle.Render("  h/j/k/l navigate  |  enter drill into  |  ctrl+p actions  |  ctrl+l services")

	n := len(m.projects)
	totalRows := (n + 1) / 2 // ceiling division
	colWidth := boxWidth / 2

	// Build all lines by rendering each grid row
	var allLines []string
	allLines = append(allLines, monoTitle, sep, "")

	// Track the starting line index for each grid row (for scroll adjustment)
	rowLineOffsets := make([]int, totalRows)
	for row := 0; row < totalRows; row++ {
		rowLineOffsets[row] = len(allLines)

		leftIdx := row * 2
		rightIdx := leftIdx + 1

		leftFocused := leftIdx == m.projectCursor
		leftView := zone.Mark(ProjectBoxZoneID(leftIdx), m.projects[leftIdx].box.View(colWidth, leftFocused))

		var rightView string
		if rightIdx < n {
			rightFocused := rightIdx == m.projectCursor
			rightView = zone.Mark(ProjectBoxZoneID(rightIdx), m.projects[rightIdx].box.View(colWidth, rightFocused))
		} else {
			// Empty placeholder for odd count — match the left box height
			leftLines := strings.Split(leftView, "\n")
			placeholder := strings.Repeat("\n", len(leftLines)-1)
			rightView = lipgloss.NewStyle().Width(colWidth).Render(placeholder)
		}

		rowView := lipgloss.JoinHorizontal(lipgloss.Top, leftView, rightView)
		rowLines := strings.Split(rowView, "\n")
		allLines = append(allLines, rowLines...)
		allLines = append(allLines, "") // spacer between rows
	}

	allLines = append(allLines, helpText)

	// Apply vertical scrolling (projectScrollY is a line offset)
	visibleHeight := contentHeight
	if visibleHeight < 1 {
		visibleHeight = 1
	}
	maxScroll := len(allLines) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	offset := m.projectScrollY
	if offset > maxScroll {
		offset = maxScroll
	}
	endIdx := offset + visibleHeight
	if endIdx > len(allLines) {
		endIdx = len(allLines)
	}

	visible := allLines[offset:endIdx]

	// Pad to exact height
	for len(visible) < contentHeight {
		visible = append(visible, "")
	}

	content := strings.Join(visible, "\n")
	return m.renderBorder(content, contentHeight)
}
