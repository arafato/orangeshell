package detail

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// SetServices sets the available services for the dropdown selector.
func (m *Model) SetServices(services []ServiceEntry) {
	m.services = services
}

// SetManagedResources sets the set of resource IDs that are wrangler-managed.
// Resources in this set are rendered in white; others in dim/gray.
// Re-sorts the resources slice so managed items appear first, preserving cursor
// on the same resource.
func (m *Model) SetManagedResources(ids map[string]bool) {
	// When no resources are managed (empty map), there's nothing to
	// distinguish — disable color coding so all items appear normal/white.
	if ids != nil && len(ids) == 0 {
		ids = nil
	}
	m.managedIDs = ids
	if ids == nil || len(m.resources) == 0 {
		m.managedCount = 0
		return
	}

	// Remember currently selected resource to restore cursor after sort.
	// Prefer the pending navigate target if set (cross-tab navigation in flight).
	var selectedID string
	if m.pendingNavigateID != "" {
		selectedID = m.pendingNavigateID
	} else if m.cursor >= 0 && m.cursor < len(m.resources) {
		selectedID = m.resources[m.cursor].ID
	}

	// Copy the slice before sorting to avoid mutating shared cache data
	sorted := make([]service.Resource, len(m.resources))
	copy(sorted, m.resources)
	m.resources = sorted

	// Stable sort: managed first, then unmanaged, preserving order within each group
	sort.SliceStable(m.resources, func(i, j int) bool {
		iManaged := ids[m.resources[i].ID]
		jManaged := ids[m.resources[j].ID]
		if iManaged != jManaged {
			return iManaged // managed before unmanaged
		}
		return false // preserve original order within group
	})

	// Count managed items
	m.managedCount = 0
	for _, r := range m.resources {
		if ids[r.ID] {
			m.managedCount++
		} else {
			break
		}
	}

	// Restore cursor position (match by ID, then fall back to Name for bindings
	// that store a resource name rather than a UUID, e.g. Queues)
	if selectedID != "" {
		for i, r := range m.resources {
			if r.ID == selectedID {
				m.cursor = i
				m.pendingNavigateID = "" // clear if it was the nav target
				return
			}
		}
		for i, r := range m.resources {
			if r.Name == selectedID {
				m.cursor = i
				m.pendingNavigateID = "" // clear if it was the nav target
				return
			}
		}
	}
}

// IsManaged returns whether a resource ID is wrangler-managed.
func (m Model) IsManaged(resourceID string) bool {
	if m.managedIDs == nil {
		return false
	}
	return m.managedIDs[resourceID]
}

// DropdownOpen returns whether the service dropdown is currently open.
func (m Model) DropdownOpen() bool {
	return m.dropdownOpen
}

// OpenDropdown opens the service dropdown. If a service is already selected,
// positions the cursor on it.
func (m *Model) OpenDropdown() {
	m.dropdownOpen = true
	m.dropdownCursor = 0
	for i, s := range m.services {
		if s.Name == m.service {
			m.dropdownCursor = i
			break
		}
	}
}

// CloseDropdown closes the service dropdown without changing the selection.
func (m *Model) CloseDropdown() {
	m.dropdownOpen = false
}

// Focus returns which pane currently has keyboard focus.
func (m Model) Focus() DetailFocus {
	return m.focus
}

// Interacting returns whether the detail pane is in interactive mode
// (user pressed enter/tab to engage with the detail view for scrolling or editing).
func (m Model) Interacting() bool {
	return m.interacting
}

// ActiveServiceMode returns the DetailMode for the currently selected service.
func (m Model) ActiveServiceMode() DetailMode {
	return m.activeServiceMode()
}

// activeServiceMode returns the DetailMode for the currently selected service.
func (m Model) activeServiceMode() DetailMode {
	for _, s := range m.services {
		if s.Name == m.service {
			return s.Mode
		}
	}
	return ReadOnly
}

// SetFocusList switches keyboard focus to the left (list) pane.
func (m *Model) SetFocusList() {
	m.focus = FocusList
}

// SetSize updates the detail panel dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// SetFocused sets whether the detail panel is the focused pane.
func (m *Model) SetFocused(f bool) {
	m.focused = f
}

// Focused returns whether the panel is focused.
func (m Model) Focused() bool {
	return m.focused
}

// Service returns the currently displayed service name (for staleness checks).
func (m Model) Service() string {
	return m.service
}

// ResetService clears the current service name so that a subsequent SetService or
// SetServiceWithCache call for the same service name won't be skipped.
// Used when switching accounts — the service name stays the same but the data changes.
func (m *Model) ResetService() {
	m.service = ""
}

// updateDropdown handles key events when the service dropdown is open.
func (m Model) updateDropdown(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.dropdownCursor < len(m.services)-1 {
			m.dropdownCursor++
		}
	case "k", "up":
		if m.dropdownCursor > 0 {
			m.dropdownCursor--
		}
	case "enter":
		if m.dropdownCursor >= 0 && m.dropdownCursor < len(m.services) {
			entry := m.services[m.dropdownCursor]
			m.dropdownOpen = false
			if entry.Integrated && entry.Name != m.service {
				return m, func() tea.Msg {
					return SelectServiceMsg{ServiceName: entry.Name}
				}
			}
		}
	case "esc", "s":
		m.dropdownOpen = false
	}
	return m, nil
}

// viewDropdownLine renders the collapsed dropdown indicator line.
// e.g. "▼ KV (5 items)" or "▼ Select Service"
func (m Model) viewDropdownLine() string {
	arrow := theme.DimStyle.Render("▼")
	if m.dropdownOpen {
		arrow = theme.TitleStyle.Render("▲")
	}

	if m.service == "" {
		return fmt.Sprintf(" %s %s", arrow, theme.DimStyle.Render("Select Service"))
	}

	serviceName := theme.TitleStyle.Render(m.service)
	count := ""
	if !m.loading && m.err == nil && !m.notIntegrated {
		count = theme.DimStyle.Render(fmt.Sprintf(" (%d items)", len(m.resources)))
	} else if m.loading {
		count = " " + m.spinner.View()
	}
	return fmt.Sprintf(" %s %s%s", arrow, serviceName, count)
}

// viewDropdownOverlay renders the expanded service dropdown list.
func (m Model) viewDropdownOverlay(maxHeight int) string {
	if len(m.services) == 0 {
		return theme.DimStyle.Render("  No services available")
	}

	var lines []string
	for i, s := range m.services {
		cursor := "  "
		if i == m.dropdownCursor {
			cursor = theme.SelectedItemStyle.Render("> ")
		}

		nameStyle := theme.NormalItemStyle
		if i == m.dropdownCursor {
			nameStyle = theme.SelectedItemStyle
		}

		label := nameStyle.Render(s.Name)
		if !s.Integrated {
			label = theme.DimStyle.Render(s.Name + " (coming soon)")
			if i == m.dropdownCursor {
				label = theme.SelectedItemStyle.Render(s.Name) + theme.DimStyle.Render(" (coming soon)")
			}
		}

		// Mark current service with a bullet
		current := "  "
		if s.Name == m.service {
			current = theme.TitleStyle.Render("● ")
		}

		lines = append(lines, fmt.Sprintf("%s%s%s", cursor, current, label))
	}

	// Pad to maxHeight
	for len(lines) < maxHeight {
		lines = append(lines, "")
	}
	if len(lines) > maxHeight {
		lines = lines[:maxHeight]
	}
	return strings.Join(lines, "\n")
}
