package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/cache"
	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
)

// Deps holds all dependencies injected into the Engine.
// All fields are interfaces — no concrete type imports.
type Deps struct {
	Privacy     privacy.Pipeline
	Router      Router
	BudgetCheck BudgetChecker
	Providers   provider.Registry
	CostTracker cost.Tracker
	AuditLog    audit.Logger
	Cache       cache.SemanticCache // may be nil (Phase 8)
	Failover    Failover
}

// Engine orchestrates the full request processing flow.
type Engine struct {
	deps Deps
}

// NewEngine creates an Engine with the given dependencies.
func NewEngine(deps Deps) *Engine {
	return &Engine{deps: deps}
}

// RouterRef returns the router for validation at startup.
func (e *Engine) RouterRef() Router {
	return e.deps.Router
}

// Process handles a parsed request through the full pipeline:
// privacy → route → budget → cache → execute → restore → cost → audit.
func (e *Engine) Process(ctx context.Context, req *types.ParsedRequest) (*types.Response, error) {
	start := time.Now()

	// 1. Privacy Pipeline: sanitize text parts.
	sanitizeResult, err := e.deps.Privacy.Sanitize(ctx, req.TextParts, req.RawBody, privacy.ProfileModerate, nil)
	if err != nil {
		// Determine fail-closed vs pass-through based on eventual provider.
		// At this point we don't know the provider yet, so we fail-closed for safety.
		return nil, fmt.Errorf("privacy: %w", err)
	}

	sanitizedReq := &types.SanitizedRequest{
		ParsedRequest:    *req,
		SanitizedBody:    sanitizeResult.SanitizedBody,
		PrivacyScore:     sanitizeResult.PrivacyScore,
		PIIEntitiesFound: len(sanitizeResult.EntitiesFound),
	}

	// 2. Route: choose provider.
	decision, err := e.deps.Router.Route(ctx, sanitizedReq)
	if err != nil {
		return nil, fmt.Errorf("routing: %w", err)
	}

	// Apply model override from routing rule.
	if decision.ModelOverride != "" {
		sanitizedReq.Model = decision.ModelOverride
	}

	// 3. Budget: estimate cost and check budgets.
	selectedProvider, err := e.deps.Providers.Get(decision.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", decision.ProviderID, err)
	}

	estimate, err := selectedProvider.EstimateCost(&sanitizedReq.ParsedRequest)
	if err != nil {
		return nil, fmt.Errorf("cost estimate: %w", err)
	}

	if err := e.deps.BudgetCheck.Check(ctx, req.ProjectID, decision.ProviderID, estimate); err != nil {
		e.recordResult(ctx, req, decision, nil, start, "budget_blocked")
		return nil, err
	}

	// 4. Cache: check for a cached response.
	if e.deps.Cache != nil {
		cached, err := e.deps.Cache.Get(ctx, sanitizedReq)
		if err == nil && cached != nil {
			// Cache hit — restore PII and return.
			if cached.TextContent != "" {
				cached.TextContent = sanitizeResult.RestoreFunc(cached.TextContent)
			}
			e.recordResult(ctx, req, decision, cached, start, "cache_hit")
			return cached, nil
		}
	}

	// 5. Execute: send request to provider (with failover).
	var resp *types.Response
	err = e.deps.Failover.Execute(ctx, decision.ProviderID, decision.FallbackID, func(pid types.ProviderID) error {
		p, err := e.deps.Providers.Get(pid)
		if err != nil {
			return err
		}
		resp, err = p.Execute(ctx, sanitizedReq)
		return err
	})
	if err != nil {
		e.recordResult(ctx, req, decision, nil, start, "error")
		return nil, fmt.Errorf("execute: %w", err)
	}

	// 6. Restore PII in text responses.
	if resp.TextContent != "" {
		resp.TextContent = sanitizeResult.RestoreFunc(resp.TextContent)
	}

	// 7. Cache: store response.
	if e.deps.Cache != nil {
		_ = e.deps.Cache.Set(ctx, sanitizedReq, resp)
	}

	// 8. Record cost + audit.
	e.recordResult(ctx, req, decision, resp, start, "ok")

	return resp, nil
}

// recordResult writes to both Cost Tracker and Audit Log.
func (e *Engine) recordResult(
	ctx context.Context,
	req *types.ParsedRequest,
	decision *RoutingDecision,
	resp *types.Response,
	start time.Time,
	status string,
) {
	record := &types.RequestRecord{
		ID:          req.ID,
		Timestamp:   time.Now().Unix(),
		ProjectID:   req.ProjectID,
		ProviderID:  decision.ProviderID,
		ServiceType: req.ServiceType,
		Model:       req.Model,
		LatencyMS:   time.Since(start).Milliseconds(),
		RouteMatched: decision.RuleName,
		Status:      status,
	}

	if resp != nil {
		record.Usage = resp.Usage
		record.Model = resp.Model
		record.ProviderID = resp.ProviderID
	}

	// Best-effort: don't fail the request if recording fails.
	_ = e.deps.CostTracker.Record(ctx, record)
	_ = e.deps.AuditLog.Log(ctx, record)
}

// ProcessRaw is a convenience method that builds a ParsedRequest from raw JSON.
// Used by the ingress handler.
func (e *Engine) ProcessRaw(ctx context.Context, id, projectID string, serviceType types.ServiceType, parts []types.TextPart, rawBody json.RawMessage) (*types.Response, error) {
	req := &types.ParsedRequest{
		ID:          id,
		ProjectID:   projectID,
		ServiceType: serviceType,
		TextParts:   parts,
		RawBody:     rawBody,
	}
	return e.Process(ctx, req)
}
