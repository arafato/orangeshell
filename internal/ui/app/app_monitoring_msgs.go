package app

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/api"
	"github.com/oarafat/orangeshell/internal/config"
	svc "github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/monitoring"
)

// analyticsTokenProvisionedMsg is sent when a fallback token has been
// re-provisioned with analytics scope. On success the analytics fetch is retried.
type analyticsTokenProvisionedMsg struct {
	token          string
	tokenID        string // Cloudflare token UUID
	accountID      string
	err            error
	scriptName     string // worker to retry analytics for
	timeRangeIndex int
}

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
		// User pressed space on a worker in the tree — add to grid and start tail
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
		// User pressed space on a worker in the tree — stop tail and remove from grid
		if m.isDevWorker(msg.ScriptName) {
			// Dev worker: remove from grid but don't stop the dev process.
			// Output continues flowing to CmdPane. User can re-add via space.
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

	case monitoring.AnalyticsRequestMsg:
		// User pressed 'a' on a worker or changed time range — fetch analytics
		if !m.monitoring.ShowAnalytics() {
			m.monitoring.OpenAnalytics(msg.ScriptName)
		}
		return *m, m.fetchAnalyticsCmd(msg.ScriptName, msg.TimeRangeIndex), true

	case monitoring.AnalyticsDataMsg:
		if msg.Err != nil {
			// If auth error: try to re-provision the fallback token with analytics scope
			if api.IsAuthError(msg.Err) {
				return m.handleAnalyticsAuthError(msg)
			}
			m.monitoring.SetAnalyticsError(msg.Err)
		} else {
			m.monitoring.SetAnalyticsMetrics(msg.Data)
		}
		return *m, nil, true

	case analyticsTokenProvisionedMsg:
		return m.handleAnalyticsTokenProvisioned(msg)

	case monitoring.AnalyticsCloseMsg:
		m.monitoring.CloseAnalytics()
		return *m, nil, true
	}

	return *m, nil, false
}

// handleAnalyticsAuthError handles an analytics auth failure by attempting to
// re-provision the fallback token with the Account Analytics Read scope.
// If CLOUDFLARE_API_KEY + CLOUDFLARE_EMAIL env vars are not available, it shows
// a toast explaining the required setup (same pattern as restricted mode).
func (m *Model) handleAnalyticsAuthError(msg monitoring.AnalyticsDataMsg) (Model, tea.Cmd, bool) {
	// Can we re-provision? Need Global API Key + Email.
	if m.cfg.Email != "" && m.cfg.APIKey != "" {
		// Re-provision the fallback token with updated scopes (now includes analytics)
		email := m.cfg.Email
		apiKey := m.cfg.APIKey
		accountID := m.registry.ActiveAccountID()
		scriptName := msg.ScriptName
		trIdx := 2 // default 24h
		if m.monitoring.ShowAnalytics() {
			// Preserve current time range from the analytics view
			for i, tr := range api.TimeRanges {
				if tr.Label == m.monitoring.AnalyticsTimeRangeLabel() {
					trIdx = i
					break
				}
			}
		}

		m.setToast("Upgrading API token for analytics access...")
		return *m, tea.Batch(
			toastTick(),
			func() tea.Msg {
				result, err := api.CreateScopedToken(context.Background(), email, apiKey, accountID)
				return analyticsTokenProvisionedMsg{
					token:          result.Value,
					tokenID:        result.ID,
					accountID:      accountID,
					err:            err,
					scriptName:     scriptName,
					timeRangeIndex: trIdx,
				}
			},
		), true
	}

	// No Global API Key available — show error with instructions
	m.monitoring.SetAnalyticsError(fmt.Errorf(
		"analytics requires Account Analytics Read permission. " +
			"Set CLOUDFLARE_API_KEY and CLOUDFLARE_EMAIL env vars to auto-provision a token, " +
			"or use ctrl+p → Help to learn more"))
	return *m, nil, true
}

// handleAnalyticsTokenProvisioned processes the result of re-provisioning
// the fallback token with analytics scope. On success: saves the token,
// updates header badge, and retries the analytics fetch.
func (m *Model) handleAnalyticsTokenProvisioned(msg analyticsTokenProvisionedMsg) (Model, tea.Cmd, bool) {
	if m.isStaleAccount(msg.accountID) {
		return *m, nil, true
	}

	if msg.err != nil {
		m.monitoring.SetAnalyticsError(fmt.Errorf("failed to provision analytics token: %w", msg.err))
		return *m, nil, true
	}

	// Save the new token (replaces old fallback token for this account)
	m.cfg.SetFallbackToken(msg.accountID, msg.token)
	if msg.tokenID != "" {
		m.cfg.SetFallbackTokenID(msg.accountID, msg.tokenID)
	}
	_ = m.cfg.Save()

	// Re-wire WorkersService access auth with the new token
	if ws := m.getWorkersService(); ws != nil {
		ws.SetAccessAuth("", "", msg.token)
	}
	m.header.SetRestricted(false)

	m.setToast("Analytics token provisioned — loading data...")

	// Retry the analytics fetch with the new token
	return *m, tea.Batch(
		toastTick(),
		m.fetchAnalyticsCmd(msg.scriptName, msg.timeRangeIndex),
	), true
}

// fetchAnalyticsCmd creates a tea.Cmd that fetches worker analytics via GraphQL.
func (m *Model) fetchAnalyticsCmd(scriptName string, timeRangeIndex int) tea.Cmd {
	client := m.getAnalyticsClient()
	if client == nil {
		return func() tea.Msg {
			return monitoring.AnalyticsDataMsg{
				ScriptName: scriptName,
				Err:        fmt.Errorf("no API credentials available"),
			}
		}
	}

	tr := api.TimeRanges[0]
	if timeRangeIndex >= 0 && timeRangeIndex < len(api.TimeRanges) {
		tr = api.TimeRanges[timeRangeIndex]
	}

	return func() tea.Msg {
		ctx := context.Background()
		metrics, err := client.FetchWorkerMetrics(ctx, scriptName, tr)
		return monitoring.AnalyticsDataMsg{
			ScriptName: scriptName,
			Data:       metrics,
			Err:        err,
		}
	}
}

// getAnalyticsClient creates the GraphQL Analytics API client.
// Uses the same credential priority as getBuildsClient: fallback token first,
// then primary credentials based on auth method.
func (m *Model) getAnalyticsClient() *api.AnalyticsClient {
	accountID := m.registry.ActiveAccountID()

	// 1. Prefer dedicated per-account fallback token
	if ft := m.cfg.FallbackTokenFor(accountID); ft != "" {
		return api.NewAnalyticsClient(accountID, "", "", ft)
	}

	// 2. Use primary credentials based on auth method
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIKey:
		return api.NewAnalyticsClient(accountID, m.cfg.Email, m.cfg.APIKey, "")
	case config.AuthMethodAPIToken:
		return api.NewAnalyticsClient(accountID, "", "", m.cfg.APIToken)
	case config.AuthMethodOAuth:
		return api.NewAnalyticsClient(accountID, "", "", m.cfg.OAuthAccessToken)
	}

	return api.NewAnalyticsClient(accountID, "", "", "")
}
