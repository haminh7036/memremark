package storage

import (
	"database/sql"
	"fmt"
	"strings"
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

// LastSummaryCoversTo returns covers_to (the real verbatim event-time the
// most recent summary drawer distilled up to) for a wing/session. ok is
// false if no summary exists yet.
//
// This deliberately reads covers_to, not created_at: created_at is the
// daemon's poll-time wall-clock when the summary row was inserted, while
// VerbatimSince filters verbatim rows by their OWN event-time created_at.
// Those are different clocks that can diverge (backlog catch-up after
// downtime, out-of-order transcript discovery), and using the insertion
// wall-clock here could make a verbatim row whose real event-time is older
// than that wall-clock permanently fail VerbatimSince's filter. covers_to
// lives in the same clock domain VerbatimSince compares against.
func (s *Store) LastSummaryCoversTo(wingID int64, sessionID string) (time.Time, bool, error) {
	var coversTo int64
	err := s.db.QueryRow(
		`SELECT covers_to FROM drawers
		 WHERE wing_id = ? AND session_id = ? AND type = 'summary'
		 ORDER BY created_at DESC LIMIT 1`,
		wingID, sessionID,
	).Scan(&coversTo)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("storage: query last summary covers_to: %w", err)
	}
	return time.Unix(coversTo, 0), true, nil
}

// SearchDrawers queries drawers matching the filters, ordered newest first.
func (s *Store) SearchDrawers(wingID int64, query, hall, drawerType string, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 10
	} else if limit > 50 {
		limit = 50
	}

	var conditions []string
	var args []interface{}

	if wingID > 0 {
		conditions = append(conditions, "wing_id = ?")
		args = append(args, wingID)
	}
	if query != "" {
		conditions = append(conditions, "content LIKE ?")
		args = append(args, fmt.Sprintf("%%%s%%", query))
	}
	if hall != "" {
		conditions = append(conditions, "hall = ?")
		args = append(args, hall)
	}
	if drawerType == "summary" || drawerType == "verbatim" {
		conditions = append(conditions, "type = ?")
		args = append(args, drawerType)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	querySQL := fmt.Sprintf(
		`SELECT id, type, hall, tool_name, content, created_at FROM drawers
		 %s
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		whereClause,
	)
	args = append(args, limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: search drawers: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var drawerType string
		var toolName sql.NullString
		var createdAt int64
		if err := rows.Scan(&d.ID, &drawerType, &d.Hall, &toolName, &d.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan search row: %w", err)
		}
		d.ToolName = toolName.String
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// InsertManualSummary records a user or AI explicit summary drawer.
func (s *Store) InsertManualSummary(wingID int64, hall, content string, createdAt time.Time) (int64, error) {
	if !isValidHall(hall) {
		return 0, fmt.Errorf("storage: invalid hall %q", hall)
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	unixTime := createdAt.Truncate(time.Second).Unix()
	res, err := s.db.Exec(
		`INSERT INTO drawers (wing_id, type, hall, content, session_id, covers_from, covers_to, created_at)
		 VALUES (?, 'summary', ?, ?, 'manual', ?, ?, ?)`,
		wingID, hall, content, unixTime, unixTime, unixTime,
	)
	if err != nil {
		return 0, fmt.Errorf("storage: insert manual summary: %w", err)
	}
	return res.LastInsertId()
}

// GetTimeline retrieves chronological sequence of events for a wing/session.
func (s *Store) GetTimeline(wingID int64, sessionID string, since time.Time, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}

	var conditions []string
	var args []interface{}

	if wingID > 0 {
		conditions = append(conditions, "wing_id = ?")
		args = append(args, wingID)
	}
	if sessionID != "" {
		conditions = append(conditions, "session_id = ?")
		args = append(args, sessionID)
	}
	if !since.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, since.Unix())
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	querySQL := fmt.Sprintf(
		`SELECT id, type, hall, tool_name, content, created_at FROM drawers
		 %s
		 ORDER BY created_at ASC, id ASC LIMIT ?`,
		whereClause,
	)
	args = append(args, limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: get timeline: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var drawerType string
		var toolName sql.NullString
		var createdAt int64
		if err := rows.Scan(&d.ID, &drawerType, &d.Hall, &toolName, &d.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan timeline row: %w", err)
		}
		d.ToolName = toolName.String
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDrawer deletes a drawer by ID, returning true if deleted.
func (s *Store) DeleteDrawer(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM drawers WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("storage: delete drawer: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}

