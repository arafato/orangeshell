package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/config"
	uiai "github.com/oarafat/orangeshell/internal/ui/ai"
	"github.com/oarafat/orangeshell/internal/ui/tabbar"
)

// --- AI provisioning message types ---

// aiProvisionDoneMsg is sent when AI Worker provisioning completes.
type aiProvisionDoneMsg struct {
	WorkerURL string
	Secret    string
	Err       error
}

// aiDeprovisionDoneMsg is sent when AI Worker deprovisioning completes.
type aiDeprovisionDoneMsg struct {
	Err error
}

// aiProvisionProgressMsg carries a progress step string from the provisioning pipeline.
type aiProvisionProgressMsg struct {
	Status string
}

// aiProvisionCfg builds the ProvisionConfig from the app's current config.
func (m Model) aiProvisionCfg() uiai.ProvisionConfig {
	pcfg := uiai.ProvisionConfig{
		AccountID: m.cfg.AccountID,
	}
	switch m.cfg.AuthMethod {
	case config.AuthMethodAPIToken:
		pcfg.APIToken = m.cfg.APIToken
	case config.AuthMethodAPIKey:
		pcfg.APIKey = m.cfg.APIKey
		pcfg.Email = m.cfg.Email
	case config.AuthMethodOAuth:
		pcfg.APIToken = m.cfg.OAuthAccessToken
		// Strip API Key env vars — they may be scoped to a different account
		pcfg.FilterEnv = []string{"CLOUDFLARE_API_KEY", "CLOUDFLARE_EMAIL"}
	}
	return pcfg
}

// provisionAIWorker starts the AI Worker deployment flow:
// download template → npm install → wrangler deploy → generate secret → wrangler secret put.
// Progress messages are sent via p.Send() for real-time UI updates.
func (m Model) provisionAIWorker() tea.Cmd {
	pcfg := m.aiProvisionCfg()
	prog := m.program // capture for goroutine
	return func() tea.Msg {
		ctx := context.Background()
		result, err := uiai.Provision(ctx, pcfg, func(status string) {
			if prog != nil {
				prog.Send(aiProvisionProgressMsg{Status: status})
			}
		})
		if err != nil {
			return aiProvisionDoneMsg{Err: err}
		}
		return aiProvisionDoneMsg{
			WorkerURL: result.WorkerURL,
			Secret:    result.Secret,
		}
	}
}

// deprovisionAIWorker removes the AI Worker from the user's account.
func (m Model) deprovisionAIWorker() tea.Cmd {
	pcfg := m.aiProvisionCfg()
	return func() tea.Msg {
		ctx := context.Background()
		err := uiai.Deprovision(ctx, pcfg)
		return aiDeprovisionDoneMsg{Err: err}
	}
}

// gatherAIContext collects log lines from selected monitoring panes for the AI prompt.
func (m Model) gatherAIContext() []uiai.ContextSourceData {
	selectedIDs := m.aiTab.SelectedContextIDs()
	if len(selectedIDs) == 0 {
		return nil
	}

	// Build a set of selected script IDs
	selectedSet := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selectedSet[id] = true
	}

	panes := m.monitoring.GridPanes()
	var result []uiai.ContextSourceData
	for _, p := range panes {
		if !selectedSet[p.ScriptName] {
			continue
		}

		name := p.ScriptName
		if p.IsDev && len(name) > 4 && name[:4] == "dev:" {
			name = name[4:]
		}

		lines := make([]uiai.TimestampedLine, len(p.Lines))
		for i, line := range p.Lines {
			lines[i] = uiai.TimestampedLine{
				Timestamp: line.Timestamp.UnixMilli(),
				Source:    name,
				Text:      line.Text,
			}
		}

		result = append(result, uiai.ContextSourceData{
			Name:  name,
			IsDev: p.IsDev,
			Lines: lines,
		})
	}
	return result
}

// refreshAIContextSources updates the AI tab's log context sources from the monitoring grid.
// This is called on every key press in the AI tab to keep line counts current.
// File sources are NOT refreshed here — use refreshAIFileSources() for that.
func (m *Model) refreshAIContextSources() {
	panes := m.monitoring.GridPanes()
	sources := make([]uiai.ContextSource, len(panes))
	for i, p := range panes {
		name := p.ScriptName
		// Strip dev: prefix for display
		if p.IsDev {
			displayName := name
			if len(name) > 4 && name[:4] == "dev:" {
				displayName = name[4:]
			}
			name = displayName
		}

		sources[i] = uiai.ContextSource{
			Name:      name,
			ScriptID:  p.ScriptName, // keep the full ID (with dev: prefix if applicable)
			IsDev:     p.IsDev,
			DevKind:   p.DevKind,
			Selected:  false, // SetSources preserves existing selection
			LineCount: p.LineCount,
			Active:    p.Active,
		}
	}
	m.aiTab.SetContextSources(sources)
}

// refreshAIFileSources scans wrangler project directories for source files
// and pushes them to the AI tab's context panel as file source toggles.
func (m *Model) refreshAIFileSources() {
	var fileSources []uiai.FileSource

	if m.wrangler.IsMonorepo() {
		for _, p := range m.wrangler.ProjectConfigs() {
			if p.Config == nil {
				continue
			}
			projectDir := filepath.Dir(p.ConfigPath)
			summary := uiai.ScanProjectFiles(projectDir, p.Config.Main)
			summary.ProjectName = p.Config.Name
			fileSources = append(fileSources, uiai.FileSource{
				ProjectName: p.Config.Name,
				ProjectDir:  projectDir,
				Summary:     summary,
			})
		}
	} else if m.wrangler.HasConfig() {
		cfg := m.wrangler.Config()
		if cfg != nil {
			projectDir := filepath.Dir(m.wrangler.ConfigPath())
			summary := uiai.ScanProjectFiles(projectDir, cfg.Main)
			summary.ProjectName = cfg.Name
			fileSources = append(fileSources, uiai.FileSource{
				ProjectName: cfg.Name,
				ProjectDir:  projectDir,
				Summary:     summary,
			})
		}
	}

	m.aiTab.SetFileSources(fileSources)
}

// gatherAIFileContext reads the contents of selected source files for the AI prompt.
func (m Model) gatherAIFileContext() []uiai.FileContextData {
	selectedFiles := m.aiTab.SelectedFileSources()
	if len(selectedFiles) == 0 {
		return nil
	}

	var result []uiai.FileContextData
	for _, fs := range selectedFiles {
		if fs.Summary == nil {
			continue
		}
		files := uiai.ReadProjectFiles(fs.Summary)
		result = append(result, files...)
	}
	return result
}

// startAIChatStream starts a streaming AI chat request using the active backend.
// It reads the streaming response channel and converts chunks into Bubble Tea messages.
// Uses a pointer receiver because it stores the cancel func for ESC interruption.
func (m *Model) startAIChatStream(userMessage string) tea.Cmd {
	backend := m.aiTab.Backend()
	if backend == nil {
		return func() tea.Msg {
			return uiai.AIChatStreamDoneMsg{Err: fmt.Errorf("no AI backend configured")}
		}
	}

	// Build the system prompt with context from selected monitoring panes and source files
	contextData := m.gatherAIContext()
	fileData := m.gatherAIFileContext()
	systemPrompt := uiai.BuildSystemPrompt(contextData, fileData)

	// Build the full message list: system + conversation history + new user message
	messages := []uiai.ChatMessage{
		{Role: uiai.RoleSystem, Content: systemPrompt},
	}
	messages = append(messages, m.aiTab.ConversationMessages()...)

	// Create a cancellable context so ESC can abort the stream.
	ctx, cancel := context.WithCancel(context.Background())
	m.aiStreamCancel = cancel
	m.aiStreamGen++
	gen := m.aiStreamGen

	return func() tea.Msg {
		ch := backend.StreamResponse(ctx, messages)

		// Read the first chunk to start streaming
		chunk, ok := <-ch
		if !ok {
			return aiStreamDoneMsg{gen: gen}
		}

		// Check for error messages from the client (same check as readNextAIChunk)
		if len(chunk) > 6 && chunk[:6] == "error:" {
			return aiStreamDoneMsg{
				inner: uiai.AIChatStreamDoneMsg{Err: fmt.Errorf("%s", chunk[7:])},
				gen:   gen,
			}
		}

		// Return the first chunk and a continuation command to read more
		return aiStreamBatchMsg{
			firstChunk: chunk,
			ch:         ch,
			gen:        gen,
		}
	}
}

// aiStreamBatchMsg carries the first chunk and the remaining channel for
// the streaming pipeline. This allows us to convert the channel-based flow
// into Bubble Tea's message-based architecture.
type aiStreamBatchMsg struct {
	firstChunk string
	ch         <-chan string
	gen        uint64 // stream generation — stale messages are dropped
}

// aiStreamDoneMsg is the internal (app-layer) wrapper around AIChatStreamDoneMsg
// that carries the stream generation for stale-message detection.
type aiStreamDoneMsg struct {
	inner uiai.AIChatStreamDoneMsg
	gen   uint64
}

// readNextAIChunk reads the next chunk from the streaming channel.
func readNextAIChunk(ch <-chan string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return aiStreamDoneMsg{gen: gen}
		}
		// Check for error messages from the client
		if len(chunk) > 6 && chunk[:6] == "error:" {
			return aiStreamDoneMsg{
				inner: uiai.AIChatStreamDoneMsg{Err: fmt.Errorf("%s", chunk[7:])},
				gen:   gen,
			}
		}
		return aiStreamContinueMsg{
			chunk: chunk,
			ch:    ch,
			gen:   gen,
		}
	}
}

// aiStreamContinueMsg carries a chunk and the channel to read more.
type aiStreamContinueMsg struct {
	chunk string
	ch    <-chan string
	gen   uint64 // stream generation — stale messages are dropped
}

// aiPermissionResponseDoneMsg signals that an HTTP POST to the OpenCode
// permissions API completed. Carries the permission ID for logging/debugging.
type aiPermissionResponseDoneMsg struct {
	PermissionID string
	Err          error
}

// processStreamChunk inspects a raw chunk from the backend channel.
// If it's a permission event, it routes it to the AI tab and returns true
// (the chunk should NOT be delivered as text). Otherwise returns false.
func (m *Model) processStreamChunk(chunk string) (handled bool, cmd tea.Cmd) {
	// Permission request: "permission:{json}"
	// Handles both legacy "permission.updated" and new "permission.asked" events.
	// Legacy has: id, sessionID, type, message, metadata
	// New has: id, sessionID, permission, patterns, metadata, always, tool
	if strings.HasPrefix(chunk, "permission:") {
		jsonStr := chunk[len("permission:"):]
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &props); err != nil {
			return false, nil // malformed — pass through as text
		}
		permID, _ := props["id"].(string)
		sessionID, _ := props["sessionID"].(string)
		if permID == "" || sessionID == "" {
			return false, nil
		}
		// Extract human-readable title — "message" field in both legacy and new systems.
		title, _ := props["message"].(string)
		// Extract permission type — legacy uses "type", new uses "permission".
		permType, _ := props["type"].(string)
		if permType == "" {
			permType, _ = props["permission"].(string)
		}
		// If no message, try to build one from patterns (new system)
		if title == "" {
			if patterns, ok := props["patterns"].([]interface{}); ok && len(patterns) > 0 {
				if first, ok := patterns[0].(string); ok {
					title = first
				}
			}
		}
		if title == "" {
			title = permType // last resort: use the type name
		}
		m.aiTab, cmd = m.aiTab.Update(uiai.AIChatPermissionMsg{
			ID:        permID,
			SessionID: sessionID,
			Title:     title,
			Type:      permType,
		})
		return true, cmd
	}

	// Permission resolved: "permission_resolved:{permID}"
	// The backend normalizes both legacy "permissionID" and new "requestID"
	// into a single ID string.
	if strings.HasPrefix(chunk, "permission_resolved:") {
		permID := chunk[len("permission_resolved:"):]
		m.aiTab, cmd = m.aiTab.Update(uiai.AIChatPermissionResolvedMsg{
			PermissionID: permID,
		})
		return true, cmd
	}

	// Status update: "status:{status}" (e.g., "status:busy" during tool execution)
	if strings.HasPrefix(chunk, "status:") {
		status := chunk[len("status:"):]
		m.aiTab, cmd = m.aiTab.Update(uiai.AIChatStatusMsg{Status: status})
		return true, cmd
	}

	return false, nil
}

// sendPermissionResponse sends an HTTP POST to the OpenCode permissions API.
// Tries the new PermissionNext endpoint first (POST /permission/:id/reply),
// then falls back to the documented session-scoped endpoint
// (POST /session/:id/permissions/:permissionID).
func sendPermissionResponse(baseURL, sessionID, permissionID, response, apiKey string) tea.Cmd {
	return func() tea.Msg {
		// Try the new PermissionNext route first.
		url := fmt.Sprintf("%s/permission/%s/reply", baseURL, permissionID)
		body := map[string]interface{}{
			"reply": response,
		}
		msg := tryPermissionPost(url, body, permissionID, apiKey)
		if msg.Err == nil {
			return msg
		}
		// Fall back to the session-scoped route (documented API).
		url = fmt.Sprintf("%s/session/%s/permissions/%s", baseURL, sessionID, permissionID)
		body = map[string]interface{}{
			"response": response,
		}
		return tryPermissionPost(url, body, permissionID, apiKey)
	}
}

// tryPermissionPost performs a single HTTP POST for permission response.
func tryPermissionPost(url string, body map[string]interface{}, permissionID, apiKey string) aiPermissionResponseDoneMsg {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return aiPermissionResponseDoneMsg{PermissionID: permissionID, Err: err}
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return aiPermissionResponseDoneMsg{PermissionID: permissionID, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return aiPermissionResponseDoneMsg{PermissionID: permissionID, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiPermissionResponseDoneMsg{
			PermissionID: permissionID,
			Err:          fmt.Errorf("permissions API returned %d", resp.StatusCode),
		}
	}
	return aiPermissionResponseDoneMsg{PermissionID: permissionID}
}

// handleAIMsg handles all AI-related messages.
// Returns (model, cmd, handled).
func (m *Model) handleAIMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// ESC while streaming on the AI tab → cancel the active stream
		if msg.String() == "esc" && m.activeTab == tabbar.TabAI && m.aiTab.IsStreaming() {
			var cmds []tea.Cmd

			// If a permission prompt is pending, reject it before aborting
			// so the OpenCode agent isn't left waiting for a response.
			if permID, sessID := m.aiTab.PendingPermissionInfo(); permID != "" {
				if b := m.aiTab.Backend(); b != nil {
					if hb, ok := b.(*uiai.HTTPBackend); ok {
						cmds = append(cmds, sendPermissionResponse(
							hb.BaseURL, sessID, permID, "reject", hb.APIKey))
					}
				}
			}

			if m.aiStreamCancel != nil {
				m.aiStreamCancel()
				m.aiStreamCancel = nil
			}
			// Abort the server-side agent prompt without clearing the session.
			// The sawBusy flag in readOpenCodeEvents protects against stale
			// idle events, so we can safely reuse the session for multi-turn
			// context. For stateless backends (Workers AI, OpenAI) this is a
			// no-op. Only HTTPBackend (OpenCode) has Abort().
			if b := m.aiTab.Backend(); b != nil {
				if hb, ok := b.(*uiai.HTTPBackend); ok {
					hb.Abort()
				}
			}
			// Tell the chat model the stream was cancelled (not an error)
			m.aiTab, _ = m.aiTab.Update(uiai.AIChatStreamDoneMsg{})
			return *m, tea.Batch(cmds...), true
		}
		return *m, nil, false

	case uiai.AIConfigSaveMsg:
		m.cfg.AIProvider = msg.Provider
		m.cfg.AIModelPreset = msg.ModelPreset
		m.cfg.AIBackendType = msg.BackendType
		m.cfg.AIHTTPEndpoint = msg.HTTPEndpoint
		m.cfg.AIHTTPProtocol = msg.HTTPProtocol
		m.cfg.AIHTTPModel = msg.HTTPModel
		m.cfg.AIHTTPAPIKey = msg.HTTPAPIKey
		_ = m.cfg.Save()
		m.aiTab.RebuildBackend()
		return *m, nil, true

	case uiai.AIProvisionRequestMsg:
		m.aiTab.SetDeploying(true)
		return *m, m.provisionAIWorker(), true

	case uiai.AIDeprovisionRequestMsg:
		m.aiTab.SetDeploying(true)
		return *m, m.deprovisionAIWorker(), true

	case aiProvisionProgressMsg:
		m.aiTab.SetDeployProgress(msg.Status)
		return *m, nil, true

	case aiProvisionDoneMsg:
		m.aiTab.SetDeploying(false)
		m.aiTab.SetDeployProgress("")
		if msg.Err != nil {
			m.aiTab.SetDeployError(msg.Err.Error())
		} else {
			m.aiTab.SetDeployError("")
			m.aiTab.SetWorkerURL(msg.WorkerURL)
			m.aiTab.SetWorkerSecret(msg.Secret)
			m.cfg.AIWorkerURL = msg.WorkerURL
			m.cfg.AIWorkerSecret = msg.Secret
			m.cfg.AIProvider = config.AIProviderWorkersAI
			_ = m.cfg.Save()
			m.aiTab.RebuildBackend()
			m.setToast("AI Worker deployed successfully")
		}
		return *m, nil, true

	case aiDeprovisionDoneMsg:
		m.aiTab.SetDeploying(false)
		if msg.Err != nil {
			m.aiTab.SetDeployError(msg.Err.Error())
		} else {
			m.aiTab.SetDeployError("")
			m.aiTab.SetWorkerURL("")
			m.cfg.AIWorkerURL = ""
			m.cfg.AIWorkerSecret = ""
			_ = m.cfg.Save()
			m.aiTab.RebuildBackend()
			m.setToast("AI Worker removed")
		}
		return *m, nil, true

	case uiai.AIChatSendMsg:
		// User pressed enter in the chat input — start streaming AI response
		if !m.aiTab.IsProvisioned() {
			return *m, nil, true
		}
		// Batch spinner init alongside the stream start so the spinner ticks immediately.
		return *m, tea.Batch(m.startAIChatStream(msg.UserMessage), m.aiTab.SpinnerInit()), true

	case aiStreamBatchMsg:
		// First chunk arrived — deliver it and start reading more.
		// Drop stale messages from a cancelled/previous stream.
		if msg.gen != m.aiStreamGen || !m.aiTab.IsStreaming() {
			return *m, nil, true
		}
		// Check if this is a permission event rather than text content.
		if handled, permCmd := m.processStreamChunk(msg.firstChunk); handled {
			return *m, tea.Batch(permCmd, readNextAIChunk(msg.ch, msg.gen)), true
		}
		m.aiTab, _ = m.aiTab.Update(uiai.AIChatStreamChunkMsg{Text: msg.firstChunk})
		return *m, readNextAIChunk(msg.ch, msg.gen), true

	case aiStreamContinueMsg:
		// Subsequent chunk — deliver and continue reading.
		// Drop stale messages from a cancelled/previous stream.
		if msg.gen != m.aiStreamGen || !m.aiTab.IsStreaming() {
			return *m, nil, true
		}
		// Check if this is a permission event rather than text content.
		if handled, permCmd := m.processStreamChunk(msg.chunk); handled {
			return *m, tea.Batch(permCmd, readNextAIChunk(msg.ch, msg.gen)), true
		}
		m.aiTab, _ = m.aiTab.Update(uiai.AIChatStreamChunkMsg{Text: msg.chunk})
		return *m, readNextAIChunk(msg.ch, msg.gen), true

	case aiStreamDoneMsg:
		// Stream completed (naturally or via cancellation).
		// Drop stale done messages from a previous/cancelled stream.
		if msg.gen != m.aiStreamGen {
			return *m, nil, true
		}
		m.aiStreamCancel = nil
		m.aiTab, _ = m.aiTab.Update(msg.inner)
		return *m, nil, true

	case uiai.AIChatNewConversationMsg:
		m.aiTab.NewConversation()
		// Clear the backend session so the next prompt starts fresh.
		// For OpenCode this aborts + clears the session ID, creating a
		// new multi-turn context. For stateless backends it's a no-op.
		if b := m.aiTab.Backend(); b != nil {
			_ = b.Close()
		}
		return *m, nil, true

	case uiai.AIPermissionResponseMsg:
		// User responded to a permission prompt (y/a/n). POST to OpenCode API.
		backend := m.aiTab.Backend()
		if backend == nil {
			return *m, nil, true
		}
		httpBackend, ok := backend.(*uiai.HTTPBackend)
		if !ok {
			return *m, nil, true
		}
		baseURL := httpBackend.BaseURL
		apiKey := httpBackend.APIKey
		return *m, sendPermissionResponse(baseURL, msg.SessionID, msg.PermissionID, msg.Response, apiKey), true

	case aiPermissionResponseDoneMsg:
		// Permission response sent (or failed). Log errors but don't disrupt the UI.
		if msg.Err != nil {
			m.setToast(fmt.Sprintf("Permission response failed: %v", msg.Err))
		}
		return *m, nil, true
	}

	return *m, nil, false
}
