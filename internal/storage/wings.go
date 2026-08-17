package storage

import (
	"fmt"
	"path/filepath"
	"time"
)

// GetOrCreateWing returns the wing id for the given project path,
// creating a new wing row if one doesn't exist yet.
//
// This is a single atomic upsert rather than SELECT-then-INSERT: two
// processes (the daemon's long-lived connection and a hook binary's
// fresh-per-invocation connection) can race to create the same brand-new
// path, and a check-then-insert would let the second INSERT fail on the
// UNIQUE(path) constraint instead of returning the existing id.
func (s *Store) GetOrCreateWing(path string) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO wings (path, name, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET path = path
		 RETURNING id`,
		path, filepath.Base(path), time.Now().Unix(),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: get or create wing %s: %w", path, err)
	}
	return id, nil
}
