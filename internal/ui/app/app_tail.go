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

// stopTail closes the active tail session and cleans up both views.
func (m *Model) stopTail() {
	if m.tailSession == nil {
		return
	}
	// Stop in a background goroutine to avoid blocking the UI
	session := m.tailSession
	client := m.client
	m.tailSession = nil

	// Clean up the view that owns this tail
	if m.tailSource == "wrangler" {
		m.wrangler.StopTailPane()
	} else {
		m.detail.SetTailStopped()
	}
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
	m.wrangler.StopParallelTail()

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
