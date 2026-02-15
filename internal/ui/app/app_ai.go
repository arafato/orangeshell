package app

import (
	"context"
	"fmt"

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
func (m Model) provisionAIWorker() tea.Cmd {
	pcfg := m.aiProvisionCfg()
	return func() tea.Msg {
		ctx := context.Background()
		result, err := uiai.Provision(ctx, pcfg, func(status string) {
			_ = status // progress callbacks for future UI updates
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

// refreshAIContextSources updates the AI tab's context sources from the monitoring grid.
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

// startAIChatStream starts a streaming AI chat request using the Workers AI client.
// It reads the streaming response channel and converts chunks into Bubble Tea messages.
func (m Model) startAIChatStream(userMessage string) tea.Cmd {
	workerURL := m.aiTab.WorkerURL()
	secret := m.aiTab.Secret()
	preset := m.aiTab.ModelPreset()

	// Build the system prompt with context from selected monitoring panes
	contextData := m.gatherAIContext()
	systemPrompt := uiai.BuildSystemPrompt(contextData)

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
