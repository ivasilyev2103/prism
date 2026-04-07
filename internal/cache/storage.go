package cache

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const storageDDL = `
CREATE TABLE IF NOT EXISTS cache_entries (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    service_type TEXT NOT NULL,
    model        TEXT NOT NULL DEFAULT '',
    embedding    BLOB NOT NULL,
    response     BLOB NOT NULL,
    pii_mapping  BLOB,
    created_at   INTEGER NOT NULL,
    ttl          INTEGER NOT NULL DEFAULT 3600
);

CREATE INDEX IF NOT EXISTS idx_cache_project ON cache_entries(project_id);
CREATE INDEX IF NOT EXISTS idx_cache_service ON cache_entries(service_type);
`

type storage struct {
	db *sql.DB
}

func newStorage(dbPath string) (*storage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("cache: open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(storageDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: init schema: %w", err)
	}
	return &storage{db: db}, nil
}

type storedEntry struct {
	id          string
	projectID   string
	serviceType string
	model       string
	embedding   []byte
	response    []byte
	piiMapping  []byte
	createdAt   int64
	ttl         int
}

func (s *storage) put(e *storedEntry) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO cache_entries (id, project_id, service_type, model, embedding, response, pii_mapping, created_at, ttl)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.id, e.projectID, e.serviceType, e.model, e.embedding, e.response, e.piiMapping, e.createdAt, e.ttl)
	return err
}

func (s *storage) get(id string) (*storedEntry, error) {
	e := &storedEntry{}
	err := s.db.QueryRow(
		`SELECT id, project_id, service_type, model, embedding, response, pii_mapping, created_at, ttl
		 FROM cache_entries WHERE id = ?`, id).
		Scan(&e.id, &e.projectID, &e.serviceType, &e.model, &e.embedding, &e.response, &e.piiMapping, &e.createdAt, &e.ttl)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s *storage) deleteByProject(projectID string) error {
	_, err := s.db.Exec(`DELETE FROM cache_entries WHERE project_id = ?`, projectID)
	return err
}

// loadAll returns all entries for populating the in-memory index at startup.
func (s *storage) loadAll() ([]storedEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, service_type, model, embedding, response, pii_mapping, created_at, ttl
		 FROM cache_entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []storedEntry
	for rows.Next() {
		var e storedEntry
		if err := rows.Scan(&e.id, &e.projectID, &e.serviceType, &e.model, &e.embedding, &e.response, &e.piiMapping, &e.createdAt, &e.ttl); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *storage) close() error { return s.db.Close() }
