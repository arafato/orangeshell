package wrangler

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// CmdPane renders the command output pane at the bottom of the wrangler view.
// Supports two modes: command output (deploy, dev, etc.) and tail log streaming.
type CmdPane struct {
	lines        []cmdLine // output lines
	running      bool      // command/tail is in flight
	action       string    // raw action string (e.g. "deploy", "dev", "dev --remote")
	label        string    // "Deploy (staging)" etc.
	exitMsg      string    // "Exited with code 0" etc.
	exitFailed   bool      // true if the command failed (for styling)
	scrollOffset int       // lines scrolled up from bottom (0 = at bottom)
	userScrolled bool      // true if user has manually scrolled

	// Tail mode
	isTail         bool   // true when showing tail output (vs command output)
	tailScript     string // script name being tailed
	tailError      string // tail error message
	tailConnecting bool   // true while waiting for TailStartedMsg (before lines arrive)
}

type cmdLine struct {
	text     string
	isStderr bool
	ts       time.Time
	level    string // tail log level (empty for command output lines)
}

// NewCmdPane creates an empty command pane.
func NewCmdPane() CmdPane {
	return CmdPane{}
}

// StartCommand resets the pane for a new command execution.
func (p *CmdPane) StartCommand(action, envName string) {
	label := wcfg.CommandLabel(action)
	if envName != "" && envName != "default" {
		label += fmt.Sprintf(" (%s)", envName)
	}
	p.lines = nil
	p.running = true
	p.action = action
	p.label = label
	p.exitMsg = ""
	p.exitFailed = false
	p.scrollOffset = 0
	p.userScrolled = false
}

// AppendLine adds a single output line.
func (p *CmdPane) AppendLine(text string, isStderr bool, ts time.Time) {
	p.lines = append(p.lines, cmdLine{text: text, isStderr: isStderr, ts: ts})
	// Cap buffer at 500 lines
	if len(p.lines) > 500 {
		p.lines = p.lines[len(p.lines)-500:]
	}
	// Auto-scroll to bottom if user hasn't manually scrolled
	if !p.userScrolled {
		p.scrollOffset = 0
	}
}

// Finish marks the command as completed.
// If the command is not running (e.g. already finished by a user-initiated stop),
// this is a no-op to prevent overwriting a clean exit message.
func (p *CmdPane) Finish(exitCode int, err error) {
	if !p.running {
		return
	}
	p.running = false
	p.action = ""
	if err != nil && exitCode == 0 {
		p.exitMsg = fmt.Sprintf("Error: %s", err)
		p.exitFailed = true
	} else if exitCode != 0 {
		p.exitMsg = fmt.Sprintf("Exited with code %d", exitCode)
		p.exitFailed = true
	} else {
		p.exitMsg = "Done"
		p.exitFailed = false
	}
}

// FinishWithMessage marks the command as completed with a custom message.
// Used for user-initiated stops (e.g. "Stopped" instead of "Exited with code -1").
func (p *CmdPane) FinishWithMessage(msg string, failed bool) {
	p.running = false
	p.action = ""
	p.exitMsg = msg
	p.exitFailed = failed
}

// --- Tail mode methods ---

// StartTail resets the pane and enters tail mode for the given script.
func (p *CmdPane) StartTail(scriptName string) {
	p.lines = nil
	p.running = true
	p.action = ""
	p.label = ""
	p.exitMsg = ""
	p.exitFailed = false
	p.scrollOffset = 0
	p.userScrolled = false
	p.isTail = true
	p.tailScript = scriptName
	p.tailError = ""
	p.tailConnecting = true
}

// TailConnected marks the tail as connected (first lines may arrive).
func (p *CmdPane) TailConnected() {
	p.tailConnecting = false
}

// AppendTailLine adds a single tail log line with level information.
func (p *CmdPane) AppendTailLine(line svc.TailLine) {
	p.lines = append(p.lines, cmdLine{
		text:  line.Text,
		ts:    line.Timestamp,
		level: line.Level,
	})
	// Cap buffer at 500 lines
	if len(p.lines) > 500 {
		p.lines = p.lines[len(p.lines)-500:]
	}
	if !p.userScrolled {
		p.scrollOffset = 0
	}
}

// SetTailError records a tail error message in the pane.
func (p *CmdPane) SetTailError(errMsg string) {
	p.running = false
	p.tailError = errMsg
}

// StopTail marks the tail as finished with a clean message.
func (p *CmdPane) StopTail() {
	p.running = false
	p.exitMsg = "Stopped"
	p.exitFailed = false
}

// IsTail returns whether the pane is in tail mode.
func (p CmdPane) IsTail() bool {
	return p.isTail
}

// IsRunning returns whether a command is in flight.
func (p CmdPane) IsRunning() bool {
	return p.running
}

// Action returns the raw action string of the currently running command.
// Returns "" if no command is running.
func (p CmdPane) Action() string {
	if p.running {
		return p.action
	}
	return ""
}

// IsActive returns whether the pane has content to show.
func (p CmdPane) IsActive() bool {
	return len(p.lines) > 0 || p.running || p.exitMsg != ""
}

// Clear resets the pane entirely.
func (p *CmdPane) Clear() {
	p.lines = nil
	p.running = false
	p.action = ""
	p.label = ""
	p.exitMsg = ""
	p.exitFailed = false
	p.scrollOffset = 0
	p.userScrolled = false
	p.isTail = false
	p.tailScript = ""
	p.tailError = ""
	p.tailConnecting = false
}

// ScrollUp moves the viewport up by n lines.
func (p *CmdPane) ScrollUp(n int) {
	p.scrollOffset += n
	max := len(p.lines)
	if p.scrollOffset > max {
		p.scrollOffset = max
	}
	p.userScrolled = true
}

// ScrollDown moves the viewport down by n lines (toward bottom).
func (p *CmdPane) ScrollDown(n int) {
	p.scrollOffset -= n
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
	if p.scrollOffset == 0 {
		p.userScrolled = false
	}
}

// ScrollToBottom resets scroll to the bottom (most recent output).
func (p *CmdPane) ScrollToBottom() {
	p.scrollOffset = 0
	p.userScrolled = false
}

// View renders the command output pane (non-tail mode).
// height: number of lines allocated for this pane.
// width: available width (same as the parent's inner content width).
// spinnerView: the current spinner frame string (passed from parent).
func (p CmdPane) View(height, width int, spinnerView string) string {
	if height < 3 {
		height = 3
	}

	sepWidth := width - 2
	if sepWidth < 0 {
		sepWidth = 0
	}
	consoleSep := theme.DimStyle.Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Header
	var headerText string
	if p.running {
		headerText = fmt.Sprintf("  %s %s",
			spinnerView,
			theme.LogConsoleHeaderStyle.Render(fmt.Sprintf("Running: %s", p.label)))
	} else if p.exitMsg != "" {
		style := theme.SuccessStyle
		if p.exitFailed {
			style = theme.ErrorStyle
		}
		headerText = fmt.Sprintf("  %s  %s",
			theme.LogConsoleHeaderStyle.Render(p.label),
			style.Render(p.exitMsg))
	} else {
		headerText = theme.DimStyle.Render("  Command Output")
	}

	lines := []string{consoleSep, headerText}

	contentHeight := height - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	outputLines := p.renderOutputLines(width)
	outputLines = p.applyScroll(outputLines, contentHeight)

	// Pad to fill space
	for len(outputLines) < contentHeight {
		outputLines = append([]string{""}, outputLines...)
	}
	lines = append(lines, outputLines...)

	// Help
	var helpText string
	if p.userScrolled {
		helpText = theme.DimStyle.Render("  pgup/pgdn scroll  |  end bottom  |  ctrl+p actions")
	} else {
		helpText = theme.DimStyle.Render("  pgup/pgdn scroll  |  ctrl+p actions")
	}
	lines = append(lines, helpText)

	return p.padToHeight(lines, height)
}

// ViewTailConsole renders the always-visible Live Logs console at the bottom
// of the wrangler view. Matches the detail view's renderLogConsole() design:
// black background, ▸/▹ header, level-colored log lines, t key hints.
func (p CmdPane) ViewTailConsole(height, width int, spinnerView string) string {
	if height < 3 {
		height = 3
	}

	sepWidth := width - 2
	if sepWidth < 0 {
		sepWidth = 0
	}
	consoleSep := theme.DimStyle.Render(
		fmt.Sprintf(" %s", strings.Repeat("─", sepWidth)))

	// Header — matches detail view's Live Logs design
	var headerText string
	if p.isTail && p.running {
		headerText = theme.LogConsoleHeaderStyle.Render("  \u25b8 Live Logs (tailing)")
	} else if p.isTail && p.tailConnecting {
		headerText = fmt.Sprintf("  %s %s", spinnerView, theme.DimStyle.Render("Connecting to tail..."))
	} else {
		headerText = theme.DimStyle.Render("  \u25b9 Live Logs")
	}

	// Help line
	var helpText string
	if p.isTail && p.running {
		helpText = theme.DimStyle.Render("  t stop tail  |  pgup/pgdn scroll")
	} else {
		helpText = theme.DimStyle.Render("  t start tail  |  pgup/pgdn scroll")
	}

	lines := []string{consoleSep, headerText}

	contentHeight := height - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Content — state-dependent (matching detail view exactly)
	if p.tailError != "" {
		errLine := theme.ErrorStyle.Render(fmt.Sprintf("  Error: %s", p.tailError))
		lines = append(lines, errLine)
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, helpText)
		return p.applyTailBackground(lines, width)
	}

	if !p.isTail || (!p.running && !p.tailConnecting) {
		hint := theme.DimStyle.Render("  Press t to start tailing logs")
		lines = append(lines, hint)
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, helpText)
		return p.applyTailBackground(lines, width)
	}

	if len(p.lines) == 0 {
		waiting := theme.DimStyle.Render("  Waiting for log events...")
		lines = append(lines, waiting)
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		lines = append(lines, helpText)
		return p.applyTailBackground(lines, width)
	}

	// Render tail log lines with level-based coloring
	outputLines := p.renderTailLines(width)
	outputLines = p.applyScroll(outputLines, contentHeight)

	// Pad to fill space
	for len(outputLines) < contentHeight {
		outputLines = append(outputLines, "")
	}
	lines = append(lines, outputLines...)
	lines = append(lines, helpText)

	return p.applyTailBackground(lines, width)
}

// renderOutputLines renders command output lines (non-tail mode).
func (p CmdPane) renderOutputLines(width int) []string {
	var outputLines []string
	maxTextWidth := width - 2
	if maxTextWidth < 5 {
		maxTextWidth = 5
	}
	for _, cl := range p.lines {
		text := truncateLine(cl.text, maxTextWidth)
		if cl.isStderr {
			outputLines = append(outputLines, "  "+theme.ErrorStyle.Render(text))
		} else {
			outputLines = append(outputLines, "  "+theme.ValueStyle.Render(text))
		}
	}
	return outputLines
}

// renderTailLines renders tail log lines with timestamp and level-based coloring.
func (p CmdPane) renderTailLines(width int) []string {
	var outputLines []string
	maxTextWidth := width - 14 // account for "  HH:MM:SS  " prefix
	if maxTextWidth < 5 {
		maxTextWidth = 5
	}
	for _, cl := range p.lines {
		text := truncateLine(cl.text, maxTextWidth)
		ts := theme.LogTimestampStyle.Render(cl.ts.Format("15:04:05"))
		outputLines = append(outputLines, fmt.Sprintf("  %s %s", ts, styleTailLevel(cl.level).Render(text)))
	}
	return outputLines
}

// applyScroll applies the scroll offset to a slice of output lines.
func (p CmdPane) applyScroll(outputLines []string, contentHeight int) []string {
	if len(outputLines) <= contentHeight {
		return outputLines
	}
	end := len(outputLines) - p.scrollOffset
	if end < contentHeight {
		end = contentHeight
	}
	if end > len(outputLines) {
		end = len(outputLines)
	}
	start := end - contentHeight
	if start < 0 {
		start = 0
	}
	return outputLines[start:end]
}

// applyTailBackground applies the black background to every line in the tail console.
func (p CmdPane) applyTailBackground(lines []string, width int) string {
	bgStyle := lipgloss.NewStyle().Background(theme.LogConsoleBg).Width(width)
	var result []string
	for _, line := range lines {
		result = append(result, bgStyle.Render(line))
	}
	return strings.Join(result, "\n")
}

// padToHeight ensures lines slice is exactly height lines, joined into a string.
func (p CmdPane) padToHeight(lines []string, height int) string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// truncateLine truncates a string to maxWidth runes, adding "..." if truncated.
func truncateLine(text string, maxWidth int) string {
	if utf8.RuneCountInString(text) <= maxWidth {
		return text
	}
	runes := []rune(text)
	if maxWidth > 3 {
		return string(runes[:maxWidth-3]) + "..."
	}
	return string(runes[:maxWidth])
}

// styleTailLevel returns the lipgloss style for a given tail log level.
func styleTailLevel(level string) lipgloss.Style {
	switch level {
	case "warn":
		return theme.LogLevelWarn
	case "error", "exception":
		return theme.LogLevelError
	case "request":
		return theme.LogLevelRequest
	case "system":
		return theme.LogLevelSystem
	default: // "log", "info", etc.
		return theme.LogLevelLog
	}
}
