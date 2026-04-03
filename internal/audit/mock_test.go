package audit_test

import (
	"context"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time check.
var _ audit.Logger = (*mockLogger)(nil)

type mockLogger struct {
	logFn         func(ctx context.Context, r *types.RequestRecord) error
	queryFn       func(ctx context.Context, filter *audit.Filter) ([]*types.RequestRecord, error)
	verifyChainFn func(ctx context.Context, from, to time.Time) error
}

func (m *mockLogger) Log(ctx context.Context, r *types.RequestRecord) error {
	if m.logFn != nil {
		return m.logFn(ctx, r)
	}
	return nil
}

func (m *mockLogger) Query(ctx context.Context, filter *audit.Filter) ([]*types.RequestRecord, error) {
	if m.queryFn != nil {
		return m.queryFn(ctx, filter)
	}
	return nil, nil
}

func (m *mockLogger) VerifyChain(ctx context.Context, from, to time.Time) error {
	if m.verifyChainFn != nil {
		return m.verifyChainFn(ctx, from, to)
	}
	return nil
}
