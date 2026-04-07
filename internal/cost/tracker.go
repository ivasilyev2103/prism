package cost

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/helldriver666/prism/internal/types"
)

type tracker struct {
	db  *sql.DB
	buf *writeBuffer
}

// NewTracker creates a Cost Tracker backed by the given SQLite database path.
// The tracker starts a background flush goroutine. Call Close() on shutdown.
func NewTracker(dbPath string) (Tracker, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("cost: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer.

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &tracker{
		db:  db,
		buf: newWriteBuffer(db, defaultFlushInterval, defaultFlushCapacity),
	}, nil
}

func (t *tracker) Record(_ context.Context, r *types.RequestRecord) error {
	if r.Timestamp == 0 {
		r.Timestamp = time.Now().Unix()
	}
	t.buf.add(r)
	return nil
}

func (t *tracker) Summary(ctx context.Context, projectID string, providerID types.ProviderID, from, to time.Time) (*Summary, error) {
	// Flush pending records so the query sees everything.
	if err := t.buf.flush(ctx); err != nil {
		return nil, fmt.Errorf("cost: pre-summary flush: %w", err)
	}

	query := `SELECT project_id, provider_id, COUNT(*), COALESCE(SUM(cost_usd),0),
	          COALESCE(SUM(CASE WHEN cache_hit THEN cost_usd ELSE 0 END),0),
	          COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
	          COALESCE(SUM(images_count),0), COALESCE(SUM(audio_seconds),0),
	          COALESCE(SUM(compute_units),0)
	          FROM requests WHERE ts >= ? AND ts <= ?`
	args := []any{from.Unix(), to.Unix()}

	if projectID != "" {
		query += " AND project_id = ?"
		args = append(args, projectID)
	}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, string(providerID))
	}
	query += " GROUP BY project_id, provider_id"

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cost: summary query: %w", err)
	}
	defer rows.Close()

	s := &Summary{
		ByProvider: make(map[types.ProviderID]*ProviderSummary),
		ByProject:  make(map[string]*ProjectSummary),
		ByPair:     make(map[string]*PairSummary),
	}

	for rows.Next() {
		var (
			pid, prvID                         string
			cnt                                int64
			usd, cacheSave                     float64
			inTok, outTok, imgCnt              int64
			audioSec, computeUnits             float64
		)
		if err := rows.Scan(&pid, &prvID, &cnt, &usd, &cacheSave,
			&inTok, &outTok, &imgCnt, &audioSec, &computeUnits); err != nil {
			return nil, fmt.Errorf("cost: scan row: %w", err)
		}

		s.TotalUSD += usd
		s.RequestsCount += cnt
		s.CacheSavingsUSD += cacheSave

		providerKey := types.ProviderID(prvID)
		if _, ok := s.ByProvider[providerKey]; !ok {
			s.ByProvider[providerKey] = &ProviderSummary{}
		}
		ps := s.ByProvider[providerKey]
		ps.USD += usd
		ps.Requests += cnt
		ps.Usage.InputTokens += int(inTok)
		ps.Usage.OutputTokens += int(outTok)
		ps.Usage.ImagesCount += int(imgCnt)
		ps.Usage.AudioSeconds += audioSec
		ps.Usage.ComputeUnits += computeUnits

		if _, ok := s.ByProject[pid]; !ok {
			s.ByProject[pid] = &ProjectSummary{}
		}
		pj := s.ByProject[pid]
		pj.USD += usd
		pj.Requests += cnt

		pairKey := pid + "\u00d7" + prvID // "×" separator
		s.ByPair[pairKey] = &PairSummary{USD: usd, Requests: cnt}
	}

	return s, rows.Err()
}

func (t *tracker) QuotaUsage(ctx context.Context, providerID types.ProviderID) (*QuotaUsage, error) {
	// Flush pending records.
	if err := t.buf.flush(ctx); err != nil {
		return nil, fmt.Errorf("cost: pre-quota flush: %w", err)
	}

	// Load provider subscription info.
	var (
		resetDay                                       int
		quotaReqs, quotaInTok, quotaOutTok, quotaImgs  sql.NullInt64
	)
	err := t.db.QueryRowContext(ctx,
		`SELECT COALESCE(sub_reset_day,1), sub_quota_requests, sub_quota_input_tokens, sub_quota_output_tokens, sub_quota_images
		 FROM providers WHERE id = ?`, string(providerID)).
		Scan(&resetDay, &quotaReqs, &quotaInTok, &quotaOutTok, &quotaImgs)
	if err != nil {
		return nil, fmt.Errorf("cost: provider %s not found: %w", providerID, err)
	}

	periodStart, periodEnd := subscriptionPeriod(time.Now(), resetDay)

	var reqUsed, inTokUsed, outTokUsed, imgsUsed int64
	err = t.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(images_count),0)
		 FROM requests WHERE provider_id = ? AND ts >= ? AND ts <= ?`,
		string(providerID), periodStart.Unix(), periodEnd.Unix()).
		Scan(&reqUsed, &inTokUsed, &outTokUsed, &imgsUsed)
	if err != nil {
		return nil, fmt.Errorf("cost: quota query: %w", err)
	}

	qu := &QuotaUsage{
		ProviderID:       providerID,
		PeriodStart:      periodStart,
		PeriodEnd:        periodEnd,
		RequestsUsed:     reqUsed,
		InputTokensUsed:  inTokUsed,
		OutputTokensUsed: outTokUsed,
		ImagesUsed:       imgsUsed,
	}
	if quotaReqs.Valid {
		v := quotaReqs.Int64
		qu.RequestsLimit = &v
	}
	if quotaInTok.Valid {
		v := quotaInTok.Int64
		qu.InputTokensLimit = &v
	}
	if quotaOutTok.Valid {
		v := quotaOutTok.Int64
		qu.OutputTokensLimit = &v
	}
	if quotaImgs.Valid {
		v := quotaImgs.Int64
		qu.ImagesLimit = &v
	}

	return qu, nil
}

func (t *tracker) Flush(ctx context.Context) error {
	return t.buf.flush(ctx)
}

// Close stops the background flush loop and closes the database.
func (t *tracker) Close() error {
	t.buf.stop()
	return t.db.Close()
}

// DB exposes the underlying database for budget checking.
func (t *tracker) DB() *sql.DB { return t.db }
