package audit

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/helldriver666/prism/internal/types"
)

type logger struct {
	db       *sql.DB
	hmacKey  []byte
	mu       sync.Mutex // serializes writes (HMAC chain is sequential)
	prevHMAC string     // cached HMAC of the last written record
}

// NewLogger creates an audit Logger backed by the given SQLite database.
// hmacKey must be derived via HKDF from the master password with info="prism-audit-hmac".
func NewLogger(dbPath string, hmacKey []byte) (Logger, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("audit: open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	// Load the HMAC of the last record to resume the chain.
	var prevHMAC string
	err = db.QueryRow(`SELECT hmac FROM audit_log ORDER BY rowid DESC LIMIT 1`).Scan(&prevHMAC)
	if err != nil && err != sql.ErrNoRows {
		db.Close()
		return nil, fmt.Errorf("audit: load last hmac: %w", err)
	}

	return &logger{
		db:       db,
		hmacKey:  hmacKey,
		prevHMAC: prevHMAC,
	}, nil
}

func (l *logger) Log(_ context.Context, r *types.RequestRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if r.Timestamp == 0 {
		r.Timestamp = time.Now().Unix()
	}

	mac := computeHMAC(l.hmacKey, r, l.prevHMAC)

	_, err := l.db.Exec(
		`INSERT INTO audit_log
		(id, ts, project_id, provider_id, service_type, model,
		 input_tokens, output_tokens, images_count, audio_seconds, compute_units,
		 cost_usd, billing_type, latency_ms, privacy_score, pii_entities, cache_hit, route_matched, status, hmac)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Timestamp, r.ProjectID, string(r.ProviderID), string(r.ServiceType), r.Model,
		r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.ImagesCount,
		r.Usage.AudioSeconds, r.Usage.ComputeUnits,
		r.CostUSD, string(r.BillingType), r.LatencyMS, r.PrivacyScore,
		r.PIIEntitiesFound, r.CacheHit, r.RouteMatched, r.Status, mac,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}

	l.prevHMAC = mac
	return nil
}

func (l *logger) Query(ctx context.Context, f *Filter) ([]*types.RequestRecord, error) {
	query := `SELECT id, ts, project_id, provider_id, service_type, model,
	          input_tokens, output_tokens, images_count, audio_seconds, compute_units,
	          cost_usd, billing_type, latency_ms, privacy_score, pii_entities, cache_hit, route_matched, status
	          FROM audit_log WHERE 1=1`
	var args []any

	if f.ProjectID != "" {
		query += " AND project_id = ?"
		args = append(args, f.ProjectID)
	}
	if f.ProviderID != "" {
		query += " AND provider_id = ?"
		args = append(args, string(f.ProviderID))
	}
	if f.ServiceType != "" {
		query += " AND service_type = ?"
		args = append(args, string(f.ServiceType))
	}
	if !f.From.IsZero() {
		query += " AND ts >= ?"
		args = append(args, f.From.Unix())
	}
	if !f.To.IsZero() {
		query += " AND ts <= ?"
		args = append(args, f.To.Unix())
	}
	if f.Status != "" {
		query += " AND status = ?"
		args = append(args, f.Status)
	}

	query += " ORDER BY rowid ASC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", f.Offset)
	}

	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	defer rows.Close()

	var records []*types.RequestRecord
	for rows.Next() {
		r := &types.RequestRecord{}
		var prvID, svcType, billType string
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.ProjectID, &prvID, &svcType, &r.Model,
			&r.Usage.InputTokens, &r.Usage.OutputTokens, &r.Usage.ImagesCount,
			&r.Usage.AudioSeconds, &r.Usage.ComputeUnits,
			&r.CostUSD, &billType, &r.LatencyMS, &r.PrivacyScore,
			&r.PIIEntitiesFound, &r.CacheHit, &r.RouteMatched, &r.Status); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		r.ProviderID = types.ProviderID(prvID)
		r.ServiceType = types.ServiceType(svcType)
		r.BillingType = types.BillingType(billType)
		records = append(records, r)
	}
	return records, rows.Err()
}

func (l *logger) VerifyChain(ctx context.Context, from, to time.Time) error {
	// Load the HMAC of the record just before the range FIRST
	// (avoids deadlock with SetMaxOpenConns(1) if done after opening rows cursor).
	prevHMAC := ""
	err := l.db.QueryRowContext(ctx,
		`SELECT hmac FROM audit_log WHERE ts < ? ORDER BY rowid DESC LIMIT 1`,
		from.Unix()).Scan(&prevHMAC)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("audit: load prev hmac: %w", err)
	}

	rows, err := l.db.QueryContext(ctx,
		`SELECT id, ts, project_id, provider_id, service_type, model,
		 input_tokens, output_tokens, images_count, audio_seconds, compute_units,
		 cost_usd, billing_type, latency_ms, privacy_score, pii_entities, cache_hit, route_matched, status, hmac
		 FROM audit_log WHERE ts >= ? AND ts <= ? ORDER BY rowid ASC`,
		from.Unix(), to.Unix())
	if err != nil {
		return fmt.Errorf("audit: verify query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		r := &types.RequestRecord{}
		var prvID, svcType, billType, storedHMAC string
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.ProjectID, &prvID, &svcType, &r.Model,
			&r.Usage.InputTokens, &r.Usage.OutputTokens, &r.Usage.ImagesCount,
			&r.Usage.AudioSeconds, &r.Usage.ComputeUnits,
			&r.CostUSD, &billType, &r.LatencyMS, &r.PrivacyScore,
			&r.PIIEntitiesFound, &r.CacheHit, &r.RouteMatched, &r.Status, &storedHMAC); err != nil {
			return fmt.Errorf("audit: scan: %w", err)
		}
		r.ProviderID = types.ProviderID(prvID)
		r.ServiceType = types.ServiceType(svcType)
		r.BillingType = types.BillingType(billType)

		if !verifyHMAC(l.hmacKey, r, prevHMAC, storedHMAC) {
			return fmt.Errorf("audit: HMAC chain broken at record %s (ts=%d)", r.ID, r.Timestamp)
		}
		prevHMAC = storedHMAC
	}
	return rows.Err()
}

// Close closes the underlying database.
func (l *logger) Close() error {
	return l.db.Close()
}
