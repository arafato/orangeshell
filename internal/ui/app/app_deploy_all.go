package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oarafat/orangeshell/internal/config"
	"github.com/oarafat/orangeshell/internal/ui/deployallpopup"
	wcfg "github.com/oarafat/orangeshell/internal/wrangler"
)

// startDeployAll builds the deploy items for an environment, creates the popup,
// and spawns parallel deploy commands for all matching projects.
func (m *Model) startDeployAll(envName string) tea.Cmd {
	var items []deployallpopup.DeployItem
	for _, pc := range m.wrangler.ProjectConfigs() {
		if pc.Config == nil {
			continue
		}
		if !pc.Config.HasEnv(envName) {
			continue
		}
		items = append(items, deployallpopup.DeployItem{
			ProjectName: pc.Config.Name,
			ConfigPath:  pc.ConfigPath,
			EnvName:     envName,
			Status:      deployallpopup.StatusDeploying,
		})
	}

	if len(items) == 0 {
		return nil
	}

	m.deployAllPopup = deployallpopup.New(envName, items)
	m.showDeployAllPopup = true

	// Resolve the API token once so all parallel runners share the same valid token.
	// This avoids OAuth refresh token race conditions when multiple wrangler processes
	// try to refresh the same token concurrently.
	accountID := m.registry.ActiveAccountID()
	apiToken := ""
	if m.cfg != nil {
		switch m.cfg.AuthMethod {
		case config.AuthMethodAPIToken:
			apiToken = m.cfg.APIToken
		case config.AuthMethodOAuth:
			apiToken = m.cfg.OAuthAccessToken
		}
	}

	// Create a runner per project and store them for cancellation
	runners := make([]*wcfg.Runner, len(items))
	var cmds []tea.Cmd
	for i, item := range items {
		runner := wcfg.NewRunner()
		runners[i] = runner
		cmds = append(cmds, m.deployProjectCmd(i, runner, item.ConfigPath, item.EnvName, accountID, apiToken))
	}
	m.deployAllRunners = runners

	cmds = append(cmds, m.deployAllPopup.SpinnerInit())
	return tea.Batch(cmds...)
}

// deployProjectCmd spawns a single wrangler deploy for one project in a goroutine.
// Output is captured into a buffer; only the final result is sent back as a message.
func (m Model) deployProjectCmd(idx int, runner *wcfg.Runner, configPath, envName, accountID, apiToken string) tea.Cmd {
	return func() tea.Msg {
		cmd := wcfg.Command{
			Action:     "deploy",
			ConfigPath: configPath,
			EnvName:    envName,
			AccountID:  accountID,
			APIToken:   apiToken,
		}

		ctx := context.Background()
		if err := runner.Start(ctx, cmd); err != nil {
			logPath := writeDeployLog(filepath.Base(filepath.Dir(configPath)), envName, []byte(err.Error()))
			return deployallpopup.ProjectDoneMsg{
				Index:   idx,
				Err:     err,
				LogPath: logPath,
			}
		}

		// Drain all output into a buffer
		var buf []byte
		for line := range runner.LinesCh() {
			buf = append(buf, []byte(line.Text+"\n")...)
		}

		result, ok := <-runner.DoneCh()
		if !ok {
			return deployallpopup.ProjectDoneMsg{Index: idx}
		}

		if result.Err != nil || result.ExitCode != 0 {
			err := result.Err
			if err == nil {
				err = fmt.Errorf("exit code %d", result.ExitCode)
			}
			logPath := writeDeployLog(filepath.Base(filepath.Dir(configPath)), envName, buf)
			return deployallpopup.ProjectDoneMsg{
				Index:   idx,
				Err:     err,
				LogPath: logPath,
			}
		}

		return deployallpopup.ProjectDoneMsg{Index: idx}
	}
}

// writeDeployLog writes deploy output to ~/.orangeshell/logs/ and returns the path.
// Returns empty string if writing fails (non-fatal).
func writeDeployLog(projectName, envName string, output []byte) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	logsDir := filepath.Join(home, ".orangeshell", "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return ""
	}
	ts := time.Now().Format("20060102T150405")
	filename := fmt.Sprintf("deploy-%s-%s-%s.log", projectName, envName, ts)
	logPath := filepath.Join(logsDir, filename)
	if err := os.WriteFile(logPath, output, 0644); err != nil {
		return ""
	}
	return logPath
}

// cancelDeployAllRunners stops all in-flight deploy runners.
func (m *Model) cancelDeployAllRunners() {
	for _, runner := range m.deployAllRunners {
		if runner != nil && runner.IsRunning() {
			runner.Stop()
		}
	}
	m.deployAllRunners = nil
}
