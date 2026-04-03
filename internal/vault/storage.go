package vault

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS providers (
	id               TEXT PRIMARY KEY,
	encrypted_key    BLOB NOT NULL,
	allowed_projects TEXT NOT NULL DEFAULT '*',
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
	hmac_key    BLOB PRIMARY KEY,
	project_id  TEXT NOT NULL,
	allowed_providers TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS metadata (
	key   TEXT PRIMARY KEY,
	value BLOB NOT NULL
);
`

// storage wraps SQLite database operations for the vault.
type storage struct {
	db *sql.DB
}

// openStorage opens (or creates) the vault SQLite database at the given path.
func openStorage(dbPath string) (*storage, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("vault: create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("vault: open database: %w", err)
	}

	// WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("vault: set WAL mode: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("vault: create schema: %w", err)
	}

	return &storage{db: db}, nil
}

// close closes the database connection.
func (s *storage) close() error {
	return s.db.Close()
}

// --- Metadata (salt) ---

func (s *storage) getSalt() ([]byte, error) {
	var value []byte
	err := s.db.QueryRow("SELECT value FROM metadata WHERE key = 'salt'").Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vault: get salt: %w", err)
	}
	return value, nil
}

func (s *storage) setSalt(salt []byte) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO metadata (key, value) VALUES ('salt', ?)", salt)
	if err != nil {
		return fmt.Errorf("vault: set salt: %w", err)
	}
	return nil
}

// --- Providers ---

func (s *storage) putProvider(id string, encryptedKey []byte, allowedProjects string, now int64) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO providers (id, encrypted_key, allowed_projects, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		id, encryptedKey, allowedProjects, now, now)
	if err != nil {
		return fmt.Errorf("vault: put provider: %w", err)
	}
	return nil
}

func (s *storage) getProvider(id string) (encryptedKey []byte, allowedProjects string, err error) {
	err = s.db.QueryRow(
		"SELECT encrypted_key, allowed_projects FROM providers WHERE id = ?", id,
	).Scan(&encryptedKey, &allowedProjects)
	if err == sql.ErrNoRows {
		return nil, "", ErrProviderNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("vault: get provider: %w", err)
	}
	return encryptedKey, allowedProjects, nil
}

func (s *storage) updateProviderKey(id string, encryptedKey []byte, now int64) error {
	result, err := s.db.Exec(
		"UPDATE providers SET encrypted_key = ?, updated_at = ? WHERE id = ?",
		encryptedKey, now, id)
	if err != nil {
		return fmt.Errorf("vault: update provider key: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("vault: rows affected: %w", err)
	}
	if rows == 0 {
		return ErrProviderNotFound
	}
	return nil
}

func (s *storage) deleteProvider(id string) error {
	result, err := s.db.Exec("DELETE FROM providers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("vault: delete provider: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("vault: rows affected: %w", err)
	}
	if rows == 0 {
		return ErrProviderNotFound
	}
	return nil
}

func (s *storage) providerExists(id string) bool {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM providers WHERE id = ?", id).Scan(&count)
	return count > 0
}

// --- Tokens ---

func (s *storage) putToken(hmacKey []byte, projectID, allowedProviders string, createdAt, expiresAt int64) error {
	_, err := s.db.Exec(
		"INSERT INTO tokens (hmac_key, project_id, allowed_providers, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		hmacKey, projectID, allowedProviders, createdAt, expiresAt)
	if err != nil {
		return fmt.Errorf("vault: put token: %w", err)
	}
	return nil
}

type tokenRecord struct {
	HMACKey          []byte
	ProjectID        string
	AllowedProviders string
	CreatedAt        int64
	ExpiresAt        int64
}

func (s *storage) getTokenByHMAC(hmacKey []byte) (*tokenRecord, error) {
	rec := &tokenRecord{}
	err := s.db.QueryRow(
		"SELECT hmac_key, project_id, allowed_providers, created_at, expires_at FROM tokens WHERE hmac_key = ?",
		hmacKey,
	).Scan(&rec.HMACKey, &rec.ProjectID, &rec.AllowedProviders, &rec.CreatedAt, &rec.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("vault: get token: %w", err)
	}
	return rec, nil
}

func (s *storage) deleteTokenByHMAC(hmacKey []byte) error {
	result, err := s.db.Exec("DELETE FROM tokens WHERE hmac_key = ?", hmacKey)
	if err != nil {
		return fmt.Errorf("vault: delete token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("vault: rows affected: %w", err)
	}
	if rows == 0 {
		return ErrInvalidToken
	}
	return nil
}
