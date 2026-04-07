package cost

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// BudgetChecker checks all applicable budgets before a request is sent.
// It implements a four-level hierarchy: global → project → provider → pair.
// The most restrictive budget wins.
type BudgetChecker struct {
	db *sql.DB
}

// NewBudgetChecker creates a BudgetChecker using the cost database.
func NewBudgetChecker(db *sql.DB) *BudgetChecker {
	return &BudgetChecker{db: db}
}

// budgetRow represents a single budget entry.
type budgetRow struct {
	level     string
	projectID string
	providerID string
	limitUSD  float64
	period    string
	action    string
}

// Check verifies all applicable budgets for the given project/provider.
// Returns BudgetExceededError if any budget is exceeded, or QuotaExceededError
// for subscription quota violations.
func (bc *BudgetChecker) Check(ctx context.Context, projectID string, providerID types.ProviderID, estimate *types.CostEstimate) error {
	// Check subscription quotas first.
	if estimate.BillingType == types.BillingSubscription {
		if err := bc.checkQuota(ctx, providerID); err != nil {
			return err
		}
	}

	// Load all applicable budgets.
	budgets, err := bc.loadBudgets(ctx, projectID, providerID)
	if err != nil {
		return fmt.Errorf("budget: load: %w", err)
	}

	now := time.Now()
	for _, b := range budgets {
		current, err := bc.currentSpend(ctx, b, now)
		if err != nil {
			return fmt.Errorf("budget: query spend: %w", err)
		}

		if current+estimate.EstimatedUSD > b.limitUSD {
			return &types.BudgetExceededError{
				Level:      b.level,
				ProjectID:  b.projectID,
				ProviderID: types.ProviderID(b.providerID),
				LimitUSD:   b.limitUSD,
				CurrentUSD: current,
				Action:     b.action,
			}
		}
	}

	return nil
}

// loadBudgets returns all applicable budgets ordered from most to least specific.
func (bc *BudgetChecker) loadBudgets(ctx context.Context, projectID string, providerID types.ProviderID) ([]budgetRow, error) {
	rows, err := bc.db.QueryContext(ctx,
		`SELECT level, COALESCE(project_id,''), COALESCE(provider_id,''), limit_usd, period, action
		 FROM budgets
		 WHERE (level = 'global')
		    OR (level = 'project' AND project_id = ?)
		    OR (level = 'provider' AND provider_id = ?)
		    OR (level = 'pair' AND project_id = ? AND provider_id = ?)
		 ORDER BY CASE level
		    WHEN 'pair' THEN 1
		    WHEN 'project' THEN 2
		    WHEN 'provider' THEN 3
		    WHEN 'global' THEN 4
		 END`,
		projectID, string(providerID), projectID, string(providerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var budgets []budgetRow
	for rows.Next() {
		var b budgetRow
		if err := rows.Scan(&b.level, &b.projectID, &b.providerID, &b.limitUSD, &b.period, &b.action); err != nil {
			return nil, err
		}
		budgets = append(budgets, b)
	}
	return budgets, rows.Err()
}

// currentSpend calculates how much has been spent for the given budget's scope and period.
func (bc *BudgetChecker) currentSpend(ctx context.Context, b budgetRow, now time.Time) (float64, error) {
	from := periodStart(b.period, now)

	query := `SELECT COALESCE(SUM(cost_usd), 0) FROM requests WHERE ts >= ?`
	args := []any{from.Unix()}

	switch b.level {
	case "pair":
		query += " AND project_id = ? AND provider_id = ?"
		args = append(args, b.projectID, b.providerID)
	case "project":
		query += " AND project_id = ?"
		args = append(args, b.projectID)
	case "provider":
		query += " AND provider_id = ?"
		args = append(args, b.providerID)
	case "global":
		// No additional filter.
	}

	var current float64
	if err := bc.db.QueryRowContext(ctx, query, args...).Scan(&current); err != nil {
		return 0, err
	}
	return current, nil
}

// checkQuota verifies subscription quotas for the provider.
func (bc *BudgetChecker) checkQuota(ctx context.Context, providerID types.ProviderID) error {
	var resetDay int
	var quotaReqs, quotaInTok, quotaOutTok, quotaImgs sql.NullInt64

	err := bc.db.QueryRowContext(ctx,
		`SELECT COALESCE(sub_reset_day,1), sub_quota_requests, sub_quota_input_tokens, sub_quota_output_tokens, sub_quota_images
		 FROM providers WHERE id = ?`, string(providerID)).
		Scan(&resetDay, &quotaReqs, &quotaInTok, &quotaOutTok, &quotaImgs)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // No subscription config — skip quota check.
		}
		return err
	}

	periodStart, periodEnd := subscriptionPeriod(time.Now(), resetDay)

	var reqUsed, inTokUsed, outTokUsed, imgsUsed int64
	err = bc.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(images_count),0)
		 FROM requests WHERE provider_id = ? AND ts >= ? AND ts <= ?`,
		string(providerID), periodStart.Unix(), periodEnd.Unix()).
		Scan(&reqUsed, &inTokUsed, &outTokUsed, &imgsUsed)
	if err != nil {
		return err
	}

	if quotaReqs.Valid && reqUsed >= quotaReqs.Int64 {
		return &types.QuotaExceededError{ProviderID: providerID, QuotaType: "requests", Used: reqUsed, Limit: quotaReqs.Int64}
	}
	if quotaInTok.Valid && inTokUsed >= quotaInTok.Int64 {
		return &types.QuotaExceededError{ProviderID: providerID, QuotaType: "input_tokens", Used: inTokUsed, Limit: quotaInTok.Int64}
	}
	if quotaOutTok.Valid && outTokUsed >= quotaOutTok.Int64 {
		return &types.QuotaExceededError{ProviderID: providerID, QuotaType: "output_tokens", Used: outTokUsed, Limit: quotaOutTok.Int64}
	}
	if quotaImgs.Valid && imgsUsed >= quotaImgs.Int64 {
		return &types.QuotaExceededError{ProviderID: providerID, QuotaType: "images", Used: imgsUsed, Limit: quotaImgs.Int64}
	}

	return nil
}

// periodStart returns the beginning of the current budget period.
func periodStart(period string, now time.Time) time.Time {
	switch period {
	case "daily":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	case "weekly":
		y, m, d := now.Date()
		weekday := int(now.Weekday())
		return time.Date(y, m, d-weekday, 0, 0, 0, 0, now.Location())
	case "monthly":
		y, m, _ := now.Date()
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
	case "yearly":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	default:
		// Default to monthly.
		y, m, _ := now.Date()
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
	}
}
