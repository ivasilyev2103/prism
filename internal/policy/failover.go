package policy

import (
	"context"
	"fmt"

	"github.com/helldriver666/prism/internal/types"
)

type failover struct{}

// NewFailover creates a Failover that retries once on the primary provider,
// then falls back to the fallback provider.
func NewFailover() Failover {
	return &failover{}
}

func (f *failover) Execute(_ context.Context, primary, fallback types.ProviderID, fn func(types.ProviderID) error) error {
	// Try primary.
	err := fn(primary)
	if err == nil {
		return nil
	}
	firstErr := err

	// One retry on primary.
	err = fn(primary)
	if err == nil {
		return nil
	}

	// Try fallback (if configured).
	if fallback == "" {
		return fmt.Errorf("provider %s failed (no fallback): %w", primary, firstErr)
	}

	err = fn(fallback)
	if err == nil {
		return nil
	}

	return fmt.Errorf("all providers failed: primary %s, fallback %s: %w", primary, fallback, err)
}
