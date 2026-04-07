package cost_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/helldriver666/prism/internal/cost"
	"github.com/helldriver666/prism/internal/types"
)

type budgetTestEnv struct {
	t       *testing.T
	tracker cost.Tracker
	checker *cost.BudgetChecker
	db      *sql.DB
}

func newBudgetTestEnv(t *testing.T) *budgetTestEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "budget_test.db")
	tr, err := cost.NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.(interface{ Close() error }).Close() })

	db := tr.(interface{ DB() *sql.DB }).DB()
	bc := cost.NewBudgetChecker(db)
	return &budgetTestEnv{t: t, tracker: tr, checker: bc, db: db}
}

func (e *budgetTestEnv) addBudget(level, projectID, providerID string, limitUSD float64, action string) {
	e.t.Helper()
	_, err := e.db.Exec(`INSERT INTO budgets (level, project_id, provider_id, limit_usd, period, action) VALUES (?,?,?,?,?,?)`,
		level, projectID, providerID, limitUSD, "monthly", action)
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *budgetTestEnv) spend(id, project string, provider types.ProviderID, usd float64) {
	e.t.Helper()
	e.tracker.Record(context.Background(), &types.RequestRecord{
		ID: id, Timestamp: time.Now().Unix(), ProjectID: project,
		ProviderID: provider, ServiceType: types.ServiceChat, Model: "m",
		CostUSD: usd, BillingType: types.BillingPerToken, Status: "ok",
	})
	e.tracker.Flush(context.Background())
}

func TestBudgetGuard_AllFourLevels(t *testing.T) {
	env := newBudgetTestEnv(t)

	// Set up budgets at each level.
	env.addBudget("global", "", "", 100.0, "block")
	env.addBudget("project", "proj-a", "", 50.0, "block")
	env.addBudget("provider", "", "claude", 30.0, "block")
	env.addBudget("pair", "proj-a", "claude", 10.0, "block")

	env.spend("r1", "proj-a", types.ProviderClaude, 9.0)

	estimate := &types.CostEstimate{EstimatedUSD: 2.0, BillingType: types.BillingPerToken}

	// Should exceed pair budget (9+2 > 10).
	err := env.checker.Check(context.Background(), "proj-a", types.ProviderClaude, estimate)
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	budgetErr, ok := err.(*types.BudgetExceededError)
	if !ok {
		t.Fatalf("expected BudgetExceededError, got %T: %v", err, err)
	}
	if budgetErr.Level != "pair" {
		t.Errorf("expected 'pair' level, got %q", budgetErr.Level)
	}
}

func TestBudgetGuard_MostRestrictiveWins(t *testing.T) {
	env := newBudgetTestEnv(t)

	// Global is generous, pair is tight.
	env.addBudget("global", "", "", 1000.0, "block")
	env.addBudget("pair", "proj-a", "claude", 5.0, "block")

	env.spend("r1", "proj-a", types.ProviderClaude, 4.0)

	estimate := &types.CostEstimate{EstimatedUSD: 2.0, BillingType: types.BillingPerToken}

	err := env.checker.Check(context.Background(), "proj-a", types.ProviderClaude, estimate)
	if err == nil {
		t.Fatal("expected most restrictive (pair) to block")
	}
	budgetErr := err.(*types.BudgetExceededError)
	if budgetErr.Level != "pair" {
		t.Errorf("expected pair level, got %s", budgetErr.Level)
	}
}

func TestBudgetGuard_UnderBudget_Passes(t *testing.T) {
	env := newBudgetTestEnv(t)
	env.addBudget("global", "", "", 100.0, "block")

	env.spend("r1", "proj-a", types.ProviderClaude, 10.0)

	estimate := &types.CostEstimate{EstimatedUSD: 5.0, BillingType: types.BillingPerToken}
	err := env.checker.Check(context.Background(), "proj-a", types.ProviderClaude, estimate)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestBudgetGuard_CostEstimate_PerImage(t *testing.T) {
	env := newBudgetTestEnv(t)
	env.addBudget("project", "proj-img", "", 1.0, "block")

	env.spend("r1", "proj-img", types.ProviderOpenAI, 0.80)

	// Image costs more than remaining budget.
	estimate := &types.CostEstimate{EstimatedUSD: 0.40, BillingType: types.BillingPerImage}
	err := env.checker.Check(context.Background(), "proj-img", types.ProviderOpenAI, estimate)
	if err == nil {
		t.Fatal("expected per_image budget exceeded")
	}
}

func TestBudgetGuard_SubscriptionQuotaExceeded(t *testing.T) {
	env := newBudgetTestEnv(t)

	// Register a subscription provider with request quota.
	_, err := env.db.Exec(
		`INSERT INTO providers (id, display_name, billing_type, sub_reset_day, sub_quota_requests)
		 VALUES (?, ?, ?, ?, ?)`, "claude", "Claude", "subscription", 1, 5)
	if err != nil {
		t.Fatal(err)
	}

	// Use up the quota.
	for i := 0; i < 5; i++ {
		env.spend(fmt.Sprintf("r%d", i), "proj-a", types.ProviderClaude, 0)
	}

	estimate := &types.CostEstimate{EstimatedUSD: 0, BillingType: types.BillingSubscription}
	err = env.checker.Check(context.Background(), "proj-a", types.ProviderClaude, estimate)
	if err == nil {
		t.Fatal("expected quota exceeded")
	}
	quotaErr, ok := err.(*types.QuotaExceededError)
	if !ok {
		t.Fatalf("expected QuotaExceededError, got %T: %v", err, err)
	}
	if quotaErr.QuotaType != "requests" {
		t.Errorf("expected 'requests' quota, got %q", quotaErr.QuotaType)
	}
}

func TestBudgetGuard_NoBudgets_Passes(t *testing.T) {
	env := newBudgetTestEnv(t)
	estimate := &types.CostEstimate{EstimatedUSD: 100.0, BillingType: types.BillingPerToken}
	err := env.checker.Check(context.Background(), "any", types.ProviderClaude, estimate)
	if err != nil {
		t.Errorf("no budgets should pass: %v", err)
	}
}

func fmt_Sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
