package cost_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/types"
)

func newTestTracker(t *testing.T) *costTestEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cost_test.db")
	tr, err := cost.NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.(interface{ Close() error }).Close() })
	return &costTestEnv{t: t, tracker: tr, dbPath: dbPath}
}

type costTestEnv struct {
	t       *testing.T
	tracker cost.Tracker
	dbPath  string
}

func (e *costTestEnv) record(id, project string, provider types.ProviderID, svc types.ServiceType, usd float64, billing types.BillingType, usage types.UsageMetrics) {
	e.t.Helper()
	err := e.tracker.Record(context.Background(), &types.RequestRecord{
		ID: id, Timestamp: time.Now().Unix(), ProjectID: project,
		ProviderID: provider, ServiceType: svc, Model: "test-model",
		Usage: usage, CostUSD: usd, BillingType: billing,
		LatencyMS: 100, Status: "ok",
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func TestTracker_RecordAndSummary(t *testing.T) {
	env := newTestTracker(t)
	env.record("r1", "proj-a", types.ProviderClaude, types.ServiceChat, 0.01, types.BillingPerToken,
		types.UsageMetrics{InputTokens: 100, OutputTokens: 50})
	env.record("r2", "proj-a", types.ProviderOpenAI, types.ServiceImage, 0.08, types.BillingPerImage,
		types.UsageMetrics{ImagesCount: 2})
	env.record("r3", "proj-b", types.ProviderClaude, types.ServiceChat, 0.02, types.BillingPerToken,
		types.UsageMetrics{InputTokens: 200, OutputTokens: 100})

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)

	s, err := env.tracker.Summary(context.Background(), "", "", from, to)
	if err != nil {
		t.Fatal(err)
	}

	if s.RequestsCount != 3 {
		t.Errorf("expected 3 requests, got %d", s.RequestsCount)
	}
	wantUSD := 0.01 + 0.08 + 0.02
	if abs(s.TotalUSD-wantUSD) > 0.001 {
		t.Errorf("expected total $%.4f, got $%.4f", wantUSD, s.TotalUSD)
	}
}

func TestSummary_ByPair_ManyToMany(t *testing.T) {
	env := newTestTracker(t)
	env.record("r1", "proj-a", types.ProviderClaude, types.ServiceChat, 0.01, types.BillingPerToken, types.UsageMetrics{})
	env.record("r2", "proj-a", types.ProviderOpenAI, types.ServiceImage, 0.05, types.BillingPerImage, types.UsageMetrics{})
	env.record("r3", "proj-b", types.ProviderClaude, types.ServiceChat, 0.02, types.BillingPerToken, types.UsageMetrics{})
	env.record("r4", "proj-b", types.ProviderOpenAI, types.ServiceChat, 0.03, types.BillingPerToken, types.UsageMetrics{})

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)

	s, err := env.tracker.Summary(context.Background(), "", "", from, to)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.ByPair) != 4 {
		t.Errorf("expected 4 pairs, got %d: %v", len(s.ByPair), s.ByPair)
	}

	// Check a specific pair.
	pair := s.ByPair["proj-a\u00d7claude"]
	if pair == nil {
		t.Fatal("pair proj-a×claude not found")
	}
	if abs(pair.USD-0.01) > 0.001 {
		t.Errorf("expected $0.01 for proj-a×claude, got $%.4f", pair.USD)
	}
}

func TestSummary_FilterByProject(t *testing.T) {
	env := newTestTracker(t)
	env.record("r1", "proj-a", types.ProviderClaude, types.ServiceChat, 0.01, types.BillingPerToken, types.UsageMetrics{})
	env.record("r2", "proj-b", types.ProviderClaude, types.ServiceChat, 0.02, types.BillingPerToken, types.UsageMetrics{})

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)

	s, err := env.tracker.Summary(context.Background(), "proj-a", "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if s.RequestsCount != 1 {
		t.Errorf("expected 1 request for proj-a, got %d", s.RequestsCount)
	}
}

func TestFlush_GracefulShutdown(t *testing.T) {
	env := newTestTracker(t)

	// Record without waiting for auto-flush.
	for i := 0; i < 10; i++ {
		env.record(fmt.Sprintf("r%d", i), "proj", types.ProviderClaude, types.ServiceChat,
			0.001, types.BillingPerToken, types.UsageMetrics{})
	}

	// Explicit flush (graceful shutdown path).
	if err := env.tracker.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Verify records are in the database.
	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)
	s, err := env.tracker.Summary(context.Background(), "", "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if s.RequestsCount != 10 {
		t.Errorf("expected 10 requests after flush, got %d", s.RequestsCount)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
