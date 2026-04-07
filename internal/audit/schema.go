package audit

import (
	"database/sql"
	"fmt"
)

const ddl = `
CREATE TABLE IF NOT EXISTS audit_log (
    rowid         INTEGER PRIMARY KEY AUTOINCREMENT,
    id            TEXT    NOT NULL UNIQUE,
    ts            INTEGER NOT NULL,
    project_id    TEXT    NOT NULL,
    provider_id   TEXT    NOT NULL,
    service_type  TEXT    NOT NULL,
    model         TEXT    NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    images_count  INTEGER NOT NULL DEFAULT 0,
    audio_seconds REAL    NOT NULL DEFAULT 0,
    compute_units REAL    NOT NULL DEFAULT 0,
    cost_usd      REAL    NOT NULL DEFAULT 0,
    billing_type  TEXT    NOT NULL,
    latency_ms    INTEGER NOT NULL,
    privacy_score REAL,
    pii_entities  INTEGER NOT NULL DEFAULT 0,
    cache_hit     BOOLEAN NOT NULL DEFAULT 0,
    route_matched TEXT,
    status        TEXT    NOT NULL,
    hmac          TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_ts      ON audit_log(ts);
CREATE INDEX IF NOT EXISTS idx_audit_project ON audit_log(project_id, ts);

-- WORM triggers: prevent UPDATE and DELETE on audit_log.
CREATE TRIGGER IF NOT EXISTS audit_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit log is immutable: UPDATE not allowed');
END;

CREATE TRIGGER IF NOT EXISTS audit_no_delete BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit log is immutable: DELETE not allowed');
END;
`

// initSchema creates the audit table, indices, and WORM triggers.
func initSchema(db *sql.DB) error {
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("audit: init schema: %w", err)
	}
	return nil
}
