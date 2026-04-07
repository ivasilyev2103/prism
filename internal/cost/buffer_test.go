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

func TestWriteBuffer_FlushOnTimer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "buffer_timer.db")
	tr, err := cost.NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.(interface{ Close() error }).Close()

	// Record a single item (below capacity threshold).
	tr.Record(context.Background(), &types.RequestRecord{
		ID: "r1", Timestamp: time.Now().Unix(), ProjectID: "p",
		ProviderID: types.ProviderClaude, ServiceType: types.ServiceChat,
		Model: "m", BillingType: types.BillingPerToken, Status: "ok",
	})

	// Wait longer than flush interval (1s default).
	time.Sleep(1500 * time.Millisecond)

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)
	s, err := tr.Summary(context.Background(), "", "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if s.RequestsCount != 1 {
		t.Errorf("expected 1 request after timer flush, got %d", s.RequestsCount)
	}
}

func TestWriteBuffer_FlushOnCapacity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "buffer_cap.db")
	tr, err := cost.NewTracker(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.(interface{ Close() error }).Close()

	// Record 100 items (default capacity).
	for i := 0; i < 100; i++ {
		tr.Record(context.Background(), &types.RequestRecord{
			ID: fmt.Sprintf("r%d", i), Timestamp: time.Now().Unix(), ProjectID: "p",
			ProviderID: types.ProviderClaude, ServiceType: types.ServiceChat,
			Model: "m", BillingType: types.BillingPerToken, Status: "ok",
		})
	}

	// Capacity-triggered flush should have happened.
	// Give a tiny bit of time for the async flush.
	time.Sleep(100 * time.Millisecond)

	// Force flush to ensure we see everything.
	tr.Flush(context.Background())

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)
	s, err := tr.Summary(context.Background(), "", "", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if s.RequestsCount != 100 {
		t.Errorf("expected 100 requests after capacity flush, got %d", s.RequestsCount)
	}
}
