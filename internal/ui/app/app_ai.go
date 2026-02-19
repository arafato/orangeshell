package app

import (
	"context"
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/config"
	uiai "github.com/oarafat/orangeshell/internal/ui/ai"
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

// startAIChatStream starts a streaming AI chat request using the Workers AI client.
// It reads the streaming response channel and converts chunks into Bubble Tea messages.
func (m Model) startAIChatStream(userMessage string) tea.Cmd {
	workerURL := m.aiTab.WorkerURL()
	secret := m.aiTab.Secret()
	preset := m.aiTab.ModelPreset()

	// Build the system prompt with context from selected monitoring panes and source files
	contextData := m.gatherAIContext()
	fileData := m.gatherAIFileContext()
	systemPrompt := uiai.BuildSystemPrompt(contextData, fileData)

	// Build the full message list: system + conversation history + new user message
	messages := []uiai.ChatMessage{
		{Role: uiai.RoleSystem, Content: systemPrompt},
	}
	messages = append(messages, m.aiTab.ConversationMessages()...)

	modelID := uiai.ModelID(preset)

	return func() tea.Msg {
		client := &uiai.WorkersAIClient{
			WorkerURL: workerURL,
			Secret:    secret,
		}

		ctx := context.Background()
		ch := client.StreamResponse(ctx, modelID, messages)

		// Read the first chunk to start streaming
		chunk, ok := <-ch
		if !ok {
			return uiai.AIChatStreamDoneMsg{}
		}

		// Check for error messages from the client (same check as readNextAIChunk)
		if len(chunk) > 6 && chunk[:6] == "error:" {
			return uiai.AIChatStreamDoneMsg{
				Err: fmt.Errorf("%s", chunk[7:]),
			}
		}

		// Return the first chunk and a continuation command to read more
		return aiStreamBatchMsg{
			firstChunk: chunk,
			ch:         ch,
		}
	}
}

// aiStreamBatchMsg carries the first chunk and the remaining channel for
// the streaming pipeline. This allows us to convert the channel-based flow
// into Bubble Tea's message-based architecture.
type aiStreamBatchMsg struct {
	firstChunk string
	ch         <-chan string
}

// readNextAIChunk reads the next chunk from the streaming channel.
func readNextAIChunk(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return uiai.AIChatStreamDoneMsg{}
		}
		// Check for error messages from the client
		if len(chunk) > 6 && chunk[:6] == "error:" {
			return uiai.AIChatStreamDoneMsg{
				Err: fmt.Errorf("%s", chunk[7:]),
			}
		}
		return aiStreamContinueMsg{
			chunk: chunk,
			ch:    ch,
		}
	}
}

// aiStreamContinueMsg carries a chunk and the channel to read more.
type aiStreamContinueMsg struct {
	chunk string
	ch    <-chan string
}

// handleAIMsg handles all AI-related messages.
// Returns (model, cmd, handled).
func (m *Model) handleAIMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uiai.AIConfigSaveMsg:
		m.cfg.AIProvider = msg.Provider
		m.cfg.AIModelPreset = msg.ModelPreset
		_ = m.cfg.Save()
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
			m.setToast("AI Worker removed")
		}
		return *m, nil, true

	case uiai.AIChatSendMsg:
		// User pressed enter in the chat input — start streaming AI response
		if !m.aiTab.IsProvisioned() {
			return *m, nil, true
		}
		return *m, m.startAIChatStream(msg.UserMessage), true

	case aiStreamBatchMsg:
		// First chunk arrived — deliver it and start reading more
		m.aiTab, _ = m.aiTab.Update(uiai.AIChatStreamChunkMsg{Text: msg.firstChunk})
		return *m, readNextAIChunk(msg.ch), true

	case aiStreamContinueMsg:
		// Subsequent chunk — deliver and continue reading
		m.aiTab, _ = m.aiTab.Update(uiai.AIChatStreamChunkMsg{Text: msg.chunk})
		return *m, readNextAIChunk(msg.ch), true

	case uiai.AIChatNewConversationMsg:
		m.aiTab.NewConversation()
		return *m, nil, true
	}

	return *m, nil, false
}
