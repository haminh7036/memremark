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

CREATE TABLE IF NOT EXISTS poll_state (
	key TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
`

// Store wraps the memremark SQLite database.
type Store struct {
	db *sql.DB
}

// Open creates the parent directory if needed, opens the SQLite database
// at path, and applies the schema. Safe to call repeatedly (idempotent).
func Open(path string) (*Store, error) {
	// 0o700: this database is a private activity log (raw commands, file
	// contents touched) -- the directory and file must not be world/group
	// readable.
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("storage: create dir %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	// SQLite allows only one writer at a time anyway, and PRAGMA busy_timeout
	// is per-connection: without pinning the pool to a single connection,
	// database/sql could hand out a fresh, un-pragma'd connection to a
	// concurrent caller and lose the busy_timeout entirely.
	db.SetMaxOpenConns(1)
	// Same reasoning as antigravity.openReadOnly: the daemon and the
	// per-session hook binary can both hit this file at once, so give a
	// concurrent writer a chance to wait instead of failing immediately
	// with "database is locked".
	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: set busy_timeout on %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: apply schema: %w", err)
	}
	// Migrate any legacy URI or unnormalized paths in the wings table
	if err := migrateLegacyWings(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: migrate legacy wings: %w", err)
	}
	// Unconditional: cheap, and simpler than tracking whether the file was
	// freshly created this call.
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: chmod %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// migrateLegacyWings normalizes any raw URI paths in the wings table and merges duplicates.
func migrateLegacyWings(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, path FROM wings WHERE path LIKE '["file://%' OR path LIKE 'file://%' OR path LIKE '["/%'`)
	if err != nil {
		return fmt.Errorf("query legacy wings: %w", err)
	}
	defer rows.Close()

	type wingFix struct {
		oldID     int64
		oldPath   string
		cleanPath string
	}
	var fixes []wingFix
	for rows.Next() {
		var id int64
		var oldPath string
		if err := rows.Scan(&id, &oldPath); err != nil {
			return fmt.Errorf("scan legacy wing: %w", err)
		}
		cleanPath := normalizePath(oldPath)
		if cleanPath != "" && cleanPath != oldPath {
			fixes = append(fixes, wingFix{oldID: id, oldPath: oldPath, cleanPath: cleanPath})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, f := range fixes {
		var existingID int64
		err := db.QueryRow(`SELECT id FROM wings WHERE path = ?`, f.cleanPath).Scan(&existingID)
		if err == sql.ErrNoRows {
			cleanName := filepath.Base(f.cleanPath)
			if cleanName == "." || cleanName == "/" || cleanName == "" {
				cleanName = f.cleanPath
			}
			if _, err := db.Exec(`UPDATE wings SET path = ?, name = ? WHERE id = ?`, f.cleanPath, cleanName, f.oldID); err != nil {
				return fmt.Errorf("update legacy wing %d: %w", f.oldID, err)
			}
		} else if err == nil {
			// Merge duplicates: re-point drawers from oldID to existingID, then delete old wing
			if _, err := db.Exec(`UPDATE drawers SET wing_id = ? WHERE wing_id = ?`, existingID, f.oldID); err != nil {
				return fmt.Errorf("re-point drawers from wing %d to %d: %w", f.oldID, existingID, err)
			}
			if _, err := db.Exec(`DELETE FROM wings WHERE id = ?`, f.oldID); err != nil {
				return fmt.Errorf("delete duplicate wing %d: %w", f.oldID, err)
			}
		} else {
			return fmt.Errorf("check existing wing for %s: %w", f.cleanPath, err)
		}
	}
	return nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
