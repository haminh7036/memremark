package antigravity

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/haminh7036/memremark/internal/observation"
)

// openReadOnly opens an Antigravity CLI SQLite database that agy itself
// may still be actively writing to.
//
// ponytail: relies on code discipline (SELECT-only, enforced by every
// caller in this file) plus a busy timeout, rather than an OS-/driver-
// enforced read-only open, since modernc.org/sqlite's exact read-only
// DSN syntax wasn't confirmed during planning. Upgrade to
// file:<path>?mode=ro once that's verified against the installed driver
// version, for a second layer of protection.
func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
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

func parseSQLiteDatetime(s string) (time.Time, error) {
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
func ReadObservations(conversationDBPath, wingPath, sessionID string, at time.Time, sinceIdx int64) (obs []observation.Observation, maxIdx int64, err error) {
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
			return nil, maxIdx, fmt.Errorf("antigravity: scan step row: %w", err)
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
	return obs, maxIdx, rows.Err()
}
