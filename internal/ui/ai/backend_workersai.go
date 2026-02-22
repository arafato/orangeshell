package ai

import (
	"context"

	"github.com/oarafat/orangeshell/internal/config"
)

// WorkersAIBackend wraps the existing WorkersAIClient to implement the
// Backend interface with a fixed model derived from the preset.
type WorkersAIBackend struct {
	client *WorkersAIClient
	model  string
	preset config.AIModelPreset
}

// NewWorkersAIBackend creates a Backend that delegates to the deployed AI
// proxy Worker. The model is determined by the preset and baked in — callers
// do not pass a model ID per-request.
func NewWorkersAIBackend(workerURL, secret string, preset config.AIModelPreset) *WorkersAIBackend {
	return &WorkersAIBackend{
		client: &WorkersAIClient{
			WorkerURL: workerURL,
			Secret:    secret,
		},
		model:  ModelID(preset),
		preset: preset,
	}
}

// StreamResponse implements Backend. It delegates to the underlying
// WorkersAIClient, injecting the pre-configured model ID.
func (b *WorkersAIBackend) StreamResponse(ctx context.Context, messages []ChatMessage) <-chan string {
	return b.client.StreamResponse(ctx, b.model, messages)
}

// Name implements Backend.
func (b *WorkersAIBackend) Name() string {
	return "Workers AI (" + ModelDisplayName(b.preset) + ")"
}

// Close implements Backend. Workers AI is stateless HTTP — nothing to release.
func (b *WorkersAIBackend) Close() error {
	return nil
}
