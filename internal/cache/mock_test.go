package cache_test

import (
	"context"

	"github.com/helldriver666/prism/internal/cache"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time checks.
var (
	_ cache.SemanticCache = (*mockSemanticCache)(nil)
	_ cache.Embedder      = (*mockEmbedder)(nil)
)

type mockSemanticCache struct {
	getFn        func(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error)
	setFn        func(ctx context.Context, req *types.SanitizedRequest, resp *types.Response) error
	invalidateFn func(ctx context.Context, projectID string) error
}

func (m *mockSemanticCache) Get(ctx context.Context, req *types.SanitizedRequest) (*types.Response, error) {
	if m.getFn != nil {
		return m.getFn(ctx, req)
	}
	return nil, nil
}

func (m *mockSemanticCache) Set(ctx context.Context, req *types.SanitizedRequest, resp *types.Response) error {
	if m.setFn != nil {
		return m.setFn(ctx, req, resp)
	}
	return nil
}

func (m *mockSemanticCache) Invalidate(ctx context.Context, projectID string) error {
	if m.invalidateFn != nil {
		return m.invalidateFn(ctx, projectID)
	}
	return nil
}

type mockEmbedder struct {
	embedFn       func(ctx context.Context, text string) ([]float32, error)
	healthCheckFn func(ctx context.Context) error
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, text)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockEmbedder) HealthCheck(ctx context.Context) error {
	if m.healthCheckFn != nil {
		return m.healthCheckFn(ctx)
	}
	return nil
}
