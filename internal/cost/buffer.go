package cost

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/helldriver666/prism/internal/types"
)

const (
	defaultFlushInterval = 1 * time.Second
	defaultFlushCapacity = 100
	maxRetries           = 3
)

// writeBuffer is an in-memory ring buffer that batches RequestRecords
// and flushes them to SQLite periodically or when capacity is reached.
type writeBuffer struct {
	mu       sync.Mutex
	records  []*types.RequestRecord
	db       *sql.DB
	done     chan struct{}
	wg       sync.WaitGroup
	interval time.Duration
	capacity int
}

func newWriteBuffer(db *sql.DB, interval time.Duration, capacity int) *writeBuffer {
	if interval <= 0 {
		interval = defaultFlushInterval
	}
	if capacity <= 0 {
		capacity = defaultFlushCapacity
	}
	wb := &writeBuffer{
		db:       db,
		done:     make(chan struct{}),
		interval: interval,
		capacity: capacity,
	}
	wb.wg.Add(1)
	go wb.flushLoop()
	return wb
}

// add appends a record to the buffer. If the buffer is full, triggers a flush.
func (wb *writeBuffer) add(r *types.RequestRecord) {
	wb.mu.Lock()
	wb.records = append(wb.records, r)
	shouldFlush := len(wb.records) >= wb.capacity
	wb.mu.Unlock()

	if shouldFlush {
		wb.flushNow()
	}
}

// flushLoop runs in a background goroutine and flushes on a timer.
func (wb *writeBuffer) flushLoop() {
	defer wb.wg.Done()
	ticker := time.NewTicker(wb.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wb.flushNow()
		case <-wb.done:
			wb.flushNow() // Final flush on shutdown.
			return
		}
	}
}

// flushNow drains the buffer and writes all records to SQLite.
func (wb *writeBuffer) flushNow() {
	wb.mu.Lock()
	if len(wb.records) == 0 {
		wb.mu.Unlock()
		return
	}
	batch := wb.records
	wb.records = nil
	wb.mu.Unlock()

	if err := wb.writeBatch(batch); err != nil {
		log.Printf("cost: flush error (lost %d records): %v", len(batch), err)
	}
}

// writeBatch inserts a batch of records into SQLite with retries.
func (wb *writeBuffer) writeBatch(batch []*types.RequestRecord) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := wb.insertBatch(batch); err != nil {
			lastErr = err
			time.Sleep(time.Duration(1<<attempt) * 50 * time.Millisecond) // exponential backoff
			continue
		}
		return nil
	}
	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// insertBatch performs the actual INSERT in a single transaction.
func (wb *writeBuffer) insertBatch(batch []*types.RequestRecord) error {
	tx, err := wb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO requests
		(id, ts, project_id, provider_id, service_type, model,
		 input_tokens, output_tokens, images_count, audio_seconds, compute_units,
		 cost_usd, billing_type, latency_ms, privacy_score, cache_hit, route_matched, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range batch {
		_, err := stmt.Exec(
			r.ID, r.Timestamp, r.ProjectID, string(r.ProviderID), string(r.ServiceType), r.Model,
			r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.ImagesCount, r.Usage.AudioSeconds, r.Usage.ComputeUnits,
			r.CostUSD, string(r.BillingType), r.LatencyMS, r.PrivacyScore, r.CacheHit, r.RouteMatched, r.Status,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// flush forces an immediate flush and blocks until complete.
func (wb *writeBuffer) flush(ctx context.Context) error {
	wb.mu.Lock()
	batch := wb.records
	wb.records = nil
	wb.mu.Unlock()

	if len(batch) == 0 {
		return nil
	}
	return wb.writeBatch(batch)
}

// stop signals the flush loop to exit and waits for completion.
func (wb *writeBuffer) stop() {
	close(wb.done)
	wb.wg.Wait()
}
