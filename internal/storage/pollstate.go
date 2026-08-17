package storage

import (
	"database/sql"
	"fmt"
)

// GetPollState returns the persisted integer value for key, and ok=false
// if no value has ever been stored under that key.
func (s *Store) GetPollState(key string) (int64, bool, error) {
	var value int64
	err := s.db.QueryRow(`SELECT value FROM poll_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("storage: get poll state %s: %w", key, err)
	}
	return value, true, nil
}

// SetPollState persists value under key, creating or replacing any
// existing entry for it.
//
// Same upsert idiom as GetOrCreateWing: one atomic statement rather than
// SELECT-then-INSERT/UPDATE, since the daemon's poll loop is the only
// writer but there's no reason to invent a different pattern here.
func (s *Store) SetPollState(key string, value int64) error {
	_, err := s.db.Exec(
		`INSERT INTO poll_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("storage: set poll state %s: %w", key, err)
	}
	return nil
}
