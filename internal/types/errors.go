package types

import "fmt"

// BudgetExceededError is returned by Budget Guard when a limit is exceeded.
type BudgetExceededError struct {
	Level      string     // "global" | "project" | "provider" | "pair"
	ProjectID  string
	ProviderID ProviderID
	LimitUSD   float64
	CurrentUSD float64
	Action     string // "block" | "downgrade_model" | "alert"
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("budget exceeded at %s level: project=%s provider=%s limit=$%.4f current=$%.4f action=%s",
		e.Level, e.ProjectID, e.ProviderID, e.LimitUSD, e.CurrentUSD, e.Action)
}

// QuotaExceededError is returned when a subscription quota is exhausted.
type QuotaExceededError struct {
	ProviderID ProviderID
	QuotaType  string // "requests" | "input_tokens" | "output_tokens" | "images" | "compute_seconds"
	Used       int64
	Limit      int64
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("quota exceeded for provider=%s type=%s used=%d limit=%d",
		e.ProviderID, e.QuotaType, e.Used, e.Limit)
}
