package cost

import (
	"context"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// Tracker records expenses and quota consumption.
// Uses an in-memory write buffer with periodic flush to SQLite
// to reduce write contention under concurrent load.
type Tracker interface {
	// Record saves metrics of a completed request (async-safe).
	// Written to an in-memory buffer, flushed to SQLite in batches.
	Record(ctx context.Context, r *types.RequestRecord) error

	// Summary returns aggregated data for a period.
	// projectID and providerID are optional (empty string = all).
	Summary(ctx context.Context, projectID string, providerID types.ProviderID, from, to time.Time) (*Summary, error)

	// QuotaUsage returns current subscription quota consumption.
	QuotaUsage(ctx context.Context, providerID types.ProviderID) (*QuotaUsage, error)

	// Flush forcefully writes the buffer to SQLite (called on graceful shutdown).
	Flush(ctx context.Context) error
}

// Summary is aggregated statistics.
type Summary struct {
	TotalUSD        float64
	RequestsCount   int64
	CacheSavingsUSD float64
	ByProvider      map[types.ProviderID]*ProviderSummary
	ByProject       map[string]*ProjectSummary
	ByPair          map[string]*PairSummary // key: "projectID×providerID"
}

// ProviderSummary is per-provider statistics.
type ProviderSummary struct {
	USD      float64
	Requests int64
	Usage    types.UsageMetrics // aggregated metrics
}

// ProjectSummary is per-project statistics.
type ProjectSummary struct {
	USD      float64
	Requests int64
}

// PairSummary is per-pair (project x provider) statistics.
type PairSummary struct {
	USD      float64
	Requests int64
}

// QuotaUsage is subscription provider quota consumption.
type QuotaUsage struct {
	ProviderID        types.ProviderID
	PeriodStart       time.Time
	PeriodEnd         time.Time
	RequestsUsed      int64
	RequestsLimit     *int64 // nil = unlimited
	InputTokensUsed   int64
	InputTokensLimit  *int64
	OutputTokensUsed  int64
	OutputTokensLimit *int64
	ImagesUsed        int64
	ImagesLimit       *int64
}
