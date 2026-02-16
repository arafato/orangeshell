package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	svc "github.com/oarafat/orangeshell/internal/service"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Runner lifecycle ---

// stopDevRunner stops and removes a specific dev runner by key.
func (m *Model) stopDevRunner(key string) {
	if dr, ok := m.devRunners[key]; ok {
		if dr.runner != nil {
			dr.runner.Stop()
		}
		if dr.logFile != nil {
			dr.logFile.Close()
		}
		delete(m.devRunners, key)
	}
}

// stopCmdRunner stops and removes a specific command runner by key.
func (m *Model) stopCmdRunner(key string) {
	if cr, ok := m.cmdRunners[key]; ok {
		if cr.runner != nil {
			cr.runner.Stop()
		}
		delete(m.cmdRunners, key)
	}
}

// --- Starting dev servers ---

// startDevServer starts a wrangler dev process for the focused project/env.
// The output goes to the monitoring grid + log file (no CmdPane).
func (m *Model) startDevServer(action, projectName, envName, configPath, scriptName string) tea.Cmd {
	key := runnerKey(projectName, envName)

	// Don't start if one is already running for this env
	if _, ok := m.devRunners[key]; ok {
		return nil
	}

	// Open log file
	logFile, logPath := openDevLogFile(scriptName)

	// Create runner
	runner := wcfg.NewRunner()
	dr := &devRunner{
		runner:  runner,
		key:     key,
		devKind: "local",
		status:  "starting",
		logPath: logPath,
		logFile: logFile,
	}
	if action == "dev --remote" {
		dr.devKind = "remote"
	}
	m.devRunners[key] = dr

	// Create dev session for monitoring integration
	dsName := devScriptName(scriptName)
	m.devSessions = append(m.devSessions, devSession{
		ScriptName:  dsName,
		ProjectName: projectName,
		EnvName:     envName,
		DevKind:     dr.devKind,
		RunnerKey:   key,
	})
	m.monitoring.AddDevToGrid(dsName, dr.devKind)
	m.refreshMonitoringWorkerTree()
	m.syncDevBadges()

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: configPath,
		EnvName:    envName,
		ExtraArgs:  []string{"--show-interactive-dev-session=false"},
		AccountID:  m.registry.ActiveAccountID(),
	}

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.CmdDoneMsg{
					RunnerKey: key,
					IsDevCmd:  true,
					Result:    wcfg.RunResult{ExitCode: 1, Err: err},
				}
			}
			return readRunnerOutput(runner, key, true)
		},
		m.wrangler.SpinnerInit(),
	)
}

// --- Starting commands (deploy, delete, etc.) ---

// startWranglerCmd creates a command runner for a short-lived wrangler command.
func (m *Model) startWranglerCmd(action, envName string) tea.Cmd {
	projectName := m.wrangler.FocusedProjectName()
	return m.startWranglerCmdWithArgs(action, projectName, envName, nil)
}

// startWranglerCmdWithArgs creates a command runner with extra arguments.
func (m *Model) startWranglerCmdWithArgs(action, projectName, envName string, extraArgs []string) tea.Cmd {
	key := runnerKey(projectName, envName)

	// Don't start if a command is already running for this env
	if cr, ok := m.cmdRunners[key]; ok && cr.runner != nil {
		return nil
	}

	// Stop any active tail when starting a wrangler command
	if m.tailSession != nil {
		m.stopTail()
	}

	// Create runner + CmdPane
	runner := wcfg.NewRunner()
	pane := uiwrangler.NewCmdPane()
	pane.StartCommand(action, envName)

	cr := &cmdRunner{
		runner:  runner,
		key:     key,
		action:  action,
		cmdPane: pane,
	}
	m.cmdRunners[key] = cr

	// Set this as the active CmdPane if it matches the focused project/env
	focusedKey := runnerKey(m.wrangler.FocusedProjectName(), m.wrangler.FocusedEnvName())
	if key == focusedKey {
		m.wrangler.SetActiveCmdPane(&cr.cmdPane)
	}

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		ExtraArgs:  extraArgs,
		AccountID:  m.registry.ActiveAccountID(),
	}

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.CmdDoneMsg{
					RunnerKey: key,
					IsDevCmd:  false,
					Result:    wcfg.RunResult{ExitCode: 1, Err: err},
				}
			}
			return readRunnerOutput(runner, key, false)
		},
		m.wrangler.SpinnerInit(),
	)
}

// --- Output reading ---

// readRunnerOutput reads the next line from a runner's output channel.
func readRunnerOutput(runner *wcfg.Runner, key string, isDevCmd bool) tea.Msg {
	line, ok := <-runner.LinesCh()
	if ok {
		return uiwrangler.CmdOutputMsg{RunnerKey: key, IsDevCmd: isDevCmd, Line: line}
	}
	// Lines channel closed — read the result.
	result, ok := <-runner.DoneCh()
	if !ok {
		return uiwrangler.CmdDoneMsg{RunnerKey: key, IsDevCmd: isDevCmd, Result: wcfg.RunResult{}}
	}
	return uiwrangler.CmdDoneMsg{RunnerKey: key, IsDevCmd: isDevCmd, Result: result}
}

// waitForRunnerOutput returns a command that reads the next output from the appropriate runner.
func (m Model) waitForRunnerOutput(key string, isDevCmd bool) tea.Cmd {
	var runner *wcfg.Runner
	if isDevCmd {
		if dr, ok := m.devRunners[key]; ok {
			runner = dr.runner
		}
	} else {
		if cr, ok := m.cmdRunners[key]; ok {
			runner = cr.runner
		}
	}
	if runner == nil {
		return nil
	}
	return func() tea.Msg {
		return readRunnerOutput(runner, key, isDevCmd)
	}
}

// --- Output handlers ---

// handleDevOutput processes a line of dev server output.
func (m *Model) handleDevOutput(key string, line wcfg.OutputLine) {
	dr, ok := m.devRunners[key]
	if !ok {
		return
	}

	// Write to log file
	writeDevLogLine(dr.logFile, line)

	// Pipe to monitoring grid
	ds := m.findDevSessionByKey(key)
	if ds == nil {
		return
	}
	tailLine := parseDevOutputLine(line)
	m.monitoring.GridAppendLines(ds.ScriptName, []svc.TailLine{tailLine})

	// Check for port announcement
	if port := extractDevPort(line.Text); port != "" && dr.port == "" {
		dr.port = port
		dr.status = "running"
		ds.Port = port
		m.refreshMonitoringWorkerTree()
		m.syncDevBadges()
	}
}

// handleCmdOutput processes a line of command output (deploy, delete, etc.).
func (m *Model) handleCmdOutput(key string, line wcfg.OutputLine) {
	cr, ok := m.cmdRunners[key]
	if !ok {
		return
	}
	cr.cmdPane.AppendLine(line.Text, line.IsStderr, line.Timestamp)
}

// handleDevDone processes a dev server exit.
func (m *Model) handleDevDone(key string, result wcfg.RunResult) tea.Cmd {
	dr, ok := m.devRunners[key]
	if !ok {
		return nil
	}

	// Drain remaining lines
	if dr.runner != nil {
		for line := range dr.runner.LinesCh() {
			writeDevLogLine(dr.logFile, line)
			ds := m.findDevSessionByKey(key)
			if ds != nil {
				tailLine := parseDevOutputLine(line)
				m.monitoring.GridAppendLines(ds.ScriptName, []svc.TailLine{tailLine})
			}
		}
	}

	// Mark as failed if non-zero exit and was still "starting"
	if result.ExitCode != 0 || result.Err != nil {
		dr.status = "failed"
		errText := "exited"
		if result.Err != nil {
			errText = result.Err.Error()
			// Truncate long error messages for badge display
			if len(errText) > 40 {
				errText = errText[:40] + "..."
			}
		}
		dr.errMsg = errText
		m.syncDevBadges()

		// Set error with log path hint
		if dr.logPath != "" {
			m.err = fmt.Errorf("dev server failed — see %s", dr.logPath)
		}
	}

	// Clean up: remove from monitoring grid, runner map, session list
	m.cleanupDevSessionByKey(key)
	return nil
}

// handleCmdDone processes a command (deploy/delete/etc.) exit.
func (m *Model) handleCmdDone(key string, result wcfg.RunResult) tea.Cmd {
	cr, ok := m.cmdRunners[key]
	if !ok {
		return nil
	}

	// Drain remaining lines
	if cr.runner != nil {
		for line := range cr.runner.LinesCh() {
			cr.cmdPane.AppendLine(line.Text, line.IsStderr, line.Timestamp)
		}
	}

	// Finish the CmdPane
	cr.cmdPane.Finish(result.ExitCode, result.Err)
	action := cr.action
	cr.runner = nil // mark as done but keep CmdPane for display

	// After mutating commands, immediately refresh stale data
	if isMutatingAction(action) && result.ExitCode == 0 {
		return m.refreshAfterMutation()
	}
	return nil
}

// --- Active CmdPane management ---

// refreshActiveCmdPane sets the wrangler view's active CmdPane based on the
// currently focused project/env. Prioritizes active command runners, then
// completed command runners with output.
func (m *Model) refreshActiveCmdPane() {
	projectName := m.wrangler.FocusedProjectName()
	envName := m.wrangler.FocusedEnvName()
	if projectName == "" || envName == "" {
		m.wrangler.SetActiveCmdPane(nil)
		return
	}

	key := runnerKey(projectName, envName)

	// Active command runner takes priority
	if cr, ok := m.cmdRunners[key]; ok {
		m.wrangler.SetActiveCmdPane(&cr.cmdPane)
		return
	}

	// No active runner for this env
	m.wrangler.SetActiveCmdPane(nil)
}

// --- Version fetching (unchanged — uses separate runner) ---

// fetchWranglerVersions runs `wrangler versions list --json` in the background
// and delivers the parsed results via VersionsFetchedMsg.
func (m *Model) fetchWranglerVersions(envName string) tea.Cmd {
	// Cancel any in-flight version fetch
	if m.wranglerVersionRunner != nil {
		m.wranglerVersionRunner.Stop()
	}

	cmd := wcfg.Command{
		Action:     "versions list",
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		ExtraArgs:  []string{"--json"},
		AccountID:  m.registry.ActiveAccountID(),
	}

	runner := wcfg.NewRunner()
	m.wranglerVersionRunner = runner

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.VersionsFetchedMsg{Err: err}
			}

			// Collect all stdout lines (the JSON output)
			var jsonBuf strings.Builder
			for line := range runner.LinesCh() {
				if !line.IsStderr {
					jsonBuf.WriteString(line.Text)
					jsonBuf.WriteByte('\n')
				}
			}

			// Wait for the command to finish
			result := <-runner.DoneCh()
			if result.Err != nil && result.ExitCode != 0 {
				return uiwrangler.VersionsFetchedMsg{
					Err: fmt.Errorf("wrangler versions list failed (exit %d)", result.ExitCode),
				}
			}

			versions, err := wcfg.ParseVersionsJSON([]byte(jsonBuf.String()))
			if err != nil {
				return uiwrangler.VersionsFetchedMsg{Err: err}
			}

			return uiwrangler.VersionsFetchedMsg{Versions: versions}
		},
		m.wrangler.SpinnerInit(),
	)
}

// openVersionPicker opens the version picker overlay, using cached versions if available
// or triggering a background fetch.
func (m *Model) openVersionPicker(mode uiwrangler.PickerMode, envName string) tea.Cmd {
	haveCached := m.wrangler.ShowVersionPicker(mode, envName)
	if haveCached {
		return nil
	}
	return m.fetchWranglerVersions(envName)
}
