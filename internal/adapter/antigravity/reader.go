package antigravity

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/haminh7036/memremark/internal/observation"
)

// ExtractWorkspacePath parses a workspace URI string from Antigravity CLI's
// conversation_summaries table (which is commonly formatted as a JSON array
// of file:// URIs, e.g. `["file:///home/user/project"]` or `file:///home/user/project`)
// and returns a clean, canonical absolute filesystem path (e.g. `/home/user/project`).
func ExtractWorkspacePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "[") {
		var uris []string
		if err := json.Unmarshal([]byte(raw), &uris); err == nil && len(uris) > 0 {
			raw = uris[0]
		} else {
			raw = strings.TrimPrefix(raw, "[")
			raw = strings.TrimSuffix(raw, "]")
			raw = strings.Trim(raw, `"' `)
		}
	}
	if strings.HasPrefix(raw, "file://") {
		if u, err := url.Parse(raw); err == nil {
			raw = u.Path
		} else {
			raw = strings.TrimPrefix(raw, "file://")
		}
	}
	if raw == "" {
		return ""
	}
	return filepath.Clean(raw)
}

// openReadOnly opens an Antigravity CLI SQLite database that agy itself
// may still be actively writing to, using SQLite's read-only URI mode
// (verified empirically against modernc.org/sqlite v1.56.0, the version
// pinned in go.mod: mode=ro succeeds against an existing file, rejects
// writes with a driver error, and fails cleanly -- no file created -- when
// the path doesn't exist) so this package can never create or modify a
// file inside Antigravity CLI's own directory, even under the narrow race
// where the target .db is deleted between the daemon's os.Stat check and
// this open.
func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("antigravity: open %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("antigravity: set busy_timeout on %s: %w", path, err)
	}
	return db, nil
}

// ConversationInfo is one row of Antigravity CLI's conversation index.
type ConversationInfo struct {
	ID            string
	WorkspaceURIs string
	LastModified  time.Time
}

// parseSQLiteDatetime parses a last_modified_time value read back from
// SQLite. Which shape we get depends on the column's declared type
// affinity: a `text`-affinity column round-trips the space-separated string
// as stored, while modernc.org/sqlite reformats values from a
// `datetime`-affinity column (the real Antigravity CLI schema) into
// RFC3339. Both are tried since we don't control the driver's behavior.
func parseSQLiteDatetime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05.999999999Z07:00", s)
}

// ListConversations returns every conversation known to
// conversation_summaries.db, most recently modified first.
func ListConversations(summariesDBPath string) ([]ConversationInfo, error) {
	db, err := openReadOnly(summariesDBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT conversation_id, workspace_uris, last_modified_time
		 FROM conversation_summaries ORDER BY last_modified_time DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("antigravity: list conversations: %w", err)
	}
	defer rows.Close()

	var out []ConversationInfo
	for rows.Next() {
		var info ConversationInfo
		var lastModified string
		if err := rows.Scan(&info.ID, &info.WorkspaceURIs, &lastModified); err != nil {
			return nil, fmt.Errorf("antigravity: scan conversation row: %w", err)
		}
		var err error
		info.LastModified, err = parseSQLiteDatetime(lastModified)
		if err != nil {
			return nil, fmt.Errorf("antigravity: parse last_modified_time %q: %w", lastModified, err)
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// ReadObservations extracts heuristic text observations (see
// protoscan.go) from every step whose idx is greater than sinceIdx, and
// returns the highest idx seen. Pass -1 for sinceIdx to read from the
// beginning; pass the previously returned maxIdx back in on later calls
// to avoid re-processing the same steps.
//
// All observations returned from one call share a single Timestamp (at,
// the conversation's last_modified_time), because the real steps table has
// no per-step timestamp column -- this is a limitation of the source data,
// not something this function can fix. Consumers of covers_from/covers_to
// should be aware that after a daemon outage, a whole backlog of steps can
// be stamped as having happened at the same instant.
//
// On error, the returned maxIdx is always sinceIdx unchanged (never a
// partially-advanced value), so a caller that forgets to check err cannot
// silently skip rows by using it.
func ReadObservations(conversationDBPath, wingPath, sessionID string, at time.Time, sinceIdx int64) (obs []observation.Observation, maxIdx int64, err error) {
	wingPath = ExtractWorkspacePath(wingPath)
	db, err := openReadOnly(conversationDBPath)
	if err != nil {
		return nil, sinceIdx, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT idx, step_payload FROM steps WHERE idx > ? ORDER BY idx`, sinceIdx)
	if err != nil {
		return nil, sinceIdx, fmt.Errorf("antigravity: read steps from %s: %w", conversationDBPath, err)
	}
	defer rows.Close()

	maxIdx = sinceIdx
	for rows.Next() {
		var idx int64
		var payload []byte
		if err := rows.Scan(&idx, &payload); err != nil {
			return nil, sinceIdx, fmt.Errorf("antigravity: scan step row: %w", err)
		}
		if idx > maxIdx {
			maxIdx = idx
		}
		if len(payload) == 0 {
			continue
		}
		strs := ExtractStrings(payload)
		if len(strs) == 0 {
			continue
		}
		obs = append(obs, observation.Observation{
			WingPath:  wingPath,
			SessionID: sessionID,
			Content:   strings.Join(strs, "\n"),
			Timestamp: at,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, sinceIdx, err
	}
	return obs, maxIdx, nil
}
