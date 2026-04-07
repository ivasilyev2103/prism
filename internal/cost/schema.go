package cost

import (
	"database/sql"
	"fmt"
)

const ddl = `
CREATE TABLE IF NOT EXISTS providers (
    id                  TEXT PRIMARY KEY,
    display_name        TEXT NOT NULL,
    billing_type        TEXT NOT NULL,
    price_input_per_1m  REAL,
    price_output_per_1m REAL,
    price_per_unit      REAL,
    price_per_second    REAL,
    prices_updated_at   INTEGER,
    sub_plan_name           TEXT,
    sub_period              TEXT,
    sub_cost_usd            REAL,
    sub_reset_day           INTEGER,
    sub_quota_requests      INTEGER,
    sub_quota_input_tokens  INTEGER,
    sub_quota_output_tokens INTEGER,
    sub_quota_images        INTEGER
);

CREATE TABLE IF NOT EXISTS requests (
    id            TEXT PRIMARY KEY,
    ts            INTEGER NOT NULL,
    project_id    TEXT NOT NULL,
    provider_id   TEXT NOT NULL,
    service_type  TEXT NOT NULL,
    model         TEXT NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    images_count  INTEGER NOT NULL DEFAULT 0,
    audio_seconds REAL    NOT NULL DEFAULT 0,
    compute_units REAL    NOT NULL DEFAULT 0,
    cost_usd      REAL    NOT NULL DEFAULT 0,
    billing_type  TEXT    NOT NULL,
    latency_ms    INTEGER NOT NULL,
    privacy_score REAL,
    cache_hit     BOOLEAN NOT NULL DEFAULT 0,
    route_matched TEXT,
    status        TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_requests_project  ON requests(project_id, ts);
CREATE INDEX IF NOT EXISTS idx_requests_provider ON requests(provider_id, ts);
CREATE INDEX IF NOT EXISTS idx_requests_pair     ON requests(project_id, provider_id, ts);
CREATE INDEX IF NOT EXISTS idx_requests_service  ON requests(service_type, ts);

CREATE TABLE IF NOT EXISTS budgets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    level       TEXT NOT NULL,
    project_id  TEXT,
    provider_id TEXT,
    limit_usd   REAL NOT NULL,
    period      TEXT NOT NULL DEFAULT 'monthly',
    action      TEXT NOT NULL DEFAULT 'block'
);

CREATE INDEX IF NOT EXISTS idx_budgets_lookup ON budgets(level, project_id, provider_id);
`

// initSchema creates the cost tables and indices.
func initSchema(db *sql.DB) error {
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("cost: init schema: %w", err)
	}
	return nil
}
