package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/oarafat/orangeshell/internal/config"
	"github.com/tmaxmax/go-sse"
)

// --- Workers AI Model IDs ---

// ModelID returns the Workers AI model identifier for a given preset.
func ModelID(preset config.AIModelPreset) string {
	switch preset {
	case config.AIModelFast:
		return "@cf/meta/llama-3.1-8b-instruct-fast"
	case config.AIModelBalanced:
		return "@cf/meta/llama-3.3-70b-instruct-fp8-fast"
	case config.AIModelDeep:
		return "@cf/deepseek-ai/deepseek-r1-distill-qwen-32b"
	default:
		return "@cf/meta/llama-3.3-70b-instruct-fp8-fast"
	}
}

// ModelDisplayName returns a human-readable name for the model preset.
func ModelDisplayName(preset config.AIModelPreset) string {
	switch preset {
	case config.AIModelFast:
		return "Fast (Llama 3.1 8B)"
	case config.AIModelBalanced:
		return "Balanced (Llama 3.3 70B)"
	case config.AIModelDeep:
		return "Deep (DeepSeek R1 32B)"
	default:
		return "Balanced (Llama 3.3 70B)"
	}
}

// --- Chat Message Types ---

// ChatRole is the role of a chat message sender.
type ChatRole string

const (
	RoleSystem    ChatRole = "system"
	RoleUser      ChatRole = "user"
	RoleAssistant ChatRole = "assistant"
)

// ChatMessage is a single message in a conversation.
type ChatMessage struct {
	Role    ChatRole `json:"role"`
	Content string   `json:"content"`
}

// --- SSE Stream Chunk ---

// workersAIChunk is the JSON structure inside each SSE data field from Workers AI streaming.
type workersAIChunk struct {
	Response string `json:"response"`
}

// --- Client ---

// WorkersAIClient communicates with the deployed AI proxy Worker.
type WorkersAIClient struct {
	WorkerURL string
	Secret    string
}

// requestBody is the JSON body sent to the AI proxy Worker.
type requestBody struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// StreamResponse sends a chat completion request and returns a channel that yields
// text chunks as they arrive via SSE. The channel is closed when the stream ends.
// Errors are sent as a final message prefixed with "error:".
func (c *WorkersAIClient) StreamResponse(ctx context.Context, model string, messages []ChatMessage) <-chan string {
	ch := make(chan string, 64)

	go func() {
		defer close(ch)

		body := requestBody{
			Model:    model,
			Messages: messages,
			Stream:   true,
		}

		jsonBody, err := json.Marshal(body)
		if err != nil {
			ch <- fmt.Sprintf("error: failed to marshal request: %v", err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WorkerURL, bytes.NewReader(jsonBody))
		if err != nil {
			ch <- fmt.Sprintf("error: failed to create request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.Secret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- fmt.Sprintf("error: request failed: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			ch <- fmt.Sprintf("error: AI Worker returned %d: %s", resp.StatusCode, string(bodyBytes))
			return
		}

		// Use go-sse to parse the SSE stream
		for ev, err := range sse.Read(resp.Body, nil) {
			if err != nil {
				// Stream ended or error
				if ctx.Err() != nil {
					return // context cancelled, don't send error
				}
				// io.EOF is normal end of stream
				return
			}

			data := ev.Data
			if data == "" {
				continue
			}

			// Workers AI sends "[DONE]" as the final message
			if strings.TrimSpace(data) == "[DONE]" {
				return
			}

			// Parse the JSON chunk
			var chunk workersAIChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Some models send raw text; try using it directly
				ch <- data
				continue
			}

			if chunk.Response != "" {
				ch <- chunk.Response
			}
		}
	}()

	return ch
}

// NonStreamResponse sends a chat completion request and returns the full response.
func (c *WorkersAIClient) NonStreamResponse(ctx context.Context, model string, messages []ChatMessage) (string, error) {
	body := requestBody{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WorkerURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Secret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("AI Worker returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Response, nil
}
