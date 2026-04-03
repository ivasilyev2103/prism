package policy

import (
	"context"

	"github.com/helldriver666/prism/internal/types"
)

// RoutingDecision is the result of the router's work.
type RoutingDecision struct {
	ProviderID    types.ProviderID
	ModelOverride string // "" = use from request
	RuleName      string // name of the matched rule
	FallbackID    types.ProviderID
}

// Router determines which provider will handle the request.
type Router interface {
	// Route returns a routing decision.
	// Rules are checked top-down, the first match is applied.
	// Checks compatibility of provider.SupportedServices with req.ServiceType.
	Route(ctx context.Context, req *types.SanitizedRequest) (*RoutingDecision, error)

	// Validate checks correctness of routing rules at configuration load time.
	// Returns a list of errors: typos, unreachable rules, incompatible provider+service.
	Validate() []error
}

// BudgetChecker checks budgets before sending a request.
type BudgetChecker interface {
	// Check verifies all applicable budgets (global, project, provider, pair).
	// Returns BudgetExceededError or QuotaExceededError on exceeding.
	Check(ctx context.Context, projectID string, providerID types.ProviderID, estimate *types.CostEstimate) error
}

// Failover manages switching when a provider is unavailable.
type Failover interface {
	// Execute tries to execute fn with the primary provider.
	// On error (timeout, 5xx) — one retry, then fallback provider.
	Execute(ctx context.Context, primary, fallback types.ProviderID, fn func(types.ProviderID) error) error
}
