package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/tmaxmax/go-sse"
)

// HTTPProtocol identifies the wire protocol used by an HTTP endpoint backend.
type HTTPProtocol string

const (
	// ProtocolOpenAI uses the OpenAI-compatible /v1/chat/completions endpoint
	// with SSE streaming. Works with Ollama, LM Studio, vLLM, and any
	// server implementing the OpenAI chat completions API.
	ProtocolOpenAI HTTPProtocol = "openai"

	// ProtocolOpenCode uses the OpenCode serve API — session-based messaging
	// with SSE event streaming via /event.
	ProtocolOpenCode HTTPProtocol = "opencode"
)

// HTTPBackend implements Backend for HTTP endpoint services.
type HTTPBackend struct {
	BaseURL  string       // e.g. "http://localhost:11434" or "http://localhost:4096"
	Model    string       // model ID (required for OpenAI, optional for OpenCode)
	Protocol HTTPProtocol // wire protocol
	APIKey   string       // optional bearer token / API key

	// OpenCode-specific state: reuse session across messages for multi-turn.
	mu        sync.Mutex
	sessionID string
}

// NewHTTPBackend creates a Backend that talks to an HTTP endpoint.
func NewHTTPBackend(baseURL, model string, protocol HTTPProtocol, apiKey string) *HTTPBackend {
	// Normalize: strip trailing slash
	baseURL = strings.TrimRight(baseURL, "/")
	return &HTTPBackend{
		BaseURL:  baseURL,
		Model:    model,
		Protocol: protocol,
		APIKey:   apiKey,
	}
}

// StreamResponse implements Backend.
func (b *HTTPBackend) StreamResponse(ctx context.Context, messages []ChatMessage) <-chan string {
	switch b.Protocol {
	case ProtocolOpenCode:
		return b.streamOpenCode(ctx, messages)
	default:
		return b.streamOpenAI(ctx, messages)
	}
}

// Name implements Backend.
func (b *HTTPBackend) Name() string {
	switch b.Protocol {
	case ProtocolOpenCode:
		label := "OpenCode"
		if b.Model != "" {
			label += " (" + b.Model + ")"
		}
		return label
	default:
		label := "HTTP"
		if b.Model != "" {
			label += " (" + b.Model + ")"
		}
		return label
	}
}

// Close implements Backend. Clears the cached OpenCode session ID.
func (b *HTTPBackend) Close() error {
	b.mu.Lock()
	b.sessionID = ""
	b.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// OpenAI-compatible streaming
// ---------------------------------------------------------------------------

// openAIRequest is the JSON body for /v1/chat/completions.
type openAIRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// openAIDelta is the delta object inside a streaming chunk choice.
type openAIDelta struct {
	Content string `json:"content"`
}

// openAIChoice is a single choice in a streaming chunk.
type openAIChoice struct {
	Delta        openAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// openAIChunk is the JSON structure of each SSE data field from an
// OpenAI-compatible streaming response.
type openAIChunk struct {
	Choices []openAIChoice `json:"choices"`
}

func (b *HTTPBackend) streamOpenAI(ctx context.Context, messages []ChatMessage) <-chan string {
	ch := make(chan string, 64)

	go func() {
		defer close(ch)

		body := openAIRequest{
			Model:    b.Model,
			Messages: messages,
			Stream:   true,
		}

		jsonBody, err := json.Marshal(body)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to marshal request: %v", err)
			return
		}

		url := b.BaseURL + "/v1/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
		if err != nil {
			ch <- fmt.Sprintf("error: failed to create request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if b.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+b.APIKey)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- fmt.Sprintf("error: request failed: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			ch <- fmt.Sprintf("error: endpoint returned %d: %s", resp.StatusCode, string(bodyBytes))
			return
		}

		// Parse SSE stream
		for ev, err := range sse.Read(resp.Body, nil) {
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if err.Error() != "EOF" {
					ch <- fmt.Sprintf("\n\n[stream error: %v]", err)
				}
				return
			}

			data := ev.Data
			if data == "" {
				continue
			}

			// OpenAI sends "[DONE]" as the final message
			if strings.TrimSpace(data) == "[DONE]" {
				return
			}

			var chunk openAIChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Some endpoints send raw text; pass through
				ch <- data
				continue
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					ch <- choice.Delta.Content
				}
			}
		}
	}()

	return ch
}

// ---------------------------------------------------------------------------
// OpenCode serve streaming
// ---------------------------------------------------------------------------

// openCodeSession is the response from POST /session.
type openCodeSession struct {
	ID string `json:"id"`
}

// openCodePart is a message part sent to the OpenCode API.
type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// openCodePromptBody is the body for POST /session/:id/prompt_async.
// Note: OpenCode's tools field is Record<string, boolean> (not an array).
// We omit it entirely — tool use still happens server-side, but our client
// only renders text deltas. Full agent support (permissions, diffs, tool
// visualization) is deferred.
type openCodePromptBody struct {
	Parts  []openCodePart    `json:"parts"`
	Model  *openCodeModelRef `json:"model,omitempty"`
	System string            `json:"system,omitempty"`
}

// openCodeModelRef specifies a model for the OpenCode API.
type openCodeModelRef struct {
	ProviderID string `json:"providerID,omitempty"`
	ModelID    string `json:"modelID,omitempty"`
}

// openCodeEvent is a partial parse of an SSE event from OpenCode's /event stream.
// The event types we care about:
//   - "message.part.delta" — streaming text chunks (field="text", delta="...")
//   - "session.idle"       — session finished processing (completion signal)
//   - "session.status"     — status changes (properties.status.type = "idle"|"busy"|"error")
//
// Events we ignore: server.connected, message.updated, message.part.updated,
// session.updated, session.diff.
type openCodeEvent struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

func (b *HTTPBackend) streamOpenCode(ctx context.Context, messages []ChatMessage) <-chan string {
	ch := make(chan string, 64)

	go func() {
		defer close(ch)

		// 1. Ensure we have a session
		sessionID, err := b.ensureSession(ctx)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to create session: %v", err)
			return
		}

		// 2. Connect to the SSE event stream BEFORE sending the prompt
		//    so we don't miss any events.
		eventURL := b.BaseURL + "/event"
		eventReq, err := http.NewRequestWithContext(ctx, http.MethodGet, eventURL, nil)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to create event request: %v", err)
			return
		}
		if b.APIKey != "" {
			eventReq.Header.Set("Authorization", "Bearer "+b.APIKey)
		}

		eventResp, err := http.DefaultClient.Do(eventReq)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to connect to event stream: %v", err)
			return
		}
		defer eventResp.Body.Close()

		// 3. Build the prompt from the last user message.
		//    OpenCode manages its own conversation history via sessions,
		//    so we only send the latest user message. The system prompt
		//    is passed via the system field.
		var systemPrompt string
		var userMessage string
		for _, m := range messages {
			switch m.Role {
			case RoleSystem:
				systemPrompt = m.Content
			case RoleUser:
				userMessage = m.Content // take the last user message
			}
		}

		promptBody := openCodePromptBody{
			Parts: []openCodePart{
				{Type: "text", Text: userMessage},
			},
		}
		if systemPrompt != "" {
			promptBody.System = systemPrompt
		}
		if b.Model != "" {
			// Try to parse "provider/model" format
			parts := strings.SplitN(b.Model, "/", 2)
			if len(parts) == 2 {
				promptBody.Model = &openCodeModelRef{
					ProviderID: parts[0],
					ModelID:    parts[1],
				}
			} else {
				promptBody.Model = &openCodeModelRef{
					ModelID: b.Model,
				}
			}
		}

		jsonBody, err := json.Marshal(promptBody)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to marshal prompt: %v", err)
			return
		}

		// 4. Send the prompt asynchronously
		promptURL := fmt.Sprintf("%s/session/%s/prompt_async", b.BaseURL, sessionID)
		promptReq, err := http.NewRequestWithContext(ctx, http.MethodPost, promptURL, bytes.NewReader(jsonBody))
		if err != nil {
			ch <- fmt.Sprintf("error: failed to create prompt request: %v", err)
			return
		}
		promptReq.Header.Set("Content-Type", "application/json")
		if b.APIKey != "" {
			promptReq.Header.Set("Authorization", "Bearer "+b.APIKey)
		}

		promptResp, err := http.DefaultClient.Do(promptReq)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to send prompt: %v", err)
			return
		}
		defer promptResp.Body.Close()

		if promptResp.StatusCode != http.StatusOK && promptResp.StatusCode != http.StatusNoContent {
			respBody, _ := io.ReadAll(promptResp.Body)
			ch <- fmt.Sprintf("error: prompt_async returned %d: %s", promptResp.StatusCode, string(respBody))
			return
		}

		// 5. Read SSE events and extract text content from assistant messages
		//    belonging to our session.
		b.readOpenCodeEvents(ctx, eventResp.Body, sessionID, ch)
	}()

	return ch
}

// ensureSession creates or reuses an OpenCode session.
func (b *HTTPBackend) ensureSession(ctx context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sessionID != "" {
		return b.sessionID, nil
	}

	url := b.BaseURL + "/session"
	body := []byte(`{}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("session creation returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var session openCodeSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("failed to decode session: %w", err)
	}

	b.sessionID = session.ID
	return session.ID, nil
}

// readOpenCodeEvents reads SSE events from the /event stream and extracts
// text content from message.part.delta events that belong to the given session.
// Completion is signalled by session.idle or session.status with type "idle".
func (b *HTTPBackend) readOpenCodeEvents(ctx context.Context, body io.Reader, sessionID string, ch chan<- string) {
	for ev, err := range sse.Read(body, nil) {
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if err.Error() != "EOF" {
				ch <- fmt.Sprintf("\n\n[stream error: %v]", err)
			}
			return
		}

		data := ev.Data
		if data == "" {
			continue
		}

		// Parse the event envelope
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			continue
		}

		eventType, _ := raw["type"].(string)
		props, _ := raw["properties"].(map[string]interface{})
		if props == nil {
			continue
		}

		// Filter by session ID — all events we care about have it at properties.sessionID
		evtSessionID, _ := props["sessionID"].(string)
		if evtSessionID != sessionID {
			continue
		}

		switch eventType {
		case "message.part.delta":
			// Streaming text chunk. Only process text field deltas.
			// Wire format: {"type":"message.part.delta","properties":{"sessionID":"...","field":"text","delta":"Hello"}}
			field, _ := props["field"].(string)
			if field != "text" {
				continue
			}
			delta, _ := props["delta"].(string)
			if delta != "" {
				ch <- delta
			}

		case "session.idle":
			// Session finished processing — stream is complete.
			return

		case "session.status":
			// Status change. Check if the session became idle or errored.
			// Wire format: {"type":"session.status","properties":{"sessionID":"...","status":{"type":"idle"}}}
			statusObj, _ := props["status"].(map[string]interface{})
			if statusObj == nil {
				continue
			}
			statusType, _ := statusObj["type"].(string)
			switch statusType {
			case "idle":
				return
			case "error":
				errMsg, _ := statusObj["error"].(string)
				if errMsg != "" {
					ch <- fmt.Sprintf("\n\n[error: %s]", errMsg)
				}
				return
			}
		}
	}
}
