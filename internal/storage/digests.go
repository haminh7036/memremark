package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// SessionDigest represents a high-level synthesized summary of an entire session.
type SessionDigest struct {
	ID           int64     `json:"id"`
	WingID       int64     `json:"wing_id"`
	SessionID    string    `json:"session_id"`
	Request      string    `json:"request"`
	Investigated string    `json:"investigated"`
	Learned      string    `json:"learned"`
	Completed    string    `json:"completed"`
	NextSteps    string    `json:"next_steps"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
}

// UpsertSessionDigest inserts a session digest or updates existing record if session_id matches.
func (s *Store) UpsertSessionDigest(digest SessionDigest) error {
	createdAt := digest.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	unixCreatedAt := createdAt.Truncate(time.Second).Unix()

	query := `
		INSERT INTO session_digests (
			wing_id, session_id, request, investigated, learned, completed, next_steps, notes, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			wing_id = excluded.wing_id,
			request = excluded.request,
			investigated = excluded.investigated,
			learned = excluded.learned,
			completed = excluded.completed,
			next_steps = excluded.next_steps,
			notes = excluded.notes,
			created_at = excluded.created_at
	`

	_, err := s.db.Exec(
		query,
		digest.WingID,
		digest.SessionID,
		digest.Request,
		digest.Investigated,
		digest.Learned,
		digest.Completed,
		digest.NextSteps,
		digest.Notes,
		unixCreatedAt,
	)
	if err != nil {
		return fmt.Errorf("storage: upsert session digest: %w", err)
	}
	return nil
}

// GetSessionDigest retrieves the session digest for a specific session_id. Returns (nil, nil) if not found.
func (s *Store) GetSessionDigest(sessionID string) (*SessionDigest, error) {
	query := `
		SELECT id, wing_id, session_id, request, investigated, learned, completed, next_steps, notes, created_at
		FROM session_digests
		WHERE session_id = ?
	`
	var d SessionDigest
	var createdAt int64

	err := s.db.QueryRow(query, sessionID).Scan(
		&d.ID,
		&d.WingID,
		&d.SessionID,
		&d.Request,
		&d.Investigated,
		&d.Learned,
		&d.Completed,
		&d.NextSteps,
		&d.Notes,
		&createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: get session digest: %w", err)
	}
	d.CreatedAt = time.Unix(createdAt, 0)
	return &d, nil
}

// ListSessionDigests retrieves session digests for a wing ordered newest first.
func (s *Store) ListSessionDigests(wingID int64, limit int) ([]SessionDigest, error) {
	if limit <= 0 {
		limit = 10
	} else if limit > 200 {
		limit = 200
	}

	query := `
		SELECT id, wing_id, session_id, request, investigated, learned, completed, next_steps, notes, created_at
		FROM session_digests
		WHERE wing_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`

	rows, err := s.db.Query(query, wingID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: list session digests: %w", err)
	}
	defer rows.Close()

	out := make([]SessionDigest, 0)
	for rows.Next() {
		var d SessionDigest
		var createdAt int64
		if err := rows.Scan(
			&d.ID,
			&d.WingID,
			&d.SessionID,
			&d.Request,
			&d.Investigated,
			&d.Learned,
			&d.Completed,
			&d.NextSteps,
			&d.Notes,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("storage: scan session digest: %w", err)
		}
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}
