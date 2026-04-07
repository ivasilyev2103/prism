package policy_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/audit"
	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/privacy"
	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
)

// --- mock implementations for Engine tests ---

type mockPrivacy struct{}

func (m *mockPrivacy) Sanitize(_ context.Context, parts []types.TextPart, rawBody json.RawMessage, _ privacy.Profile, _ []privacy.Pattern) (*privacy.SanitizeResult, error) {
	return &privacy.SanitizeResult{
		SanitizedParts: parts,
		SanitizedBody:  rawBody,
		PrivacyScore:   0.1,
		EntitiesFound:  nil,
		RestoreFunc:    func(s string) string { return s },
	}, nil
}

type engineCostTracker struct{}

func (m *engineCostTracker) Record(context.Context, *types.RequestRecord) error { return nil }
func (m *engineCostTracker) Summary(_ context.Context, _ string, _ types.ProviderID, _, _ time.Time) (*cost.Summary, error) {
	return &cost.Summary{}, nil
}
func (m *engineCostTracker) QuotaUsage(context.Context, types.ProviderID) (*cost.QuotaUsage, error) {
	return &cost.QuotaUsage{}, nil
}
func (m *engineCostTracker) Flush(context.Context) error { return nil }

type engineAuditLog struct {
	logged []*types.RequestRecord
}

func (m *engineAuditLog) Log(_ context.Context, r *types.RequestRecord) error {
	m.logged = append(m.logged, r)
	return nil
}
func (m *engineAuditLog) Query(context.Context, *audit.Filter) ([]*types.RequestRecord, error) {
	return nil, nil
}
func (m *engineAuditLog) VerifyChain(_ context.Context, _, _ time.Time) error { return nil }

// --- Engine test ---

func TestEngine_FullFlow_WithMocks(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{
		id:       "claude",
		services: []types.ServiceType{types.ServiceChat},
	})
	reg.Register(&stubProvider{
		id:       "ollama",
		services: []types.ServiceType{types.ServiceChat, types.ServiceImage, types.ServiceEmbedding},
	})

	routesYAML := []byte(`
routes:
  - name: "default"
    then:
      provider: claude
      fallback: ollama
`)

	router, err := policy.NewRouter(routesYAML, reg)
	if err != nil {
		t.Fatal(err)
	}

	auditLog := &engineAuditLog{}

	engine := policy.NewEngine(policy.Deps{
		Privacy:     &mockPrivacy{},
		Router:      router,
		BudgetCheck: &mockBudgetChecker{},
		Providers:   reg,
		CostTracker: &engineCostTracker{},
		AuditLog:    auditLog,
		Cache:       nil, // no cache
		Failover:    policy.NewFailover(),
	})

	req := &types.ParsedRequest{
		ID:          "req-001",
		ProjectID:   "test-proj",
		ServiceType: types.ServiceChat,
		Model:       "claude-haiku",
		TextParts: []types.TextPart{
			{Role: "user", Content: "Hello, world!", Index: 0},
		},
		RawBody: json.RawMessage(`{"messages":[{"role":"user","content":"Hello, world!"}]}`),
	}

	resp, err := engine.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.TextContent != "ok" {
		t.Errorf("expected 'ok' response, got %q", resp.TextContent)
	}

	// Verify audit log was written.
	if len(auditLog.logged) != 1 {
		t.Errorf("expected 1 audit record, got %d", len(auditLog.logged))
	}
	if auditLog.logged[0].Status != "ok" {
		t.Errorf("expected status 'ok', got %q", auditLog.logged[0].Status)
	}
}

func TestEngine_BudgetBlocked(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{id: "claude", services: []types.ServiceType{types.ServiceChat}})

	router, _ := policy.NewRouter([]byte(`
routes:
  - name: "default"
    then:
      provider: claude
`), reg)

	blocked := &mockBudgetChecker{
		checkFn: func(_ context.Context, _ string, _ types.ProviderID, _ *types.CostEstimate) error {
			return &types.BudgetExceededError{Level: "global", LimitUSD: 10, CurrentUSD: 10, Action: "block"}
		},
	}

	engine := policy.NewEngine(policy.Deps{
		Privacy:     &mockPrivacy{},
		Router:      router,
		BudgetCheck: blocked,
		Providers:   reg,
		CostTracker: &engineCostTracker{},
		AuditLog:    &engineAuditLog{},
		Failover:    policy.NewFailover(),
	})

	req := &types.ParsedRequest{
		ID: "req-002", ProjectID: "p", ServiceType: types.ServiceChat,
		Model: "m", RawBody: json.RawMessage(`{}`),
	}

	_, err := engine.Process(context.Background(), req)
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	if _, ok := err.(*types.BudgetExceededError); !ok {
		t.Errorf("expected BudgetExceededError, got %T: %v", err, err)
	}
}

func TestEngine_RouterRef(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{id: "claude", services: []types.ServiceType{types.ServiceChat}})

	router, _ := policy.NewRouter([]byte(`
routes:
  - name: "default"
    then:
      provider: claude
`), reg)

	engine := policy.NewEngine(policy.Deps{Router: router})

	if engine.RouterRef() == nil {
		t.Error("expected RouterRef to return non-nil")
	}
	errs := engine.RouterRef().Validate()
	if len(errs) != 0 {
		t.Errorf("expected valid config, got: %v", errs)
	}
}
