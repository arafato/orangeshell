package app

import (
	"regexp"
	"strings"
	"time"

	svc "github.com/oarafat/orangeshell/internal/service"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// devSession tracks an active wrangler dev session for the Monitoring tab.
type devSession struct {
	ScriptName  string // resolved worker name (used as grid pane key, prefixed with "dev:")
	ProjectName string // project name (for tree grouping)
	EnvName     string // environment name
	DevKind     string // "local" or "remote"
	Port        string // extracted from wrangler dev output (e.g. "8787")
}

// devScriptName returns the prefixed script name used to identify dev panes
// in the monitoring grid. The prefix avoids collisions with deployed workers.
func devScriptName(scriptName string) string {
	return "dev:" + scriptName
}

// devDisplayName returns the human-readable name for a dev script name
// by stripping the "dev:" prefix.
func devDisplayName(scriptName string) string {
	return strings.TrimPrefix(scriptName, "dev:")
}

// isDevScriptName returns true if the script name has the dev prefix.
func isDevScriptName(scriptName string) bool {
	return strings.HasPrefix(scriptName, "dev:")
}

// parseDevOutputLine converts a wrangler dev OutputLine into a TailLine.
// It classifies lines by content patterns to assign appropriate log levels.
func parseDevOutputLine(ol wcfg.OutputLine) svc.TailLine {
	level := "log"
	text := ol.Text

	switch {
	case ol.IsStderr:
		// Stderr lines are generally errors or warnings from wrangler
		if containsAnyCI(text, "error", "fatal", "panic") {
			level = "error"
		} else if containsAnyCI(text, "warn") {
			level = "warn"
		} else {
			level = "system"
		}
	case strings.HasPrefix(text, "[wrangler:") || strings.HasPrefix(text, "[mf:"):
		// Wrangler/miniflare system messages
		level = "system"
	case reHTTPMethod.MatchString(text):
		// HTTP request log lines (e.g. "GET /api/users 200 OK (12ms)")
		level = "request"
	case containsAnyCI(text, "console.error:", "error:", "exception"):
		level = "error"
	case containsAnyCI(text, "console.warn:", "warning:"):
		level = "warn"
	}

	ts := ol.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	return svc.TailLine{
		Timestamp: ts,
		Level:     level,
		Text:      text,
	}
}

// reHTTPMethod matches lines starting with an HTTP method (GET, POST, etc.)
var reHTTPMethod = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s`)

// reReadyURL matches "Ready on http://..." to extract the port.
var reReadyURL = regexp.MustCompile(`(?i)ready on https?://[^:]+:(\d+)`)

// extractDevPort looks for wrangler's "Ready on http://localhost:XXXX" pattern
// and returns the port number, or "" if not found.
func extractDevPort(text string) string {
	matches := reReadyURL.FindStringSubmatch(text)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// containsAnyCI returns true if s contains any of the given substrings (case-insensitive).
func containsAnyCI(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// --- App model helpers ---

// isDevWorker returns true if the given script name belongs to an active dev session.
func (m Model) isDevWorker(scriptName string) bool {
	for _, ds := range m.devSessions {
		if ds.ScriptName == scriptName {
			return true
		}
	}
	return false
}

// findDevSession returns the dev session for the given script name, or nil.
func (m Model) findDevSession(scriptName string) *devSession {
	for i := range m.devSessions {
		if m.devSessions[i].ScriptName == scriptName {
			return &m.devSessions[i]
		}
	}
	return nil
}

// cleanupDevSession removes all dev panes from the monitoring grid,
// clears dev sessions, and refreshes the worker tree.
func (m *Model) cleanupDevSession() {
	for _, ds := range m.devSessions {
		m.monitoring.RemoveFromGrid(ds.ScriptName)
	}
	m.devSessions = nil
	m.refreshMonitoringWorkerTree()
}
