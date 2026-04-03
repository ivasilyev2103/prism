package cost_test

import (
	"context"
	"time"

	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time check.
var _ cost.Tracker = (*mockTracker)(nil)

type mockTracker struct {
	recordFn     func(ctx context.Context, r *types.RequestRecord) error
	summaryFn    func(ctx context.Context, projectID string, providerID types.ProviderID, from, to time.Time) (*cost.Summary, error)
	quotaUsageFn func(ctx context.Context, providerID types.ProviderID) (*cost.QuotaUsage, error)
	flushFn      func(ctx context.Context) error
}

func (m *mockTracker) Record(ctx context.Context, r *types.RequestRecord) error {
	if m.recordFn != nil {
		return m.recordFn(ctx, r)
	}
	return nil
}

func (m *mockTracker) Summary(ctx context.Context, projectID string, providerID types.ProviderID, from, to time.Time) (*cost.Summary, error) {
	if m.summaryFn != nil {
		return m.summaryFn(ctx, projectID, providerID, from, to)
	}
	return &cost.Summary{}, nil
}

func (m *mockTracker) QuotaUsage(ctx context.Context, providerID types.ProviderID) (*cost.QuotaUsage, error) {
	if m.quotaUsageFn != nil {
		return m.quotaUsageFn(ctx, providerID)
	}
	return &cost.QuotaUsage{}, nil
}

func (m *mockTracker) Flush(ctx context.Context) error {
	if m.flushFn != nil {
		return m.flushFn(ctx)
	}
	return nil
}
