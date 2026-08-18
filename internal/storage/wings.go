package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// normalizePath converts various path formats (JSON arrays, file:// URIs) into
// a canonical, clean filesystem path.
func normalizePath(raw string) string {
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

// GetOrCreateWing returns the wing id for the given project path,
// creating a new wing row if one doesn't exist yet.
//
// This is a single atomic upsert rather than SELECT-then-INSERT: two
// processes (the daemon's long-lived connection and a hook binary's
// fresh-per-invocation connection) can race to create the same brand-new
// path, and a check-then-insert would let the second INSERT fail on the
// UNIQUE(path) constraint instead of returning the existing id.
func (s *Store) GetOrCreateWing(path string) (int64, error) {
	path = normalizePath(path)
	if path == "" {
		return 0, fmt.Errorf("storage: get or create wing: empty path")
	}

	name := filepath.Base(path)
	if name == "." || name == "/" || name == "" {
		name = path
	}

	var id int64
	err := s.db.QueryRow(
		`INSERT INTO wings (path, name, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET path = path
		 RETURNING id`,
		path, name, time.Now().Unix(),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("storage: get or create wing %s: %w", path, err)
	}
	return id, nil
}
