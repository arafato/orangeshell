package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	svc "github.com/oarafat/orangeshell/internal/service"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Runner types ---

// runnerKey builds a unique key for a project/env runner pair.
func runnerKey(projectName, envName string) string {
	return projectName + ":" + envName
}

// devRunner tracks a long-lived wrangler dev process.
type devRunner struct {
	runner  *wcfg.Runner
	key     string // runnerKey
	devKind string // "local" or "remote"
	status  string // "starting", "running", "failed"
	port    string // extracted from "Ready on localhost:XXXX"
	errMsg  string // short error message on failure
	logPath string // path to log file (~/.orangeshell/logs/)
	logFile *os.File
}

// cmdRunner tracks a short-lived wrangler command process (deploy, delete, etc.)
type cmdRunner struct {
	runner  *wcfg.Runner
	key     string // runnerKey
	action  string // "deploy", "delete", "versions deploy", etc.
	cmdPane uiwrangler.CmdPane
}

// devSession tracks an active wrangler dev session for the Monitoring tab.
type devSession struct {
	ScriptName  string // resolved worker name (used as grid pane key, prefixed with "dev:")
	ProjectName string // project name (for tree grouping)
	EnvName     string // environment name
	DevKind     string // "local" or "remote"
	Port        string // extracted from wrangler dev output (e.g. "8787")
	RunnerKey   string // key into devRunners map
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

// --- Log file helpers ---

// openDevLogFile creates a log file for a dev server process at ~/.orangeshell/logs/.
func openDevLogFile(scriptName string) (*os.File, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ""
	}
	logsDir := filepath.Join(home, ".orangeshell", "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return nil, ""
	}
	ts := time.Now().Format("20060102T150405")
	filename := fmt.Sprintf("dev-%s-%s.log", scriptName, ts)
	logPath := filepath.Join(logsDir, filename)
	f, err := os.Create(logPath)
	if err != nil {
		return nil, ""
	}
	return f, logPath
}

// writeDevLogLine writes a single line to the dev log file.
func writeDevLogLine(f *os.File, line wcfg.OutputLine) {
	if f == nil {
		return
	}
	prefix := "stdout"
	if line.IsStderr {
		prefix = "stderr"
	}
	ts := line.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	fmt.Fprintf(f, "[%s] [%s] %s\n", ts.Format("15:04:05.000"), prefix, line.Text)
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

// findDevSessionByKey returns the dev session for the given runner key, or nil.
func (m Model) findDevSessionByKey(key string) *devSession {
	for i := range m.devSessions {
		if m.devSessions[i].RunnerKey == key {
			return &m.devSessions[i]
		}
	}
	return nil
}

// cleanupAllDevSessions removes all dev panes from the monitoring grid,
// stops all dev runners, clears dev sessions, and refreshes the worker tree.
// Used on app shutdown.
func (m *Model) cleanupAllDevSessions() {
	for _, ds := range m.devSessions {
		m.monitoring.RemoveFromGrid(ds.ScriptName)
	}
	for _, dr := range m.devRunners {
		if dr.runner != nil {
			dr.runner.Stop()
		}
		if dr.logFile != nil {
			dr.logFile.Close()
		}
	}
	m.devSessions = nil
	m.devRunners = make(map[string]*devRunner)
	m.refreshMonitoringWorkerTree()
}

// cleanupDevSessionByKey removes a single dev session by its runner key.
// Stops the dev runner, removes the monitoring grid pane, and refreshes the worker tree.
func (m *Model) cleanupDevSessionByKey(key string) {
	// Find and remove the dev session
	for i, ds := range m.devSessions {
		if ds.RunnerKey == key {
			m.monitoring.RemoveFromGrid(ds.ScriptName)
			m.devSessions = append(m.devSessions[:i], m.devSessions[i+1:]...)
			break
		}
	}

	// Stop and remove the dev runner
	if dr, ok := m.devRunners[key]; ok {
		if dr.runner != nil {
			dr.runner.Stop()
		}
		if dr.logFile != nil {
			dr.logFile.Close()
		}
		delete(m.devRunners, key)
	}

	m.refreshMonitoringWorkerTree()
	m.syncDevBadges()
}

// hasDevRunnerFor returns true if a dev server is running for the given project/env.
func (m Model) hasDevRunnerFor(projectName, envName string) bool {
	key := runnerKey(projectName, envName)
	dr, ok := m.devRunners[key]
	return ok && dr.runner != nil
}

// hasCmdRunnerFor returns true if a command (deploy/delete) is running for the given project/env.
func (m Model) hasCmdRunnerFor(projectName, envName string) bool {
	key := runnerKey(projectName, envName)
	cr, ok := m.cmdRunners[key]
	return ok && cr.runner != nil
}

// devRunnerStatus returns the dev status for a given project/env, or "" if none.
func (m Model) devRunnerStatus(projectName, envName string) string {
	key := runnerKey(projectName, envName)
	dr, ok := m.devRunners[key]
	if !ok {
		return ""
	}
	return dr.status
}

// syncDevBadges updates the dev badge data on all env boxes and project boxes.
func (m *Model) syncDevBadges() {
	// Build badge data from active dev sessions
	type badgeInfo struct {
		Kind   string // "local" or "remote"
		Port   string
		Status string // "starting", "running", "failed"
		ErrMsg string // error text for failed state
	}
	// Map: projectName -> envName -> badgeInfo
	badges := make(map[string]map[string]badgeInfo)
	for _, ds := range m.devSessions {
		if _, ok := badges[ds.ProjectName]; !ok {
			badges[ds.ProjectName] = make(map[string]badgeInfo)
		}
		status := "running"
		port := ds.Port
		errMsg := ""
		if dr, ok := m.devRunners[ds.RunnerKey]; ok {
			status = dr.status
			if dr.port != "" {
				port = dr.port
			}
			errMsg = dr.errMsg
		}
		badges[ds.ProjectName][ds.EnvName] = badgeInfo{
			Kind:   ds.DevKind,
			Port:   port,
			Status: status,
			ErrMsg: errMsg,
		}
	}

	// Update env boxes (for drilled-in project view)
	for i := range m.wrangler.EnvBoxCount() {
		eb := m.wrangler.EnvBoxAt(i)
		if eb == nil {
			continue
		}
		projectName := m.wrangler.FocusedProjectName()
		if info, ok := badges[projectName][eb.EnvName]; ok {
			eb.DevStatus = info.Status
			eb.DevKind = info.Kind
			eb.DevPort = info.Port
			eb.DevError = info.ErrMsg
		} else {
			eb.DevStatus = ""
			eb.DevKind = ""
			eb.DevPort = ""
			eb.DevError = ""
		}
	}

	// Update project boxes (for monorepo project list)
	m.wrangler.UpdateDevBadges(func(projectName, envName string) uiwrangler.DevBadge {
		info, ok := badges[projectName][envName]
		if !ok {
			return uiwrangler.DevBadge{}
		}
		return uiwrangler.DevBadge{
			Kind:   info.Kind,
			Port:   info.Port,
			Status: info.Status,
		}
	})
}

// triggerDevCron fires an HTTP GET to the dev server's /cdn-cgi/handler/scheduled
// endpoint to invoke the worker's scheduled() handler.
func triggerDevCron(scriptName, port string) devCronTriggerDoneMsg {
	url := fmt.Sprintf("http://localhost:%s/cdn-cgi/handler/scheduled", port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return devCronTriggerDoneMsg{ScriptName: scriptName, Err: err}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return devCronTriggerDoneMsg{ScriptName: scriptName, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return devCronTriggerDoneMsg{
			ScriptName: scriptName,
			Err:        fmt.Errorf("HTTP %d from scheduled handler", resp.StatusCode),
		}
	}

	return devCronTriggerDoneMsg{ScriptName: scriptName}
}
