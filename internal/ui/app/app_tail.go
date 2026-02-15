package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/detail"
)

// --- Single tail lifecycle helpers ---

// startTailCmd returns a command that creates a tail session via the API.
func (m Model) startTailCmd(accountID, scriptName string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx := context.Background()
		session, err := svc.StartTail(ctx, client.CF, accountID, scriptName)
		if err != nil {
			return detail.TailErrorMsg{Err: err}
		}
		return detail.TailStartedMsg{Session: session}
	}
}

// waitForTailLines returns a command that blocks on the tail session's channel
// and returns a TailLogMsg when new lines arrive.
func (m Model) waitForTailLines() tea.Cmd {
	session := m.tailSession
	if session == nil {
		return nil
	}
	return func() tea.Msg {
		lines, ok := <-session.LinesChan()
		if !ok {
			// Channel closed — tail ended
			return detail.TailStoppedMsg{}
		}
		return detail.TailLogMsg{Lines: lines}
	}
}

// stopTail closes the active tail session and cleans up.
func (m *Model) stopTail() {
	if m.tailSession == nil {
		return
	}
	// Stop in a background goroutine to avoid blocking the UI
	session := m.tailSession
	client := m.client
	m.tailSession = nil

	// Clean up the monitoring model's single-tail state
	m.monitoring.SetTailStopped()
	m.tailSource = ""

	go func() {
		ctx := context.Background()
		svc.StopTail(ctx, client.CF, session)
	}()
}

// --- Parallel tail lifecycle helpers ---

// startParallelTailSessionCmd returns a command that creates a single parallel tail session.
func (m Model) startParallelTailSessionCmd(accountID, scriptName string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx := context.Background()
		session, err := svc.StartTail(ctx, client.CF, accountID, scriptName)
		if err != nil {
			return parallelTailErrorMsg{ScriptName: scriptName, Err: err}
		}
		return parallelTailStartedMsg{ScriptName: scriptName, Session: session}
	}
}

// waitForParallelTailLines returns a command that blocks on a single parallel tail
// session's channel and returns a parallelTailLogMsg when new lines arrive.
func (m Model) waitForParallelTailLines(scriptName string, session *svc.TailSession) tea.Cmd {
	if session == nil {
		return nil
	}
	return func() tea.Msg {
		lines, ok := <-session.LinesChan()
		if !ok {
			// Channel closed — session ended
			return parallelTailSessionDoneMsg{ScriptName: scriptName}
		}
		return parallelTailLogMsg{ScriptName: scriptName, Lines: lines}
	}
}

// stopAllParallelTails closes all parallel tail sessions and cleans up state.
func (m *Model) stopAllParallelTails() {
	if !m.parallelTailActive {
		return
	}
	sessions := m.parallelTailSessions
	client := m.client
	m.parallelTailSessions = nil
	m.parallelTailActive = false
	m.monitoring.StopParallelTail()

	// Stop all sessions in the background to avoid blocking the UI
	if len(sessions) > 0 {
		go func() {
			ctx := context.Background()
			for _, s := range sessions {
				svc.StopTail(ctx, client.CF, s)
			}
		}()
	}
}

// --- Grid tail lifecycle helpers (for dual-pane monitoring) ---

// startGridTailCmd returns a command that creates a tail session for a grid pane.
// Reuses the same message types as parallel tail so existing handlers route data correctly.
func (m Model) startGridTailCmd(accountID, scriptName string) tea.Cmd {
	return m.startParallelTailSessionCmd(accountID, scriptName)
}

// hasGridTailSession returns whether an active tail session exists for the given script.
func (m Model) hasGridTailSession(scriptName string) bool {
	for _, s := range m.parallelTailSessions {
		if s.ScriptName == scriptName {
			return true
		}
	}
	return false
}

// stopGridTail stops the tail session for a specific script name and removes it
// from the session slice. Does not modify the monitoring model's grid state.
func (m *Model) stopGridTail(scriptName string) {
	client := m.client
	for i, s := range m.parallelTailSessions {
		if s.ScriptName == scriptName {
			session := s
			m.parallelTailSessions = append(m.parallelTailSessions[:i], m.parallelTailSessions[i+1:]...)
			go func() {
				ctx := context.Background()
				svc.StopTail(ctx, client.CF, session)
			}()
			return
		}
	}
}

// stopAllGridTails stops all grid tail sessions and marks all panes as stopped.
// Dev panes are not affected — they have no API session and remain active.
func (m *Model) stopAllGridTails() {
	sessions := m.parallelTailSessions
	client := m.client
	m.parallelTailSessions = nil
	m.parallelTailActive = false

	// Mark all non-dev grid panes as stopped
	for _, script := range m.monitoring.AllGridPaneScripts() {
		if !m.isDevWorker(script) {
			m.monitoring.GridSetStopped(script)
		}
	}

	if len(sessions) > 0 {
		go func() {
			ctx := context.Background()
			for _, s := range sessions {
				svc.StopTail(ctx, client.CF, s)
			}
		}()
	}
}
