package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/api"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/detail"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// --- Runner lifecycle ---

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

	// If there's an existing dev runner for this env (failed or running), clean it up first
	if dr, ok := m.devRunners[key]; ok {
		if dr.runner != nil {
			// Still running — don't start a second one
			return nil
		}
		// Failed state — clean up before restarting
		m.cleanupDevSessionByKey(key)
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
		ConfigPath:  configPath,
	})
	m.monitoring.AddDevToGrid(dsName, dr.devKind)
	m.refreshMonitoringWorkerTree()
	m.syncDevBadges()
	m.syncLocalResources()

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
// Uses the focused project's config path.
func (m *Model) startWranglerCmd(action, envName string) tea.Cmd {
	projectName := m.wrangler.FocusedProjectName()
	configPath := m.wrangler.ConfigPath()
	return m.startWranglerCmdWithArgs(action, projectName, envName, configPath, nil)
}

// startWranglerCmdWithArgs creates a command runner with extra arguments.
// The configPath must be provided explicitly so this works correctly even
// if the focused project differs from the target project.
func (m *Model) startWranglerCmdWithArgs(action, projectName, envName, configPath string, extraArgs []string) tea.Cmd {
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
		ConfigPath: configPath,
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

	// Drain any remaining lines that arrived between the last read and
	// CmdDoneMsg delivery. In practice LinesCh is already closed by the time
	// we get here (readRunnerOutput drains it), so this is a safety net.
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

	// On failure: keep the session with "failed" status so the badge persists.
	// The user can retry via the action menu (which calls cleanupDevSessionByKey first).
	if result.ExitCode != 0 || result.Err != nil {
		dr.status = "failed"
		dr.runner = nil // mark as no longer running
		errText := "exited"
		if result.Err != nil {
			errText = result.Err.Error()
			if len(errText) > 40 {
				errText = errText[:40] + "..."
			}
		}
		dr.errMsg = errText
		if dr.logFile != nil {
			dr.logFile.Close()
			dr.logFile = nil
		}
		m.syncDevBadges()

		if dr.logPath != "" {
			m.err = fmt.Errorf("dev server failed — see %s", dr.logPath)
		}
		return nil
	}

	// Normal exit (exit code 0): clean up completely
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
// currently focused project/env. Shows the CmdPane for active or recently
// completed commands. Cleans up completed entries for non-focused envs.
func (m *Model) refreshActiveCmdPane() {
	projectName := m.wrangler.FocusedProjectName()
	envName := m.wrangler.FocusedEnvName()
	if projectName == "" || envName == "" {
		m.wrangler.SetActiveCmdPane(nil)
		// Clean up all completed cmdRunners since nothing is focused
		m.cleanupCompletedCmdRunners("")
		return
	}

	focusedKey := runnerKey(projectName, envName)

	// Clean up completed cmdRunners for NON-focused envs (they've been seen)
	m.cleanupCompletedCmdRunners(focusedKey)

	// Show the CmdPane for the focused env if one exists
	if cr, ok := m.cmdRunners[focusedKey]; ok {
		m.wrangler.SetActiveCmdPane(&cr.cmdPane)
		return
	}

	// No runner for this env
	m.wrangler.SetActiveCmdPane(nil)
}

// cleanupCompletedCmdRunners removes finished cmdRunner entries (runner == nil)
// for all keys except exceptKey. This prevents memory accumulation.
func (m *Model) cleanupCompletedCmdRunners(exceptKey string) {
	for key, cr := range m.cmdRunners {
		if key != exceptKey && cr.runner == nil {
			delete(m.cmdRunners, key)
		}
	}
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

// --- Version History (Resources tab — Workers detail view) ---

// fetchVersionHistory runs `wrangler versions list --name <script> --json` and
// `wrangler deployments list --name <script> --json` in parallel, merges the
// results, and returns a detail.VersionHistoryLoadedMsg.
func (m *Model) fetchVersionHistory(scriptName string) tea.Cmd {
	// Cancel any in-flight version history fetches
	if m.vhVersionRunner != nil {
		m.vhVersionRunner.Stop()
	}
	if m.vhDeploymentRunner != nil {
		m.vhDeploymentRunner.Stop()
	}

	accountID := m.registry.ActiveAccountID()

	versionRunner := wcfg.NewRunner()
	deployRunner := wcfg.NewRunner()
	m.vhVersionRunner = versionRunner
	m.vhDeploymentRunner = deployRunner

	return func() tea.Msg {
		ctx := context.Background()

		// Start both commands
		versionCmd := wcfg.Command{
			Action:    "versions list",
			ExtraArgs: []string{"--name", scriptName, "--json"},
			AccountID: accountID,
		}
		deployCmd := wcfg.Command{
			Action:    "deployments list",
			ExtraArgs: []string{"--name", scriptName, "--json"},
			AccountID: accountID,
		}

		if err := versionRunner.Start(ctx, versionCmd); err != nil {
			return detail.VersionHistoryLoadedMsg{
				ScriptName: scriptName,
				Err:        fmt.Errorf("failed to start versions list: %w", err),
			}
		}
		if err := deployRunner.Start(ctx, deployCmd); err != nil {
			versionRunner.Stop()
			return detail.VersionHistoryLoadedMsg{
				ScriptName: scriptName,
				Err:        fmt.Errorf("failed to start deployments list: %w", err),
			}
		}

		// Collect stdout from both runners in parallel via goroutines
		type result struct {
			json string
			err  error
		}
		versionCh := make(chan result, 1)
		deployCh := make(chan result, 1)

		collectJSON := func(r *wcfg.Runner, ch chan<- result, label string) {
			var buf strings.Builder
			for line := range r.LinesCh() {
				if !line.IsStderr {
					buf.WriteString(line.Text)
					buf.WriteByte('\n')
				}
			}
			res := <-r.DoneCh()
			if res.Err != nil && res.ExitCode != 0 {
				ch <- result{err: fmt.Errorf("wrangler %s failed (exit %d)", label, res.ExitCode)}
				return
			}
			ch <- result{json: buf.String()}
		}

		go collectJSON(versionRunner, versionCh, "versions list")
		go collectJSON(deployRunner, deployCh, "deployments list")

		vResult := <-versionCh
		dResult := <-deployCh

		if vResult.err != nil {
			return detail.VersionHistoryLoadedMsg{ScriptName: scriptName, Err: vResult.err}
		}
		if dResult.err != nil {
			return detail.VersionHistoryLoadedMsg{ScriptName: scriptName, Err: dResult.err}
		}

		// Parse
		versions, err := wcfg.ParseVersionsJSON([]byte(vResult.json))
		if err != nil {
			return detail.VersionHistoryLoadedMsg{ScriptName: scriptName, Err: fmt.Errorf("parse versions: %w", err)}
		}
		deployments, err := wcfg.ParseDeploymentsJSON([]byte(dResult.json))
		if err != nil {
			return detail.VersionHistoryLoadedMsg{ScriptName: scriptName, Err: fmt.Errorf("parse deployments: %w", err)}
		}

		// Merge
		entries := wcfg.BuildVersionHistory(versions, deployments)

		return detail.VersionHistoryLoadedMsg{
			ScriptName: scriptName,
			Entries:    entries,
		}
	}
}

// fetchBuildsForVersionHistory uses the Workers Builds API to fetch build
// metadata for CI-deployed versions and enriches the version history entries.
func (m *Model) fetchBuildsForVersionHistory(scriptName string) tea.Cmd {
	client := m.getBuildsClient()
	entries := m.detail.VersionHistory()

	// Send ALL version IDs — we can't know which have builds from the
	// wrangler source alone (Workers Builds uses wrangler deploy internally).
	versionIDs := make([]string, len(entries))
	for i, e := range entries {
		versionIDs[i] = e.VersionID
	}

	return func() tea.Msg {
		ctx := context.Background()

		buildsByVersion, err := client.GetBuildsByVersionIDs(ctx, versionIDs)
		if err != nil {
			// Auth error (401/403) — credentials lack Workers CI Read scope.
			if api.IsAuthError(err) {
				return detail.BuildsAuthFailedMsg{ScriptName: scriptName}
			}
			// Other errors: non-fatal, return unenriched entries.
			return detail.BuildsEnrichedMsg{ScriptName: scriptName, Entries: entries}
		}

		// Convert api.BuildResult → wrangler.BuildInfo
		builds := make(map[string]wcfg.BuildInfo)
		for versionID, br := range buildsByVersion {
			builds[versionID] = wcfg.BuildInfo{
				BuildUUID:     br.BuildUUID,
				BuildOutcome:  br.BuildOutcome,
				Branch:        br.BuildTriggerMetadata.Branch,
				CommitHash:    br.BuildTriggerMetadata.CommitHash,
				CommitMessage: br.BuildTriggerMetadata.CommitMessage,
				Author:        br.BuildTriggerMetadata.Author,
				RepoName:      br.BuildTriggerMetadata.RepoName,
				ProviderType:  br.BuildTriggerMetadata.ProviderType,
			}
		}

		// Enrich entries in place
		wcfg.EnrichWithBuilds(entries, builds)

		return detail.BuildsEnrichedMsg{
			ScriptName: scriptName,
			Entries:    entries,
		}
	}
}

// fetchBuildLog fetches the build log for a specific build UUID.
func (m *Model) fetchBuildLog(buildUUID string, entry wcfg.VersionHistoryEntry) tea.Cmd {
	client := m.getBuildsClient()

	return func() tea.Msg {
		ctx := context.Background()

		logLines, err := client.GetBuildLog(ctx, buildUUID)
		if err != nil {
			return detail.BuildLogLoadedMsg{
				BuildUUID: buildUUID,
				Err:       err,
				Entry:     entry,
			}
		}

		// Convert api.LogLine → plain strings
		lines := make([]string, len(logLines))
		for i, l := range logLines {
			lines[i] = l.Message
		}

		return detail.BuildLogLoadedMsg{
			BuildUUID: buildUUID,
			Lines:     lines,
			Entry:     entry,
		}
	}
}
