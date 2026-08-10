package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS wings (
	id INTEGER PRIMARY KEY,
	path TEXT UNIQUE NOT NULL,
	name TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS drawers (
	id INTEGER PRIMARY KEY,
	wing_id INTEGER NOT NULL REFERENCES wings(id),
	room TEXT,
	type TEXT NOT NULL CHECK (type IN ('verbatim','summary')),
	hall TEXT NOT NULL CHECK (hall IN ('event','fact','discovery','preference','advice')),
	content TEXT NOT NULL,
	tool_name TEXT,
	session_id TEXT NOT NULL,
	covers_from INTEGER,
	covers_to INTEGER,
	created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_drawers_wing_type_created ON drawers(wing_id, type, created_at);
CREATE INDEX IF NOT EXISTS idx_drawers_wing_session_type ON drawers(wing_id, session_id, type, created_at);
`

// Store wraps the memremark SQLite database.
type Store struct {
	db *sql.DB
}

// Open creates the parent directory if needed, opens the SQLite database
// at path, and applies the schema. Safe to call repeatedly (idempotent).
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("storage: create dir %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
