package audit

import (
	"context"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

// Logger records request metadata in an append-only log.
// IMPORTANT: request and response bodies are never logged.
// Each record contains an HMAC hash of the chain for tamper detection.
type Logger interface {
	// Log writes a record. The operation is append-only, modification is impossible.
	// Automatically computes HMAC: hash(content || prev_hmac).
	Log(ctx context.Context, r *types.RequestRecord) error

	// Query returns records with filtering (without request bodies).
	Query(ctx context.Context, filter *Filter) ([]*types.RequestRecord, error)

	// VerifyChain verifies HMAC chain integrity for a given period.
	// Returns nil if the chain is intact.
	VerifyChain(ctx context.Context, from, to time.Time) error
}

// Filter contains query parameters for the audit log.
type Filter struct {
	ProjectID   string
	ProviderID  types.ProviderID
	ServiceType types.ServiceType
	From        time.Time
	To          time.Time
	Status      string
	Limit       int
	Offset      int
}
