package cache

import (
	"context"

	"github.com/helldriver666/prism/internal/types"
)

// CachePolicy defines whether a given service type is cached.
// For chat/embedding — yes (deterministic). For image gen — no by default (stochastic).
type CachePolicy struct {
	ServiceType types.ServiceType
	Enabled     bool
	TTL         int // seconds; 0 = default
}

// SemanticCache caches responses to semantically similar requests.
// Stores sanitized responses + encrypted PII mappings for restoration.
// PII in the cache is encrypted per-entry (AES-256-GCM), not in plaintext.
type SemanticCache interface {
	// Get looks for a semantically similar request in the cache.
	// Returns (nil, nil) on miss or if caching is disabled for this ServiceType.
	Get(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)

	// Set saves a request and response in the cache.
	// For ServiceType with disabled caching — no-op.
	Set(ctx context.Context, req *types.SanitizedRequest, resp *types.Response) error

	// Invalidate removes entries by projectID (e.g., on privacy settings change).
	Invalidate(ctx context.Context, projectID string) error
}

// Embedder generates vector representations of text.
type Embedder interface {
	// Embed returns an embedding for the text.
	// Implementation: nomic-embed-text via Ollama (locally).
	Embed(ctx context.Context, text string) ([]float32, error)

	// HealthCheck verifies Ollama availability.
	HealthCheck(ctx context.Context) error
}
