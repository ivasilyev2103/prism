package ingress_test

import (
	"context"
	"net/http"

	"github.com/helldriver666/prism/internal/ingress"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time checks.
var (
	_ ingress.Handler     = (*mockHandler)(nil)
	_ ingress.RateLimiter = (*mockRateLimiter)(nil)
)

type mockHandler struct {
	handleFn func(ctx context.Context, r *http.Request) (*types.ParsedRequest, error)
}

func (m *mockHandler) Handle(ctx context.Context, r *http.Request) (*types.ParsedRequest, error) {
	if m.handleFn != nil {
		return m.handleFn(ctx, r)
	}
	return &types.ParsedRequest{}, nil
}

type mockRateLimiter struct {
	allowFn func(projectID string) bool
}

func (m *mockRateLimiter) Allow(projectID string) bool {
	if m.allowFn != nil {
		return m.allowFn(projectID)
	}
	return true
}
