package ai

import "context"

// Backend is the provider-agnostic interface for AI chat backends.
// Implementations include Workers AI (via deployed proxy), HTTP endpoints
// (OpenCode serve, Ollama, LM Studio), and future local agent processes.
//
// All backends produce a streaming channel of text chunks using the same
// protocol as the existing Workers AI client — the app-layer bridge
// (aiStreamBatchMsg / aiStreamContinueMsg / AIChatStreamDoneMsg) works
// unchanged regardless of which backend is active.
type Backend interface {
	// StreamResponse sends a chat completion request and returns a channel
	// that yields text chunks as they arrive. The channel is closed when the
	// stream ends. Errors are sent as a final message prefixed with "error:".
	StreamResponse(ctx context.Context, messages []ChatMessage) <-chan string

	// Name returns a human-readable name for this backend (e.g. "Workers AI",
	// "HTTP Endpoint", "Local Agent").
	Name() string

	// Close releases any resources held by the backend (open connections,
	// child processes, etc.). It is safe to call Close multiple times.
	Close() error
}
