package policy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/helldriver666/prism/internal/policy"
	"github.com/helldriver666/prism/internal/provider"
	"github.com/helldriver666/prism/internal/types"
)

// --- test helpers ---

type stubProvider struct {
	id       types.ProviderID
	services []types.ServiceType
}

func (p *stubProvider) ID() types.ProviderID                         { return p.id }
func (p *stubProvider) SupportedServices() []types.ServiceType       { return p.services }
func (p *stubProvider) Execute(context.Context, *types.SanitizedRequest) (*types.Response, error) {
	return &types.Response{ProviderID: p.id, TextContent: "ok"}, nil
}
func (p *stubProvider) EstimateCost(*types.ParsedRequest) (*types.CostEstimate, error) {
	return &types.CostEstimate{EstimatedUSD: 0.001, BillingType: types.BillingPerToken}, nil
}
func (p *stubProvider) HealthCheck(context.Context) error { return nil }

func testRegistry() provider.Registry {
	reg := provider.NewRegistry()
	reg.Register(&stubProvider{id: "claude", services: []types.ServiceType{types.ServiceChat}})
	reg.Register(&stubProvider{id: "openai", services: []types.ServiceType{types.ServiceChat, types.ServiceImage, types.ServiceEmbedding}})
	reg.Register(&stubProvider{id: "ollama", services: []types.ServiceType{types.ServiceChat, types.ServiceImage, types.ServiceEmbedding}})
	reg.Register(&stubProvider{id: "gemini", services: []types.ServiceType{types.ServiceChat, types.ServiceEmbedding}})
	return reg
}

func makeReq(svc types.ServiceType, project string, score float64, tags []string) *types.SanitizedRequest {
	return &types.SanitizedRequest{
		ParsedRequest: types.ParsedRequest{
			ID: "test", ProjectID: project, ServiceType: svc, Model: "m",
			RawBody: json.RawMessage(`{}`), Tags: tags,
		},
		PrivacyScore: score,
	}
}

const routesYAML = `
routes:
  - name: "sensitive"
    if:
      privacy_score: ">0.7"
    then:
      provider: ollama

  - name: "images"
    if:
      service_type: image
    then:
      provider: openai
      fallback: ollama

  - name: "embeddings"
    if:
      service_type: embedding
    then:
      provider: ollama

  - name: "code_tasks"
    if:
      tags: ["code", "sql"]
    then:
      provider: claude

  - name: "project_x"
    if:
      project_id: proj-x
    then:
      provider: gemini

  - name: "default"
    then:
      provider: claude
      fallback: ollama
`

// --- Router tests ---

func TestRouter_FirstMatchWins(t *testing.T) {
	r, err := policy.NewRouter([]byte(routesYAML), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	// High privacy score → should match "sensitive" (first rule), not "default".
	req := makeReq(types.ServiceChat, "p", 0.9, nil)
	d, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleName != "sensitive" {
		t.Errorf("expected 'sensitive', got %q", d.RuleName)
	}
	if d.ProviderID != "ollama" {
		t.Errorf("expected ollama, got %s", d.ProviderID)
	}
}

func TestRouter_DefaultRule(t *testing.T) {
	r, err := policy.NewRouter([]byte(routesYAML), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	// No special conditions → should match "default" (catch-all).
	req := makeReq(types.ServiceChat, "proj-abc", 0.1, nil)
	d, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleName != "default" {
		t.Errorf("expected 'default', got %q", d.RuleName)
	}
}

func TestRouter_ServiceTypeCondition(t *testing.T) {
	r, err := policy.NewRouter([]byte(routesYAML), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	// Image request → should match "images" rule.
	req := makeReq(types.ServiceImage, "p", 0.1, nil)
	d, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleName != "images" {
		t.Errorf("expected 'images', got %q", d.RuleName)
	}
	if d.ProviderID != "openai" {
		t.Errorf("expected openai, got %s", d.ProviderID)
	}
	if d.FallbackID != "ollama" {
		t.Errorf("expected fallback ollama, got %s", d.FallbackID)
	}
}

func TestRouter_TagsCondition(t *testing.T) {
	r, err := policy.NewRouter([]byte(routesYAML), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	req := makeReq(types.ServiceChat, "p", 0.1, []string{"code"})
	d, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleName != "code_tasks" {
		t.Errorf("expected 'code_tasks', got %q", d.RuleName)
	}
}

func TestRouter_ProjectIDCondition(t *testing.T) {
	r, err := policy.NewRouter([]byte(routesYAML), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	req := makeReq(types.ServiceChat, "proj-x", 0.1, nil)
	d, err := r.Route(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if d.RuleName != "project_x" {
		t.Errorf("expected 'project_x', got %q", d.RuleName)
	}
}

func TestRouter_ProviderCapabilityCheck(t *testing.T) {
	// Claude only supports chat. Route image to claude → error.
	yaml := `
routes:
  - name: "bad_route"
    if:
      service_type: image
    then:
      provider: claude
  - name: "default"
    then:
      provider: claude
`
	r, err := policy.NewRouter([]byte(yaml), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	req := makeReq(types.ServiceImage, "p", 0, nil)
	_, err = r.Route(context.Background(), req)
	if err == nil {
		t.Fatal("expected error: claude does not support image")
	}
}

// --- Validate tests ---

func TestRouter_Validate_UnreachableRule(t *testing.T) {
	yaml := `
routes:
  - name: "catch_all"
    then:
      provider: claude
  - name: "unreachable"
    if:
      service_type: chat
    then:
      provider: ollama
`
	r, err := policy.NewRouter([]byte(yaml), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	errs := r.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "unreachable") {
			found = true
		}
	}
	if !found {
		t.Error("expected unreachable rule warning")
	}
}

func TestRouter_Validate_IncompatibleProviderService(t *testing.T) {
	yaml := `
routes:
  - name: "bad"
    if:
      service_type: image
    then:
      provider: claude
  - name: "default"
    then:
      provider: claude
`
	r, err := policy.NewRouter([]byte(yaml), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	errs := r.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "does not support") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected incompatible provider+service error, got: %v", errs)
	}
}

func TestRouter_Validate_UnknownProvider(t *testing.T) {
	yaml := `
routes:
  - name: "typo"
    then:
      provider: cluade
`
	r, err := policy.NewRouter([]byte(yaml), testRegistry())
	if err != nil {
		t.Fatal(err)
	}

	errs := r.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "unknown provider") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown provider error, got: %v", errs)
	}
}

func TestRouter_Validate_ValidConfig(t *testing.T) {
	r, err := policy.NewRouter([]byte(routesYAML), testRegistry())
	if err != nil {
		t.Fatal(err)
	}
	errs := r.Validate()
	if len(errs) != 0 {
		t.Errorf("expected no validation errors, got: %v", errs)
	}
}

// --- Failover tests ---

func TestFailover_On5xx_SwitchesToFallback(t *testing.T) {
	f := policy.NewFailover()
	calls := 0
	err := f.Execute(context.Background(), "primary", "fallback", func(pid types.ProviderID) error {
		calls++
		if pid == "primary" {
			return fmt.Errorf("5xx error")
		}
		return nil // fallback succeeds
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	// primary (fail) + retry (fail) + fallback (success) = 3 calls
	if calls != 3 {
		t.Errorf("expected 3 calls (primary+retry+fallback), got %d", calls)
	}
}

func TestFailover_BothProvidersFail_Returns503(t *testing.T) {
	f := policy.NewFailover()
	err := f.Execute(context.Background(), "primary", "fallback", func(pid types.ProviderID) error {
		return fmt.Errorf("provider %s failed", pid)
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Errorf("expected 'all providers failed' message, got: %v", err)
	}
}

func TestFailover_NoFallback(t *testing.T) {
	f := policy.NewFailover()
	err := f.Execute(context.Background(), "primary", "", func(pid types.ProviderID) error {
		return fmt.Errorf("fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no fallback") {
		t.Errorf("expected 'no fallback' message, got: %v", err)
	}
}

func TestFailover_PrimarySucceeds(t *testing.T) {
	f := policy.NewFailover()
	calls := 0
	err := f.Execute(context.Background(), "primary", "fallback", func(pid types.ProviderID) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}
