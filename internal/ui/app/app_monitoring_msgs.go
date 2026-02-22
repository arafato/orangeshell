package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/monitoring"
)

// handleMonitoringMsg handles all monitoring-related messages.
// Returns (model, cmd, handled).
func (m *Model) handleMonitoringMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case monitoring.TailStopMsg:
		m.stopTail()
		return *m, nil, true

	case monitoring.ParallelTailStopMsg:
		m.stopAllParallelTails()
		return *m, nil, true

	case monitoring.TailAddMsg:
		// User pressed 'a' on a worker in the tree — add to grid and start tail
		if m.isDevWorker(msg.ScriptName) {
			// Dev worker: output is already being piped. Just ensure it's in the grid.
			if ds := m.findDevSession(msg.ScriptName); ds != nil {
				m.monitoring.AddDevToGrid(ds.ScriptName, ds.DevKind)
			}
			return *m, nil, true
		}
		if m.client == nil {
			return *m, nil, true
		}
		m.monitoring.AddToGrid(msg.ScriptName, "")
		m.parallelTailActive = true
		accountID := m.registry.ActiveAccountID()
		return *m, m.startGridTailCmd(accountID, msg.ScriptName), true

	case monitoring.TailRemoveMsg:
		// User pressed 'd' on a worker in the tree — stop tail and remove from grid
		if m.isDevWorker(msg.ScriptName) {
			// Dev worker: remove from grid but don't stop the dev process.
			// Output continues flowing to CmdPane. User can re-add via 'a'.
			m.monitoring.RemoveFromGrid(msg.ScriptName)
			return *m, nil, true
		}
		m.stopGridTail(msg.ScriptName)
		m.monitoring.RemoveFromGrid(msg.ScriptName)
		return *m, nil, true

	case monitoring.TailToggleMsg:
		if m.isDevWorker(msg.ScriptName) {
			// Dev panes can't be toggled — they're always active while process runs
			return *m, nil, true
		}
		if m.client == nil {
			return *m, nil, true
		}
		if msg.Start {
			// Restart the tail for this pane
			m.parallelTailActive = true
			accountID := m.registry.ActiveAccountID()
			return *m, m.startGridTailCmd(accountID, msg.ScriptName), true
		}
		// Stop the tail for this pane (but keep in grid)
		m.stopGridTail(msg.ScriptName)
		m.monitoring.GridSetStopped(msg.ScriptName)
		return *m, nil, true

	case monitoring.TailToggleAllMsg:
		if m.client == nil {
			return *m, nil, true
		}
		if msg.Start {
			// Start tails for all stopped grid panes (skip dev panes — always active)
			m.parallelTailActive = true
			accountID := m.registry.ActiveAccountID()
			var cmds []tea.Cmd
			for _, script := range m.monitoring.AllGridPaneScripts() {
				if !m.hasGridTailSession(script) && !m.isDevWorker(script) {
					cmds = append(cmds, m.startGridTailCmd(accountID, script))
				}
			}
			if len(cmds) > 0 {
				return *m, tea.Batch(cmds...), true
			}
			return *m, nil, true
		}
		// Stop all grid tails (dev panes are unaffected — no API session to stop)
		m.stopAllGridTails()
		return *m, nil, true

	case monitoring.DevCronTriggerMsg:
		ds := m.findDevSession(msg.ScriptName)
		if ds == nil || ds.Port == "" {
			// No port yet — dev server hasn't announced it
			warnLines := []svc.TailLine{{
				Timestamp: time.Now(),
				Level:     "warn",
				Text:      "[orangeshell] Cannot trigger cron: dev server port not detected yet",
			}}
			m.monitoring.GridAppendLines(msg.ScriptName, warnLines)
			m.logExporter.WriteLines(msg.ScriptName, warnLines)
			return *m, nil, true
		}
		// Fire the cron trigger request in the background
		port := ds.Port
		scriptName := msg.ScriptName
		return *m, func() tea.Msg {
			return triggerDevCron(scriptName, port)
		}, true

	case devCronTriggerDoneMsg:
		if msg.Err != nil {
			errLines := []svc.TailLine{{
				Timestamp: time.Now(),
				Level:     "error",
				Text:      fmt.Sprintf("[orangeshell] Cron trigger failed: %v", msg.Err),
			}}
			m.monitoring.GridAppendLines(msg.ScriptName, errLines)
			m.logExporter.WriteLines(msg.ScriptName, errLines)
		} else {
			okLines := []svc.TailLine{{
				Timestamp: time.Now(),
				Level:     "system",
				Text:      "[orangeshell] Cron trigger fired (scheduled handler invoked)",
			}}
			m.monitoring.GridAppendLines(msg.ScriptName, okLines)
			m.logExporter.WriteLines(msg.ScriptName, okLines)
		}
		return *m, nil, true
	}

	return *m, nil, false
}
