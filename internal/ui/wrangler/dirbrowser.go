package wrangler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

const (
	loadFromHereLabel = "[ Load from here ]"
	createHereLabel   = "[ Create here ]"
	createFolderLabel = "+ Create folder"
)

// DirBrowser is a simple directory browser for selecting a wrangler project directory.
// Navigation: up/down to select, right/enter to drill in, left/backspace to go up, esc to cancel.
// The first entry is always an action label ("[ Load from here ]" or "[ Create here ]").
// In create mode, a "+ Create folder" entry appears below the action label.
type DirBrowser struct {
	currentDir string   // absolute path being browsed
	entries    []string // action label, optional create folder, "..", then sorted subdirectories
	cursor     int
	scrollY    int
	scanErr    error // permission or read errors
	mode       DirBrowserMode

	// Inline folder creation
	creatingFolder bool
	folderInput    textinput.Model
	folderErr      error
}

// NewDirBrowser creates a directory browser starting at the given directory.
func NewDirBrowser(startDir string) DirBrowser {
	absDir, err := filepath.Abs(startDir)
	if err != nil {
		return DirBrowser{currentDir: startDir, scanErr: err}
	}
	b := DirBrowser{currentDir: absDir}
	b.scan()
	return b
}

// SetMode sets the browser mode (must be called before first scan for correct labels).
func (b *DirBrowser) SetMode(mode DirBrowserMode) {
	b.mode = mode
	b.scan() // rescan to update entries with correct labels
}

// scan reads the current directory and populates entries.
func (b *DirBrowser) scan() {
	b.scanErr = nil
	b.entries = nil
	b.cursor = 0
	b.scrollY = 0

	// Action label depends on mode
	if b.mode == DirBrowserModeCreate {
		b.entries = append(b.entries, createHereLabel)
		b.entries = append(b.entries, createFolderLabel)
	} else {
		b.entries = append(b.entries, loadFromHereLabel)
	}

	// Add ".." unless we're at the filesystem root
	parent := filepath.Dir(b.currentDir)
	if parent != b.currentDir {
		b.entries = append(b.entries, "..")
	}

	// Read directory entries
	dirEntries, err := os.ReadDir(b.currentDir)
	if err != nil {
		b.scanErr = err
		return
	}

	// Collect subdirectories (skip hidden dirs starting with ".")
	var dirs []string
	for _, entry := range dirEntries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			dirs = append(dirs, entry.Name())
		}
	}
	sort.Strings(dirs)

	b.entries = append(b.entries, dirs...)
}

// CurrentDir returns the absolute path being browsed.
func (b DirBrowser) CurrentDir() string {
	return b.currentDir
}

// Update handles key events for the directory browser.
// Returns the updated browser and an optional command (LoadConfigPathMsg when confirmed).
func (b DirBrowser) Update(msg tea.KeyMsg) (DirBrowser, tea.Cmd) {
	// Inline folder creation mode — text input takes exclusive focus
	if b.creatingFolder {
		return b.updateCreateFolder(msg)
	}

	switch msg.String() {
	case "up", "k":
		if b.cursor > 0 {
			b.cursor--
			b.adjustScroll()
		}
	case "down", "j":
		if b.cursor < len(b.entries)-1 {
			b.cursor++
			b.adjustScroll()
		}
	case "right", "l", "enter":
		return b.selectEntry()
	case "left", "h", "backspace":
		b.navigateUp()
	case "esc":
		// Signal close — parent checks for this
		return b, func() tea.Msg { return dirBrowserCloseMsg{} }
	}
	return b, nil
}

// updateCreateFolder handles keys while the folder name input is active.
func (b DirBrowser) updateCreateFolder(msg tea.KeyMsg) (DirBrowser, tea.Cmd) {
	switch msg.String() {
	case "esc":
		b.creatingFolder = false
		b.folderErr = nil
		return b, nil
	case "enter":
		name := strings.TrimSpace(b.folderInput.Value())
		if name == "" {
			return b, nil
		}
		// Create the directory
		target := filepath.Join(b.currentDir, name)
		if err := os.MkdirAll(target, 0o755); err != nil {
			b.folderErr = err
			return b, nil
		}
		// Navigate into the new folder
		b.creatingFolder = false
		b.folderErr = nil
		b.currentDir = target
		b.scan()
		return b, nil
	default:
		var cmd tea.Cmd
		b.folderInput, cmd = b.folderInput.Update(msg)
		return b, cmd
	}
}

// dirBrowserCloseMsg is an internal message to signal the browser should close.
type dirBrowserCloseMsg struct{}

// selectEntry handles Enter/Right on the currently selected entry.
func (b DirBrowser) selectEntry() (DirBrowser, tea.Cmd) {
	if b.cursor < 0 || b.cursor >= len(b.entries) {
		return b, nil
	}

	entry := b.entries[b.cursor]

	switch entry {
	case loadFromHereLabel, createHereLabel:
		// Confirm current directory — emit load message
		dir := b.currentDir
		return b, func() tea.Msg {
			return LoadConfigPathMsg{Path: dir}
		}
	case createFolderLabel:
		// Activate inline text input for folder name
		ni := textinput.New()
		ni.Placeholder = "my-project"
		ni.CharLimit = 255
		ni.Width = 40
		ni.Prompt = "  "
		ni.PromptStyle = lipgloss.NewStyle().Foreground(theme.ColorGreen)
		ni.TextStyle = theme.ValueStyle
		ni.PlaceholderStyle = theme.DimStyle
		ni.Focus()
		b.creatingFolder = true
		b.folderInput = ni
		b.folderErr = nil
		return b, nil
	case "..":
		b.navigateUp()
		return b, nil
	default:
		// Drill into subdirectory
		target := filepath.Join(b.currentDir, entry)
		b.currentDir = target
		b.scan()
		return b, nil
	}
}

// navigateUp moves to the parent directory.
func (b *DirBrowser) navigateUp() {
	parent := filepath.Dir(b.currentDir)
	if parent == b.currentDir {
		return // already at root
	}

	// Remember current dir name so we can place cursor on it after navigating up
	prevName := filepath.Base(b.currentDir)

	b.currentDir = parent
	b.scan()

	// Try to place cursor on the directory we came from
	for i, entry := range b.entries {
		if entry == prevName {
			b.cursor = i
			b.adjustScroll()
			return
		}
	}
}

// adjustScroll ensures the cursor is visible within the scroll window.
func (b *DirBrowser) adjustScroll() {
	// We don't know the exact visible height here, but we keep scrollY
	// close to cursor. The View method handles the actual clipping.
	if b.cursor < b.scrollY {
		b.scrollY = b.cursor
	}
}

// View renders the directory browser content (without outer border — parent handles that).
func (b DirBrowser) View(width, height int) string {
	if height < 3 {
		height = 3
	}

	var lines []string

	// Title
	if b.mode == DirBrowserModeCreate {
		lines = append(lines, theme.TitleStyle.Render("  Choose project location"))
	} else {
		lines = append(lines, theme.TitleStyle.Render("  Browse for Wrangler project"))
	}

	// Separator
	sepWidth := width - 2
	if sepWidth < 0 {
		sepWidth = 0
	}
	sep := theme.DimStyle.Render(fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))
	lines = append(lines, sep)

	// Current path
	lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  %s", b.currentDir)))
	lines = append(lines, "") // spacer

	// Error message
	if b.scanErr != nil {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("  Error: %s", b.scanErr.Error())))
		lines = append(lines, "")
	}

	// Available height for entries (subtract header lines + help line)
	headerLines := len(lines)
	helpLines := 2 // spacer + help text
	entryHeight := height - headerLines - helpLines
	if entryHeight < 1 {
		entryHeight = 1
	}

	// If creating a folder, render the input inline instead of the entry list
	if b.creatingFolder {
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("  Folder name:"))
		lines = append(lines, b.folderInput.View())
		if b.folderErr != nil {
			lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("  %s", b.folderErr.Error())))
		}
		lines = append(lines, "")
		lines = append(lines, theme.DimStyle.Render("  enter confirm  esc cancel"))

		// Pad to height
		for len(lines) < height {
			lines = append(lines, "")
		}
		if len(lines) > height {
			lines = lines[:height]
		}
		return strings.Join(lines, "\n")
	}

	// Adjust scroll so cursor is visible
	if b.cursor < b.scrollY {
		b.scrollY = b.cursor
	}
	if b.cursor >= b.scrollY+entryHeight {
		b.scrollY = b.cursor - entryHeight + 1
	}
	if b.scrollY < 0 {
		b.scrollY = 0
	}

	// Render visible entries
	endIdx := b.scrollY + entryHeight
	if endIdx > len(b.entries) {
		endIdx = len(b.entries)
	}

	greenStyle := lipgloss.NewStyle().Foreground(theme.ColorGreen)

	for i := b.scrollY; i < endIdx; i++ {
		entry := b.entries[i]
		selected := i == b.cursor

		// Format display name
		displayName := entry
		if entry != loadFromHereLabel && entry != createHereLabel &&
			entry != createFolderLabel && entry != ".." {
			displayName = entry + "/"
		}

		if entry == createFolderLabel {
			// Always render in green
			if selected {
				line := fmt.Sprintf("  %s %s",
					greenStyle.Bold(true).Render("\u25b8"), // ▸
					greenStyle.Bold(true).Render(displayName))
				lines = append(lines, line)
			} else {
				lines = append(lines, fmt.Sprintf("    %s", greenStyle.Render(displayName)))
			}
		} else if selected {
			line := fmt.Sprintf("  %s %s",
				theme.SelectedItemStyle.Render("\u25b8"), // ▸
				theme.SelectedItemStyle.Render(displayName))
			lines = append(lines, line)
		} else {
			lines = append(lines, fmt.Sprintf("    %s", theme.ValueStyle.Render(displayName)))
		}
	}

	// Pad entries area to fixed height
	renderedEntries := endIdx - b.scrollY
	for renderedEntries < entryHeight {
		lines = append(lines, "")
		renderedEntries++
	}

	// Help text
	lines = append(lines, "")
	lines = append(lines, theme.DimStyle.Render("  \u2190/h back  \u2191\u2193/jk select  \u2192/l/enter open  esc cancel"))

	// Ensure exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return strings.Join(lines, "\n")
}
