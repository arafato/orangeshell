package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	uiwrangler "github.com/oarafat/orangeshell/internal/ui/wrangler"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// stopWranglerRunner cancels any running wrangler command.
func (m *Model) stopWranglerRunner() {
	if m.wranglerRunner != nil {
		m.wranglerRunner.Stop()
		m.wranglerRunner = nil
	}
}

// startWranglerCmd creates a Runner and starts the wrangler command.
func (m *Model) startWranglerCmd(action, envName string) tea.Cmd {
	if m.wranglerRunner != nil && m.wranglerRunner.IsRunning() {
		// Don't start a new command while one is running
		return nil
	}

	// Stop any active wrangler tail since the CmdPane is being taken over
	if m.tailSource == "wrangler" && m.tailSession != nil {
		m.stopTail()
	}

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		AccountID:  m.registry.ActiveAccountID(),
	}

	runner := wcfg.NewRunner()
	m.wranglerRunner = runner
	m.wranglerRunnerAction = action
	m.wrangler.StartCommand(action, envName)

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.CmdDoneMsg{Result: wcfg.RunResult{ExitCode: 1, Err: err}}
			}
			// Read first output line (or done signal)
			return readWranglerOutputMsg(runner)
		},
		m.wrangler.SpinnerInit(),
	)
}

// readWranglerOutputMsg reads the next output line or done signal from the runner.
// Since linesCh is closed before doneCh fires, we always drain lines first.
func readWranglerOutputMsg(runner *wcfg.Runner) tea.Msg {
	// Read lines until the channel is closed
	line, ok := <-runner.LinesCh()
	if ok {
		return uiwrangler.CmdOutputMsg{Line: line}
	}
	// Lines channel closed — all output consumed. Now read the result.
	result, ok := <-runner.DoneCh()
	if !ok {
		return uiwrangler.CmdDoneMsg{Result: wcfg.RunResult{}}
	}
	return uiwrangler.CmdDoneMsg{Result: result}
}

// waitForWranglerOutput returns a command that waits for the next output from the runner.
func waitForWranglerOutput(runner *wcfg.Runner) tea.Cmd {
	if runner == nil {
		return nil
	}
	return func() tea.Msg {
		return readWranglerOutputMsg(runner)
	}
}

// startWranglerCmdWithArgs creates a Runner and starts a wrangler command with extra arguments.
// Used for version deploy commands that need version specs and -y flag.
func (m *Model) startWranglerCmdWithArgs(action, envName string, extraArgs []string) tea.Cmd {
	if m.wranglerRunner != nil && m.wranglerRunner.IsRunning() {
		return nil
	}

	// Stop any active wrangler tail since the CmdPane is being taken over
	if m.tailSource == "wrangler" && m.tailSession != nil {
		m.stopTail()
	}

	cmd := wcfg.Command{
		Action:     action,
		ConfigPath: m.wrangler.ConfigPath(),
		EnvName:    envName,
		ExtraArgs:  extraArgs,
		AccountID:  m.registry.ActiveAccountID(),
	}

	runner := wcfg.NewRunner()
	m.wranglerRunner = runner
	m.wranglerRunnerAction = action
	m.wrangler.StartCommand(action, envName)

	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			if err := runner.Start(ctx, cmd); err != nil {
				return uiwrangler.CmdDoneMsg{Result: wcfg.RunResult{ExitCode: 1, Err: err}}
			}
			return readWranglerOutputMsg(runner)
		},
		m.wrangler.SpinnerInit(),
	)
}

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
		// Versions were served from cache — no fetch needed
		return nil
	}
	// Need to fetch versions in the background
	return m.fetchWranglerVersions(envName)
}
