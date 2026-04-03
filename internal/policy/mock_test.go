package policy_test

import (
	"context"

	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/types"
)

// Compile-time checks.
var (
	_ policy.Router        = (*mockRouter)(nil)
	_ policy.BudgetChecker = (*mockBudgetChecker)(nil)
	_ policy.Failover      = (*mockFailover)(nil)
)

type mockRouter struct {
	routeFn    func(ctx context.Context, req *types.SanitizedRequest) (*policy.RoutingDecision, error)
	validateFn func() []error
}

func (m *mockRouter) Route(ctx context.Context, req *types.SanitizedRequest) (*policy.RoutingDecision, error) {
	if m.routeFn != nil {
		return m.routeFn(ctx, req)
	}
	return &policy.RoutingDecision{
		ProviderID: types.ProviderClaude,
		RuleName:   "default",
	}, nil
}

func (m *mockRouter) Validate() []error {
	if m.validateFn != nil {
		return m.validateFn()
	}
	return nil
}

type mockBudgetChecker struct {
	checkFn func(ctx context.Context, projectID string, providerID types.ProviderID, estimate *types.CostEstimate) error
}

func (m *mockBudgetChecker) Check(ctx context.Context, projectID string, providerID types.ProviderID, estimate *types.CostEstimate) error {
	if m.checkFn != nil {
		return m.checkFn(ctx, projectID, providerID, estimate)
	}
	return nil
}

type mockFailover struct {
	executeFn func(ctx context.Context, primary, fallback types.ProviderID, fn func(types.ProviderID) error) error
}

func (m *mockFailover) Execute(ctx context.Context, primary, fallback types.ProviderID, fn func(types.ProviderID) error) error {
	if m.executeFn != nil {
		return m.executeFn(ctx, primary, fallback, fn)
	}
	return fn(primary)
}
