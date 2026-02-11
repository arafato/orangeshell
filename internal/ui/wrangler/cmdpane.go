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
	isTail     bool   // true when showing tail output (vs command output)
	tailScript string // script name being tailed
	tailError  string // tail error message
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

// View renders the command output pane.
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
	if p.isTail {
		headerText = p.tailHeader(spinnerView)
	} else if p.running {
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

	// Available lines for content (minus sep, header, help)
	contentHeight := height - 3
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Render output lines
	var outputLines []string
	maxTextWidth := width - 2 // subtract 2-char indent
	if maxTextWidth < 5 {
		maxTextWidth = 5
	}
	for _, cl := range p.lines {
		text := cl.text
		// Truncate long lines to fit within the pane (accounting for indent)
		if utf8.RuneCountInString(text) > maxTextWidth {
			runes := []rune(text)
			if maxTextWidth > 3 {
				text = string(runes[:maxTextWidth-3]) + "..."
			} else {
				text = string(runes[:maxTextWidth])
			}
		}

		if cl.level != "" {
			// Tail mode: level-based coloring with timestamp
			tsStr := ""
			if !cl.ts.IsZero() {
				tsStr = theme.LogTimestampStyle.Render(cl.ts.Format("15:04:05")) + "  "
			}
			outputLines = append(outputLines, "  "+tsStr+styleTailLevel(cl.level).Render(text))
		} else if cl.isStderr {
			outputLines = append(outputLines, "  "+theme.ErrorStyle.Render(text))
		} else {
			outputLines = append(outputLines, "  "+theme.ValueStyle.Render(text))
		}
	}

	// Apply scroll offset: scrollOffset=0 means at bottom, >0 means scrolled up
	if len(outputLines) > contentHeight {
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
		outputLines = outputLines[start:end]
	}

	// Pad to fill space
	for len(outputLines) < contentHeight {
		outputLines = append([]string{""}, outputLines...)
	}

	lines = append(lines, outputLines...)

	// Help
	var helpText string
	if p.isTail && p.running {
		if p.userScrolled {
			helpText = theme.DimStyle.Render("  pgup/pgdn scroll  |  end bottom  |  ctrl+p stop tail")
		} else {
			helpText = theme.DimStyle.Render("  pgup/pgdn scroll  |  ctrl+p stop tail")
		}
	} else if p.userScrolled {
		helpText = theme.DimStyle.Render("  pgup/pgdn scroll  |  end bottom  |  ctrl+p actions")
	} else {
		helpText = theme.DimStyle.Render("  pgup/pgdn scroll  |  ctrl+p actions")
	}
	lines = append(lines, helpText)

	// Ensure exact height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return strings.Join(lines, "\n")
}

// tailHeader renders the header line for tail mode.
func (p CmdPane) tailHeader(spinnerView string) string {
	if p.tailError != "" {
		return fmt.Sprintf("  %s  %s",
			theme.LogConsoleHeaderStyle.Render(fmt.Sprintf("Tail: %s", p.tailScript)),
			theme.ErrorStyle.Render(p.tailError))
	}
	if p.running {
		return fmt.Sprintf("  %s %s",
			spinnerView,
			theme.LogConsoleHeaderStyle.Render(fmt.Sprintf("Tailing: %s", p.tailScript)))
	}
	if p.exitMsg != "" {
		return fmt.Sprintf("  %s  %s",
			theme.LogConsoleHeaderStyle.Render(fmt.Sprintf("Tail: %s", p.tailScript)),
			theme.DimStyle.Render(p.exitMsg))
	}
	return theme.DimStyle.Render(fmt.Sprintf("  Tail: %s", p.tailScript))
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
