package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// Hall values a summary drawer may carry.
const (
	HallFact       = "fact"
	HallDiscovery  = "discovery"
	HallPreference = "preference"
	HallAdvice     = "advice"
)

func isValidHall(hall string) bool {
	switch hall {
	case HallFact, HallDiscovery, HallPreference, HallAdvice:
		return true
	default:
		return false
	}
}

// InsertVerbatimDrawer records one raw observation for a wing.
func (s *Store) InsertVerbatimDrawer(wingID int64, sessionID, toolName, content string, createdAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO drawers (wing_id, type, hall, content, tool_name, session_id, created_at)
		 VALUES (?, 'verbatim', 'event', ?, ?, ?, ?)`,
		wingID, content, toolName, sessionID, createdAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: insert verbatim drawer: %w", err)
	}
	return nil
}

// InsertSummaryDrawer records one distilled summary for a wing.
func (s *Store) InsertSummaryDrawer(wingID int64, sessionID, hall, content string, coversFrom, coversTo, createdAt time.Time) error {
	if !isValidHall(hall) {
		return fmt.Errorf("storage: invalid hall %q", hall)
	}
	_, err := s.db.Exec(
		`INSERT INTO drawers (wing_id, type, hall, content, session_id, covers_from, covers_to, created_at)
		 VALUES (?, 'summary', ?, ?, ?, ?, ?, ?)`,
		wingID, hall, content, sessionID, coversFrom.Unix(), coversTo.Unix(), createdAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: insert summary drawer: %w", err)
	}
	return nil
}

// Drawer is a single row read back from the drawers table. ToolName is
// only populated for verbatim rows.
type Drawer struct {
	ID        int64
	Hall      string
	ToolName  string
	Content   string
	CreatedAt time.Time
}

// RecentSummaries returns up to limit summary drawers for a wing, most
// recent first.
func (s *Store) RecentSummaries(wingID int64, limit int) ([]Drawer, error) {
	rows, err := s.db.Query(
		`SELECT id, hall, content, created_at FROM drawers
		 WHERE wing_id = ? AND type = 'summary'
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		wingID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: query recent summaries: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var createdAt int64
		if err := rows.Scan(&d.ID, &d.Hall, &d.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan summary row: %w", err)
		}
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// VerbatimSince returns verbatim drawers for a wing/session created
// strictly after the given time, oldest first.
func (s *Store) VerbatimSince(wingID int64, sessionID string, since time.Time) ([]Drawer, error) {
	rows, err := s.db.Query(
		`SELECT id, content, tool_name, created_at FROM drawers
		 WHERE wing_id = ? AND session_id = ? AND type = 'verbatim' AND created_at > ?
		 ORDER BY created_at ASC`,
		wingID, sessionID, since.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: query verbatim since: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var toolName sql.NullString
		var createdAt int64
		if err := rows.Scan(&d.ID, &d.Content, &toolName, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan verbatim row: %w", err)
		}
		d.ToolName = toolName.String
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// LastSummaryTime returns the created_at of the most recent summary
// drawer for a wing/session. ok is false if no summary exists yet.
func (s *Store) LastSummaryTime(wingID int64, sessionID string) (time.Time, bool, error) {
	var createdAt int64
	err := s.db.QueryRow(
		`SELECT created_at FROM drawers
		 WHERE wing_id = ? AND session_id = ? AND type = 'summary'
		 ORDER BY created_at DESC LIMIT 1`,
		wingID, sessionID,
	).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("storage: query last summary time: %w", err)
	}
	return time.Unix(createdAt, 0), true, nil
}
