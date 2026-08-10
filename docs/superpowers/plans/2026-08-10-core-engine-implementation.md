# Core Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the memremark Core Engine daemon that captures tool-use observations from Claude Code and Antigravity CLI transcripts, stores them durably in SQLite, distills them into summaries via a headless CLI sub-conversation, and injects those summaries back into new Claude Code sessions.

**Architecture:** A single Go daemon (`memremarkd`) polls each CLI's own on-disk transcript store on a fixed interval, normalizes new events into a shared `Observation` type, writes them to SQLite immediately (verbatim), and — once a session goes idle — spawns a headless `claude -p` / `agy -p` sub-conversation to distill accumulated verbatim drawers into hall-classified summary drawers. A small companion binary wired to Claude Code's `SessionStart` hook reads the latest summaries for the current project and injects them into a new session via `hookSpecificOutput.additionalContext`.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite` (pure-Go SQLite driver), `google.golang.org/protobuf/encoding/protowire` (generic protobuf wire-format scanning, no schema needed).

## Global Constraints

- Module path: `github.com/haminh7036/memremark` (matches `git@github.com:haminh7036/memremark.git`).
- Storage: exactly one SQLite file per machine at `~/.memremark/memremark.db`, schema per the approved spec (`docs/superpowers/specs/2026-08-10-core-engine-design.md` §7) — `wings(id, path, name, created_at)` and `drawers(id, wing_id, room, type, hall, content, tool_name, session_id, covers_from, covers_to, created_at)`. `type` is `verbatim`|`summary`; `hall` is `event`|`fact`|`discovery`|`preference`|`advice`.
- Claude Code transcripts live at `~/.claude/projects/**/*.jsonl` (confirmed real format, one JSON object per line — see Task 4). Antigravity CLI transcripts live at `~/.gemini/antigravity-cli/conversations/<id>.db` (SQLite, `steps` table, `step_payload` column is a **protobuf-encoded blob**, confirmed empirically — see Task 7) with an index at `~/.gemini/antigravity-cli/conversation_summaries.db` (`conversation_summaries` table mapping `conversation_id` → `workspace_uris`, `last_modified_time`).
- Every task ships with real unit tests (this is a standing, non-negotiable requirement — see `feedback_always_unit_test` in project memory). No task is complete without its tests passing.
- Deliberate v1 scope decisions, each with a documented ceiling and upgrade path (ponytail convention: `ponytail: <ceiling>, <upgrade path>`):
  - **No hook-nudge listening in v1.** The daemon polls on a fixed interval only. The spec (§5) explicitly allows this — hooks are "optional, best-effort, never the sole trigger." Upgrade path: add a nudge file/socket if 3-second latency ever matters in practice.
  - **Antigravity transcript parsing is heuristic, not a schema-mapped decode.** `step_payload` is protobuf but Antigravity CLI's `.proto` definitions are proprietary and undocumented (spec §10). Task 7 recovers embedded readable strings via generic wire-format scanning, without knowing which field number means what. Upgrade path: map real field numbers once/if structured tool-name/argument separation is needed.
  - **Antigravity context-injection is out of scope for this plan.** Spec §10 Open Risk #2: there is no confirmed mechanism to deliver text into a new Antigravity CLI session (its hooks don't execute at all — see spec §2.1). Only Claude Code's `SessionStart` injection (Task 6) is built here; Antigravity's equivalent needs its own research spike first.
  - **Read-only access to Antigravity's live `.db` files relies on code discipline (SELECT-only) + `PRAGMA busy_timeout`,** not a verified read-only DSN flag, since `modernc.org/sqlite`'s exact read-only URI syntax wasn't confirmed during planning (spec §8). Upgrade path: switch to `file:<path>?mode=ro` once verified against the installed driver version.
  - **Test fixtures are schema-accurate synthetic data**, built from the real field names/table structures confirmed during the design spec's empirical investigation — not copies of the user's actual private transcripts, to avoid committing private conversation content to the repo.

---

## File Structure

```
go.mod
internal/
  storage/
    storage.go          # SQLite schema + Store CRUD (wings, drawers)
    storage_test.go
  observation/
    observation.go       # shared Observation struct
  debounce/
    debounce.go           # idle-detection/debounce Tracker
    debounce_test.go
  adapter/
    claudecode/
      parser.go            # tool_use/tool_result pairing -> Observation
      parser_test.go
      testdata/sample.jsonl
      tailer.go             # transcript discovery + byte-offset tailing
      tailer_test.go
    antigravity/
      protoscan.go          # generic protobuf string extraction
      protoscan_test.go
      reader.go              # conversation discovery + steps reading
      reader_test.go
  summarizer/
    summarizer.go          # Invoker interface, Claude/Antigravity invokers, prompt+parse
    summarizer_test.go
  daemon/
    daemon.go                # poll loop wiring everything together
    daemon_test.go
cmd/
  memremarkd/
    main.go                   # daemon entrypoint
  memremark-hook-claude-sessionstart/
    main.go                    # SessionStart hook binary
    main_test.go
```

---

### Task 1: Go module scaffold + SQLite schema

**Files:**
- Create: `go.mod`
- Create: `internal/storage/storage.go`
- Test: `internal/storage/storage_test.go` (schema/Open portion only — CRUD tests come in Task 2)

**Interfaces:**
- Produces: `storage.Store`, `storage.Open(path string) (*Store, error)`, `(*Store) Close() error`

- [ ] **Step 1: Initialize the Go module and add the SQLite dependency**

Run:
```bash
cd /home/minh/personal/memremark
go mod init github.com/haminh7036/memremark
go get modernc.org/sqlite
```

- [ ] **Step 2: Write the failing test**

Create `internal/storage/storage_test.go`:
```go
package storage

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "memremark.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
}

func TestOpenIsIdempotentOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memremark.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open on existing file: %v", err)
	}
	defer s2.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `storage.Open` undefined (package doesn't exist yet).

- [ ] **Step 3: Write the implementation**

Create `internal/storage/storage.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/storage/storage.go internal/storage/storage_test.go
git commit -m "feat: add SQLite schema and Store.Open/Close"
```

---

### Task 2: Storage CRUD (wings + drawers)

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/storage_test.go`

**Interfaces:**
- Consumes: `storage.Store` from Task 1
- Produces: `(*Store) GetOrCreateWing(path string) (int64, error)`, `(*Store) InsertVerbatimDrawer(wingID int64, sessionID, toolName, content string, createdAt time.Time) error`, `(*Store) InsertSummaryDrawer(wingID int64, sessionID, hall, content string, coversFrom, coversTo, createdAt time.Time) error`, `(*Store) RecentSummaries(wingID int64, limit int) ([]Drawer, error)`, `(*Store) VerbatimSince(wingID int64, sessionID string, since time.Time) ([]Drawer, error)`, `(*Store) LastSummaryTime(wingID int64, sessionID string) (time.Time, bool, error)`, `storage.Drawer{ID int64, Hall, ToolName, Content string, CreatedAt time.Time}`, constants `storage.HallFact`, `storage.HallDiscovery`, `storage.HallPreference`, `storage.HallAdvice`

- [ ] **Step 1: Write the failing tests**

Append to `internal/storage/storage_test.go`:
```go
import (
	"path/filepath"
	"testing"
	"time"
)

func TestGetOrCreateWingIsIdempotentPerPath(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id1, err := s.GetOrCreateWing("/home/minh/personal/memremark")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	if id1 == 0 {
		t.Fatalf("expected non-zero wing id")
	}

	id2, err := s.GetOrCreateWing("/home/minh/personal/memremark")
	if err != nil {
		t.Fatalf("GetOrCreateWing (2nd call): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected same wing id, got %d and %d", id1, id2)
	}

	id3, err := s.GetOrCreateWing("/home/minh/personal/other-project")
	if err != nil {
		t.Fatalf("GetOrCreateWing (other path): %v", err)
	}
	if id3 == id1 {
		t.Fatalf("expected a different wing id for a different path")
	}
}

func TestInsertVerbatimAndSummaryDrawers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, err := s.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now()
	if err := s.InsertVerbatimDrawer(wingID, "session-1", "Read", "read foo.txt", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "session-1", HallFact, "decided to use SQLite", now.Add(-time.Hour), now, now); err != nil {
		t.Fatalf("InsertSummaryDrawer: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "session-1", "not-a-real-hall", "x", now, now, now); err == nil {
		t.Fatalf("expected error for invalid hall, got nil")
	}

	summaries, err := s.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary drawer, got %d", len(summaries))
	}
	if summaries[0].Hall != HallFact || summaries[0].Content != "decided to use SQLite" {
		t.Fatalf("unexpected summary: %+v", summaries[0])
	}
}

func TestRecentSummariesOrdersNewestFirst(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now()

	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "older fact", older, older, older); err != nil {
		t.Fatalf("insert older: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "newer fact", newer, newer, newer); err != nil {
		t.Fatalf("insert newer: %v", err)
	}

	summaries, err := s.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 2 || summaries[0].Content != "newer fact" {
		t.Fatalf("expected newest first, got %+v", summaries)
	}
}

func TestVerbatimSinceReturnsOnlyNewerRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	if err := s.InsertVerbatimDrawer(wingID, "s1", "Bash", "old command", older); err != nil {
		t.Fatalf("insert older: %v", err)
	}
	if err := s.InsertVerbatimDrawer(wingID, "s1", "Read", "new command", newer); err != nil {
		t.Fatalf("insert newer: %v", err)
	}

	got, err := s.VerbatimSince(wingID, "s1", older)
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(got) != 1 || got[0].Content != "new command" || got[0].ToolName != "Read" {
		t.Fatalf("expected only the newer row, got %+v", got)
	}
}

func TestLastSummaryTimeReturnsFalseWhenNoneExists(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	_, ok, err := s.LastSummaryTime(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryTime: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when no summary exists")
	}
}

func TestLastSummaryTimeReturnsMostRecent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	first := time.Now().Add(-time.Hour)
	second := time.Now()
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "a", first, first, first); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "b", second, second, second); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	got, ok, err := s.LastSummaryTime(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryTime: %v", err)
	}
	if !ok || !got.Equal(second) {
		t.Fatalf("expected %v (ok=true), got %v (ok=%v)", second, got, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/...`
Expected: FAIL — `GetOrCreateWing`, `InsertVerbatimDrawer`, etc. undefined.

- [ ] **Step 3: Write the implementation**

Append to `internal/storage/storage.go`:
```go
import (
	"time"
)

// GetOrCreateWing returns the wing id for the given project path,
// creating a new wing row if one doesn't exist yet.
func (s *Store) GetOrCreateWing(path string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM wings WHERE path = ?`, path).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("storage: query wing %s: %w", path, err)
	}
	res, err := s.db.Exec(
		`INSERT INTO wings (path, name, created_at) VALUES (?, ?, ?)`,
		path, filepath.Base(path), time.Now().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("storage: insert wing %s: %w", path, err)
	}
	return res.LastInsertId()
}

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
		 ORDER BY created_at DESC LIMIT ?`,
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
```

Update the single `import (...)` block at the top of `storage.go` (Go only allows one per file) so it reads:
```go
import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/... -v`
Expected: PASS (all 8 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/storage/storage.go internal/storage/storage_test.go
git commit -m "feat: add storage CRUD for wings and drawers"
```

---

### Task 3: Shared Observation type + debounce tracker

**Files:**
- Create: `internal/observation/observation.go`
- Create: `internal/debounce/debounce.go`
- Test: `internal/debounce/debounce_test.go`

**Interfaces:**
- Produces: `observation.Observation{WingPath, SessionID, ToolName, Content string, Timestamp time.Time}`, `debounce.Tracker`, `debounce.NewTracker() *Tracker`, `(*Tracker) Touch(sessionID string, at time.Time)`, `(*Tracker) Due(now time.Time, idleWindow time.Duration) []string`

- [ ] **Step 1: Write the failing test**

Create `internal/debounce/debounce_test.go`:
```go
package debounce

import (
	"testing"
	"time"
)

func TestDueFiresOnceAfterIdleWindow(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("session-1", base)

	if due := tr.Due(base.Add(1*time.Second), 5*time.Second); len(due) != 0 {
		t.Fatalf("expected no due sessions before idle window elapses, got %v", due)
	}

	due := tr.Due(base.Add(6*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "session-1" {
		t.Fatalf("expected [session-1] due, got %v", due)
	}

	due2 := tr.Due(base.Add(20*time.Second), 5*time.Second)
	if len(due2) != 0 {
		t.Fatalf("expected no repeat firing without a new Touch, got %v", due2)
	}
}

func TestTouchResetsFiredState(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("session-1", base)
	tr.Due(base.Add(6*time.Second), 5*time.Second)

	tr.Touch("session-1", base.Add(10*time.Second))
	due := tr.Due(base.Add(16*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "session-1" {
		t.Fatalf("expected session-1 due again after new activity, got %v", due)
	}
}

func TestDueTracksMultipleSessionsIndependently(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("a", base)
	tr.Touch("b", base.Add(3*time.Second))

	due := tr.Due(base.Add(6*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "a" {
		t.Fatalf("expected only session a due, got %v", due)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/debounce/...`
Expected: FAIL — package `debounce` doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/observation/observation.go`:
```go
package observation

import "time"

// Observation is one normalized tool-use event captured from a CLI's own
// transcript, independent of which CLI produced it.
type Observation struct {
	WingPath  string // absolute project directory the observation belongs to
	SessionID string
	ToolName  string // empty for Antigravity CLI observations in v1 -- see reader.go
	Content   string // human-readable summary of the tool call and its result
	Timestamp time.Time
}
```

Create `internal/debounce/debounce.go`:
```go
package debounce

import (
	"sync"
	"time"
)

// Tracker decides when a session has gone idle long enough to trigger
// summarization, coalescing rapid bursts of activity into one trigger.
type Tracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	fired    map[string]bool
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{
		lastSeen: make(map[string]time.Time),
		fired:    make(map[string]bool),
	}
}

// Touch records activity for a session, resetting its idle clock so it
// can be reported due again after the next idle window elapses.
func (t *Tracker) Touch(sessionID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastSeen[sessionID] = at
	t.fired[sessionID] = false
}

// Due returns the sessions that have been idle for at least idleWindow
// since their last Touch, and marks each returned session as fired so it
// is not returned again until its next Touch.
func (t *Tracker) Due(now time.Time, idleWindow time.Duration) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var due []string
	for sessionID, last := range t.lastSeen {
		if t.fired[sessionID] {
			continue
		}
		if now.Sub(last) >= idleWindow {
			due = append(due, sessionID)
			t.fired[sessionID] = true
		}
	}
	return due
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/debounce/... ./internal/observation/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/observation/observation.go internal/debounce/debounce.go internal/debounce/debounce_test.go
git commit -m "feat: add shared Observation type and idle-detection Tracker"
```

---

### Task 4: Claude Code transcript parser

**Files:**
- Create: `internal/adapter/claudecode/parser.go`
- Create: `internal/adapter/claudecode/testdata/sample.jsonl`
- Test: `internal/adapter/claudecode/parser_test.go`

**Interfaces:**
- Consumes: `observation.Observation` from Task 3
- Produces: `claudecode.NewParser() *Parser`, `(*Parser) Feed(line []byte) (observation.Observation, bool, error)`

Real transcript schema confirmed by reading an actual `~/.claude/projects/**/*.jsonl` file during planning: top-level fields `type` (`"assistant"`|`"user"`|others), `sessionId`, `cwd`, `message.content` (either a plain JSON string for an ordinary text turn, or an array of content blocks). An assistant `tool_use` block has `{"type":"tool_use","id":"...","name":"...","input":{...}}`; a user `tool_result` block has `{"type":"tool_result","tool_use_id":"...","content":"...","is_error":false}` where `content` is either a plain string or an array of `{"type":"text","text":"..."}` blocks.

- [ ] **Step 1: Write the test fixture and the failing tests**

Create `internal/adapter/claudecode/testdata/sample.jsonl` (each line is one JSON object — schema-accurate synthetic data, not real user content):
```
{"type":"assistant","uuid":"a1","timestamp":"2026-08-10T10:00:00.000Z","sessionId":"sess-1","cwd":"/home/minh/personal/memremark","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"a2","timestamp":"2026-08-10T10:00:01.000Z","sessionId":"sess-1","cwd":"/home/minh/personal/memremark","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"README.md\nmain.go","is_error":false}]}}
{"type":"user","uuid":"a3","timestamp":"2026-08-10T10:00:02.000Z","sessionId":"sess-1","cwd":"/home/minh/personal/memremark","message":{"content":"just a plain question"}}
{"type":"assistant","uuid":"a4","timestamp":"2026-08-10T10:00:03.000Z","sessionId":"sess-1","cwd":"/home/minh/personal/memremark","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"README.md"}}]}}
```

Create `internal/adapter/claudecode/parser_test.go`:
```go
package claudecode

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestParserPairsToolUseWithToolResult(t *testing.T) {
	f, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	p := NewParser()
	var count int
	var toolName, sessionID, wingPath, content string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		obs, ok, err := p.Feed(scanner.Bytes())
		if err != nil {
			t.Fatalf("Feed returned error: %v", err)
		}
		if ok {
			count++
			toolName, sessionID, wingPath, content = obs.ToolName, obs.SessionID, obs.WingPath, obs.Content
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly 1 completed observation (the 2nd tool_use has no matching result), got %d", count)
	}
	if toolName != "Bash" {
		t.Fatalf("expected tool name Bash, got %q", toolName)
	}
	if sessionID != "sess-1" {
		t.Fatalf("expected session sess-1, got %q", sessionID)
	}
	if wingPath != "/home/minh/personal/memremark" {
		t.Fatalf("unexpected wing path %q", wingPath)
	}
	if !strings.Contains(content, "README.md") {
		t.Fatalf("expected result content to include README.md, got %q", content)
	}
}

func TestParserSkipsMalformedLinesWithoutError(t *testing.T) {
	p := NewParser()
	_, ok, err := p.Feed([]byte(`{"type":"user","uuid":"a5","message":{`))
	if err != nil {
		t.Fatalf("expected no error for a malformed line, got %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for a malformed line")
	}
}

func TestParserOnEmptyLineReturnsNothing(t *testing.T) {
	p := NewParser()
	_, ok, err := p.Feed([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for an empty line")
	}
}

func TestParserIgnoresPlainTextTurns(t *testing.T) {
	p := NewParser()
	line := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","message":{"content":"hello"}}`
	_, ok, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for a plain text turn")
	}
}

func TestParserLeavesUnmatchedToolUsePending(t *testing.T) {
	p := NewParser()
	line := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp","message":{"content":[{"type":"tool_use","id":"toolu_x","name":"Read","input":{}}]}}`
	_, ok, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false, a lone tool_use should not emit an observation")
	}
	if len(p.pending) != 1 {
		t.Fatalf("expected 1 pending tool_use, got %d", len(p.pending))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/claudecode/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/claudecode/parser.go`:
```go
package claudecode

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/haminh7036/memremark/internal/observation"
)

type rawEvent struct {
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	SessionID string      `json:"sessionId"`
	CWD       string      `json:"cwd"`
	Message   *rawMessage `json:"message"`
}

type rawMessage struct {
	Content json.RawMessage `json:"content"`
}

type rawContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`          // present on tool_use
	Name      string          `json:"name"`         // present on tool_use
	Input     json.RawMessage `json:"input"`         // present on tool_use
	ToolUseID string          `json:"tool_use_id"`  // present on tool_result
	Content   json.RawMessage `json:"content"`       // present on tool_result
	Text      string          `json:"text"`           // present on text blocks
}

type pendingToolUse struct {
	name      string
	input     string
	sessionID string
	cwd       string
	timestamp time.Time
}

// Parser turns a Claude Code transcript's JSONL lines into Observations
// by pairing each tool_use block with its matching tool_result. It is
// stateful across calls to Feed since the two halves of a tool call
// appear on different lines.
type Parser struct {
	pending map[string]pendingToolUse // keyed by tool_use id
}

// NewParser returns an empty Parser.
func NewParser() *Parser {
	return &Parser{pending: make(map[string]pendingToolUse)}
}

// Feed processes one JSONL line. It returns a completed Observation once
// a matching tool_result closes out a previously seen tool_use;
// otherwise ok is false. Malformed or empty lines are skipped
// (ok=false, err=nil) since a mid-write transcript can contain a
// truncated final line.
func (p *Parser) Feed(line []byte) (obs observation.Observation, ok bool, err error) {
	if len(line) == 0 {
		return observation.Observation{}, false, nil
	}

	var ev rawEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return observation.Observation{}, false, nil
	}
	if ev.Message == nil || len(ev.Message.Content) == 0 {
		return observation.Observation{}, false, nil
	}

	ts, _ := time.Parse(time.RFC3339, ev.Timestamp)

	var blocks []rawContentBlock
	if err := json.Unmarshal(ev.Message.Content, &blocks); err != nil {
		// content is a plain string (an ordinary text turn) -- nothing to pair.
		return observation.Observation{}, false, nil
	}

	switch ev.Type {
	case "assistant":
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			p.pending[b.ID] = pendingToolUse{
				name:      b.Name,
				input:     string(b.Input),
				sessionID: ev.SessionID,
				cwd:       ev.CWD,
				timestamp: ts,
			}
		}
		return observation.Observation{}, false, nil

	case "user":
		for _, b := range blocks {
			if b.Type != "tool_result" {
				continue
			}
			pend, found := p.pending[b.ToolUseID]
			if !found {
				continue
			}
			delete(p.pending, b.ToolUseID)
			return observation.Observation{
				WingPath:  pend.cwd,
				SessionID: pend.sessionID,
				ToolName:  pend.name,
				Content:   fmt.Sprintf("input: %s\nresult: %s", pend.input, resultContentText(b.Content)),
				Timestamp: pend.timestamp,
			}, true, nil
		}
		return observation.Observation{}, false, nil
	}

	return observation.Observation{}, false, nil
}

// resultContentText extracts human-readable text from a tool_result's
// content field, which Claude Code emits either as a plain string or as
// an array of {"type":"text","text":"..."} blocks.
func resultContentText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []rawContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var out string
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text
			}
		}
		return out
	}
	return string(raw)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/claudecode/... -v -run TestParser`
Expected: PASS (all 5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/claudecode/parser.go internal/adapter/claudecode/parser_test.go internal/adapter/claudecode/testdata/sample.jsonl
git commit -m "feat: add Claude Code transcript parser"
```

---

### Task 5: Claude Code transcript discovery + tailer

**Files:**
- Create: `internal/adapter/claudecode/tailer.go`
- Test: `internal/adapter/claudecode/tailer_test.go`

**Interfaces:**
- Produces: `claudecode.DiscoverTranscriptFiles(root string) ([]string, error)`, `claudecode.NewTailer() *Tailer`, `(*Tailer) ReadNewLines(path string) ([][]byte, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/claudecode/tailer_test.go`:
```go
package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func linesToStrings(lines [][]byte) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}

func TestReadNewLinesOnlyReturnsAppendedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tailer := NewTailer()
	lines, err := tailer.ReadNewLines(path)
	if err != nil {
		t.Fatalf("ReadNewLines: %v", err)
	}
	if len(lines) != 2 || string(lines[0]) != "line1" || string(lines[1]) != "line2" {
		t.Fatalf("unexpected first read: %v", linesToStrings(lines))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("line3\npartial-lin"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	lines2, err := tailer.ReadNewLines(path)
	if err != nil {
		t.Fatalf("ReadNewLines (2nd): %v", err)
	}
	if len(lines2) != 1 || string(lines2[0]) != "line3" {
		t.Fatalf("expected only the complete new line, got %v", linesToStrings(lines2))
	}

	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("e4\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	lines3, err := tailer.ReadNewLines(path)
	if err != nil {
		t.Fatalf("ReadNewLines (3rd): %v", err)
	}
	if len(lines3) != 1 || string(lines3[0]) != "partial-line4" {
		t.Fatalf("expected the completed partial line, got %v", linesToStrings(lines3))
	}
}

func TestDiscoverTranscriptFilesFindsOnlyJSONL(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mustWrite("proj-a/session1.jsonl", "{}")
	mustWrite("proj-a/session2.jsonl", "{}")
	mustWrite("proj-b/session3.jsonl", "{}")
	mustWrite("proj-b/notes.md", "ignore me")

	files, err := DiscoverTranscriptFiles(root)
	if err != nil {
		t.Fatalf("DiscoverTranscriptFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 .jsonl files, got %d: %v", len(files), files)
	}
}

func TestDiscoverTranscriptFilesOnMissingRootReturnsEmptyNoError(t *testing.T) {
	files, err := DiscoverTranscriptFiles(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error for a missing root, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/claudecode/... -run "TestReadNewLines|TestDiscover"`
Expected: FAIL — `Tailer`/`DiscoverTranscriptFiles` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/claudecode/tailer.go`:
```go
package claudecode

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
)

// DiscoverTranscriptFiles returns every Claude Code transcript file
// (*.jsonl) found under root, typically $HOME/.claude/projects. A
// missing root is not an error -- it simply means no transcripts exist
// yet.
func DiscoverTranscriptFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting the whole walk
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// Tailer reads only the bytes appended to each transcript file since the
// last call for that path, so a poll cycle never re-processes old lines.
type Tailer struct {
	offsets map[string]int64
}

// NewTailer returns a Tailer with no prior read history.
func NewTailer() *Tailer {
	return &Tailer{offsets: make(map[string]int64)}
}

// ReadNewLines returns the complete lines appended to path since the
// last call for that path. An incomplete trailing line (the file is
// still being written) is left unconsumed for the next call.
func (t *Tailer) ReadNewLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	offset := t.offsets[path]
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	var lines [][]byte
	reader := bufio.NewReader(f)
	consumed := offset
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			lines = append(lines, line[:len(line)-1])
			consumed += int64(len(line))
		}
		if err != nil {
			break // EOF, or a partial trailing line with no newline yet
		}
	}
	t.offsets[path] = consumed
	return lines, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/claudecode/... -v`
Expected: PASS (all tests in the package, including Task 4's)

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/claudecode/tailer.go internal/adapter/claudecode/tailer_test.go
git commit -m "feat: add Claude Code transcript discovery and byte-offset tailer"
```

---

### Task 6: Claude Code SessionStart hook binary

**Files:**
- Create: `cmd/memremark-hook-claude-sessionstart/main.go`
- Test: `cmd/memremark-hook-claude-sessionstart/main_test.go`

**Interfaces:**
- Consumes: `storage.Store`, `storage.Drawer`, `storage.HallFact` etc. from Task 2
- Produces: a standalone binary; testable pure functions `buildOutput([]storage.Drawer) hookOutput` and `formatSummaries([]storage.Drawer) string`

This is the confirmed, real Claude Code context-injection mechanism: a `SessionStart` hook prints a JSON object to stdout with a `hookSpecificOutput.additionalContext` field, and Claude Code injects that text into the new session's context.

- [ ] **Step 1: Write the failing tests**

Create `cmd/memremark-hook-claude-sessionstart/main_test.go`:
```go
package main

import (
	"strings"
	"testing"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestFormatSummariesIncludesHallAndContent(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: storage.HallFact, Content: "chose SQLite for v1"},
		{Hall: storage.HallDiscovery, Content: "agy hooks don't execute"},
	}
	out := formatSummaries(summaries)
	if !strings.Contains(out, "chose SQLite for v1") {
		t.Fatalf("expected output to include first summary, got %q", out)
	}
	if !strings.Contains(out, "agy hooks don't execute") {
		t.Fatalf("expected output to include second summary, got %q", out)
	}
}

func TestBuildOutputOmitsHookSpecificOutputWhenNoSummaries(t *testing.T) {
	out := buildOutput(nil)
	if out.HookSpecificOutput != nil {
		t.Fatalf("expected nil HookSpecificOutput for no summaries, got %+v", out.HookSpecificOutput)
	}
}

func TestBuildOutputSetsHookEventNameAndContext(t *testing.T) {
	out := buildOutput([]storage.Drawer{{Hall: storage.HallFact, Content: "x"}})
	if out.HookSpecificOutput == nil {
		t.Fatalf("expected non-nil HookSpecificOutput")
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("expected hookEventName SessionStart, got %q", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "x") {
		t.Fatalf("expected additionalContext to include the summary content, got %q", out.HookSpecificOutput.AdditionalContext)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/memremark-hook-claude-sessionstart/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `cmd/memremark-hook-claude-sessionstart/main.go`:
```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/haminh7036/memremark/internal/storage"
)

type hookOutput struct {
	HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-claude-sessionstart:", err)
		// Never fail the hook's exit code -- a broken memory feature must
		// not block the user from starting a Claude Code session.
	}
	os.Exit(0)
}

func run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing(cwd)
	if err != nil {
		return err
	}
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(buildOutput(summaries))
}

func buildOutput(summaries []storage.Drawer) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		HookSpecificOutput: &hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: formatSummaries(summaries),
		},
	}
}

func formatSummaries(summaries []storage.Drawer) string {
	out := "Bối cảnh từ các phiên làm việc trước (memremark):\n"
	for _, d := range summaries {
		out += fmt.Sprintf("- [%s] %s\n", d.Hall, d.Content)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/memremark-hook-claude-sessionstart/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/memremark-hook-claude-sessionstart/main.go cmd/memremark-hook-claude-sessionstart/main_test.go
git commit -m "feat: add Claude Code SessionStart context-injection hook"
```

*(Wiring this binary into a real `hooks.json` for Claude Code is a manual, environment-specific step, not part of this repo's automated build — document it in the repo's own hook-setup notes once the binary is built and installed on a machine, per spec §5's `SessionStart` mapping.)*

---

### Task 7: Antigravity CLI protobuf heuristic string extraction

**Files:**
- Create: `internal/adapter/antigravity/protoscan.go`
- Test: `internal/adapter/antigravity/protoscan_test.go`

**Interfaces:**
- Produces: `antigravity.ExtractStrings(data []byte) []string`

Confirmed empirically during planning: Antigravity CLI's `steps.step_payload` column is a raw protobuf-encoded blob (verified via hex dump of a real conversation database — readable strings like prompt text are visible embedded in the binary, consistent with protobuf's length-delimited wire encoding). There is no public `.proto` schema. This task recovers embedded text generically, via wire-format scanning, without needing to know field numbers.

- [ ] **Step 1: Add the protobuf wire-format dependency and write the failing tests**

Run:
```bash
go get google.golang.org/protobuf
```

Create `internal/adapter/antigravity/protoscan_test.go`:
```go
package antigravity

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestExtractStringsRecoversTopLevelStringField(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendString(buf, "Append a line 'foo' to sample.txt")

	got := ExtractStrings(buf)
	want := []string{"Append a line 'foo' to sample.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractStringsRecursesIntoNestedMessages(t *testing.T) {
	var inner []byte
	inner = protowire.AppendTag(inner, 1, protowire.BytesType)
	inner = protowire.AppendString(inner, "nested tool name")
	inner = protowire.AppendTag(inner, 2, protowire.VarintType)
	inner = protowire.AppendVarint(inner, 42)

	var outer []byte
	outer = protowire.AppendTag(outer, 3, protowire.BytesType)
	outer = protowire.AppendBytes(outer, inner)

	got := ExtractStrings(outer)
	want := []string{"nested tool name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractStringsIgnoresShortAndBinaryNoise(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendBytes(buf, []byte{0x00, 0x01, 0xff, 0xfe})
	buf = protowire.AppendTag(buf, 2, protowire.BytesType)
	buf = protowire.AppendString(buf, "ok") // shorter than minStringLen

	got := ExtractStrings(buf)
	if len(got) != 0 {
		t.Fatalf("expected no strings extracted from noise, got %v", got)
	}
}

func TestExtractStringsOnEmptyInputReturnsNil(t *testing.T) {
	got := ExtractStrings(nil)
	if len(got) != 0 {
		t.Fatalf("expected no strings for empty input, got %v", got)
	}
}

func TestExtractStringsOnTruncatedInputStopsGracefully(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = append(buf, 0x05) // claims a 5-byte string but provides none -- truncated
	got := ExtractStrings(buf) // must not panic
	if len(got) != 0 {
		t.Fatalf("expected no strings from truncated input, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/antigravity/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/antigravity/protoscan.go`:
```go
package antigravity

import (
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// minStringLen is the shortest byte run treated as a meaningful embedded
// string rather than incidental binary noise.
//
// ponytail: this is a heuristic wire-format scan, not a real decode
// against Antigravity CLI's (undocumented, proprietary) .proto schema --
// it recovers human-readable text embedded in length-delimited fields
// without knowing which field number means what. Upgrade to a real
// field-mapped decoder if structured tool-name/argument separation is
// ever needed; see spec §10.
const minStringLen = 3

// ExtractStrings recovers every printable UTF-8 string embedded in a
// protobuf-encoded blob, by walking the wire format generically (without
// a schema) and recursing into length-delimited fields that aren't
// themselves valid text, since those are likely nested sub-messages.
// Malformed or truncated input causes the scan to stop and return
// whatever was recovered so far, never a panic.
func ExtractStrings(data []byte) []string {
	var out []string
	for len(data) > 0 {
		_, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return out
		}
		data = data[n:]

		switch typ {
		case protowire.VarintType:
			_, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return out
			}
			data = data[n:]
		case protowire.Fixed32Type:
			_, n := protowire.ConsumeFixed32(data)
			if n < 0 {
				return out
			}
			data = data[n:]
		case protowire.Fixed64Type:
			_, n := protowire.ConsumeFixed64(data)
			if n < 0 {
				return out
			}
			data = data[n:]
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return out
			}
			data = data[n:]
			if isMeaningfulText(v) {
				out = append(out, string(v))
			} else {
				out = append(out, ExtractStrings(v)...)
			}
		default:
			return out
		}
	}
	return out
}

func isMeaningfulText(v []byte) bool {
	if len(v) < minStringLen || !utf8.Valid(v) {
		return false
	}
	s := string(v)
	printable := 0
	for _, r := range s {
		if unicode.IsPrint(r) {
			printable++
		}
	}
	return printable == utf8.RuneCountInString(s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/antigravity/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/adapter/antigravity/protoscan.go internal/adapter/antigravity/protoscan_test.go
git commit -m "feat: add heuristic protobuf string extraction for Antigravity CLI transcripts"
```

---

### Task 8: Antigravity CLI conversation discovery + steps reader

**Files:**
- Create: `internal/adapter/antigravity/reader.go`
- Test: `internal/adapter/antigravity/reader_test.go`

**Interfaces:**
- Consumes: `antigravity.ExtractStrings` from Task 7, `observation.Observation` from Task 3
- Produces: `antigravity.ConversationInfo{ID, WorkspaceURIs string, LastModified time.Time}`, `antigravity.ListConversations(summariesDBPath string) ([]ConversationInfo, error)`, `antigravity.ReadObservations(conversationDBPath, wingPath, sessionID string, at time.Time, sinceIdx int64) (obs []observation.Observation, maxIdx int64, err error)`

Real schemas confirmed empirically during planning: `conversation_summaries` table has `conversation_id`, `workspace_uris`, `last_modified_time` (format `"2026-07-17 03:45:48.875161831+00:00"`); `steps` table has `idx INTEGER PRIMARY KEY` and `step_payload BLOB`. `sinceIdx=-1` means "read everything" (idx values start at 0).

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/antigravity/reader_test.go`:
```go
package antigravity

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"google.golang.org/protobuf/encoding/protowire"
)

func createTestConversationDB(t *testing.T, path string, payloads [][]byte) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE steps (
		idx integer, step_type integer NOT NULL DEFAULT 0, status integer NOT NULL DEFAULT 0,
		has_subtrajectory numeric NOT NULL DEFAULT false, metadata blob, error_details blob,
		permissions blob, task_details blob, render_info blob, step_payload blob,
		step_format integer NOT NULL DEFAULT 0, PRIMARY KEY (idx)
	)`)
	if err != nil {
		t.Fatalf("create steps table: %v", err)
	}
	for i, p := range payloads {
		if _, err := db.Exec(`INSERT INTO steps (idx, step_payload) VALUES (?, ?)`, i, p); err != nil {
			t.Fatalf("insert step %d: %v", i, err)
		}
	}
}

func createTestSummariesDB(t *testing.T, path, conversationID, workspaceURIs, lastModified string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id text, workspace_uris text NOT NULL DEFAULT "",
		last_modified_time datetime NOT NULL, PRIMARY KEY (conversation_id)
	)`)
	if err != nil {
		t.Fatalf("create conversation_summaries table: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO conversation_summaries (conversation_id, workspace_uris, last_modified_time) VALUES (?, ?, ?)`,
		conversationID, workspaceURIs, lastModified,
	)
	if err != nil {
		t.Fatalf("insert conversation summary: %v", err)
	}
}

func buildProtobufPromptBlob(text string) []byte {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendString(buf, text)
	return buf
}

func TestReadObservationsExtractsTextFromStepPayloads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation.db")
	createTestConversationDB(t, dbPath, [][]byte{
		buildProtobufPromptBlob("Append a line 'foo' to sample.txt"),
		nil, // a step with no payload must just be skipped
		buildProtobufPromptBlob("second step content"),
	})

	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	obs, maxIdx, err := ReadObservations(dbPath, "/tmp/project", "conv-1", at, -1)
	if err != nil {
		t.Fatalf("ReadObservations: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations (nil payload skipped), got %d", len(obs))
	}
	if maxIdx != 2 {
		t.Fatalf("expected maxIdx 2, got %d", maxIdx)
	}
	if obs[0].Content != "Append a line 'foo' to sample.txt" {
		t.Fatalf("unexpected first observation content: %q", obs[0].Content)
	}
	if obs[0].WingPath != "/tmp/project" || obs[0].SessionID != "conv-1" {
		t.Fatalf("unexpected wing/session on observation: %+v", obs[0])
	}
	if !obs[0].Timestamp.Equal(at) {
		t.Fatalf("expected timestamp %v, got %v", at, obs[0].Timestamp)
	}
}

func TestReadObservationsOnlyReturnsStepsAfterSinceIdx(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation.db")
	createTestConversationDB(t, dbPath, [][]byte{
		buildProtobufPromptBlob("first step"),
		buildProtobufPromptBlob("second step"),
	})

	at := time.Now()
	obs1, maxIdx1, err := ReadObservations(dbPath, "/tmp/project", "conv-1", at, -1)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(obs1) != 2 {
		t.Fatalf("expected 2 observations on first read, got %d", len(obs1))
	}
	if maxIdx1 != 1 {
		t.Fatalf("expected maxIdx 1, got %d", maxIdx1)
	}

	obs2, maxIdx2, err := ReadObservations(dbPath, "/tmp/project", "conv-1", at, maxIdx1)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(obs2) != 0 {
		t.Fatalf("expected no new observations on second read, got %d", len(obs2))
	}
	if maxIdx2 != maxIdx1 {
		t.Fatalf("expected maxIdx unchanged, got %d", maxIdx2)
	}
}

func TestListConversationsParsesRealTimestampFormat(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation_summaries.db")
	createTestSummariesDB(t, dbPath, "conv-1", "/home/minh/personal/memremark", "2026-07-17 03:45:48.875161831+00:00")

	convs, err := ListConversations(dbPath)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].WorkspaceURIs != "/home/minh/personal/memremark" {
		t.Fatalf("unexpected workspace: %q", convs[0].WorkspaceURIs)
	}
	want := time.Date(2026, 7, 17, 3, 45, 48, 875161831, time.UTC)
	if !convs[0].LastModified.Equal(want) {
		t.Fatalf("expected LastModified %v, got %v", want, convs[0].LastModified)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/antigravity/... -run "TestReadObservations|TestListConversations"`
Expected: FAIL — `ReadObservations`/`ListConversations` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/antigravity/reader.go`:
```go
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
	return time.Parse("2006-01-02 15:04:05.999999999-07:00", s)
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
		info.LastModified, _ = parseSQLiteDatetime(lastModified)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/antigravity/... -v`
Expected: PASS (all tests in the package, including Task 7's)

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/antigravity/reader.go internal/adapter/antigravity/reader_test.go
git commit -m "feat: add Antigravity CLI conversation discovery and steps reader"
```

---

### Task 9: Headless summarizer

**Files:**
- Create: `internal/summarizer/summarizer.go`
- Test: `internal/summarizer/summarizer_test.go`

**Interfaces:**
- Consumes: `observation.Observation` from Task 3, `storage.HallFact` etc. from Task 2
- Produces: `summarizer.Invoker` interface, `summarizer.ClaudeCodeInvoker{}`, `summarizer.AntigravityInvoker{}`, `summarizer.SummaryItem{Hall, Content string}`, `summarizer.Summarize(ctx context.Context, invoker Invoker, observations []observation.Observation) ([]SummaryItem, error)`

Real JSON envelope shapes confirmed empirically during planning (`claude -p --output-format json` and `agy -p --output-format json` against the installed binaries): Claude Code returns `{"is_error":false,...,"result":"<text>",...}`; Antigravity CLI returns `{"status":"SUCCESS","response":"<text>",...}` (and `{"status":"ERROR","error":"..."}` on failure).

- [ ] **Step 1: Write the failing tests**

Create `internal/summarizer/summarizer_test.go`:
```go
package summarizer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/haminh7036/memremark/internal/observation"
)

type stubInvoker struct {
	reply string
	err   error
}

func (s stubInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	return s.reply, s.err
}

func TestSummarizeParsesValidJSONReply(t *testing.T) {
	stub := stubInvoker{reply: "Here you go:\n" +
		`[{"hall":"fact","content":"chose SQLite for v1"},{"hall":"discovery","content":"agy hooks don't execute"}]` +
		"\nHope that helps!"}

	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	if items[0].Hall != "fact" || items[0].Content != "chose SQLite for v1" {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
}

func TestSummarizeOnEmptyObservationsSkipsInvocation(t *testing.T) {
	stub := stubInvoker{err: errors.New("should not be called")}
	items, err := Summarize(context.Background(), stub, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if items != nil {
		t.Fatalf("expected nil items, got %v", items)
	}
}

func TestSummarizeReturnsErrorWhenInvokerFails(t *testing.T) {
	stub := stubInvoker{err: errors.New("network down")}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	if _, err := Summarize(context.Background(), stub, obs); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestSummarizeRejectsInvalidHall(t *testing.T) {
	stub := stubInvoker{reply: `[{"hall":"not-a-real-hall","content":"x"}]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	if _, err := Summarize(context.Background(), stub, obs); err == nil {
		t.Fatalf("expected error for invalid hall, got nil")
	}
}

func TestSummarizeReturnsEmptyForEmptyJSONArray(t *testing.T) {
	stub := stubInvoker{reply: `[]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestBuildPromptIncludesObservationContent(t *testing.T) {
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls -la"}}
	prompt := buildPrompt(obs)
	if !strings.Contains(prompt, "ls -la") {
		t.Fatalf("expected prompt to include observation content, got %q", prompt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/summarizer/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/summarizer/summarizer.go`:
```go
package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

// Invoker runs one headless prompt through a CLI's own non-interactive
// mode and returns the model's plain-text reply, already unwrapped from
// that CLI's own JSON envelope. Concrete implementations shell out to
// `claude -p` or `agy -p`; tests use a stub.
type Invoker interface {
	Invoke(ctx context.Context, prompt string) (string, error)
}

type claudeCodeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// ClaudeCodeInvoker runs prompts through `claude -p --output-format json`.
type ClaudeCodeInvoker struct{}

// Invoke implements Invoker.
func (ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	out, err := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "json").Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: claude -p failed: %w", err)
	}
	var res claudeCodeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse claude -p output: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("summarizer: claude -p reported an error result")
	}
	return res.Result, nil
}

type antigravityResult struct {
	Status   string `json:"status"`
	Response string `json:"response"`
	Error    string `json:"error"`
}

// AntigravityInvoker runs prompts through `agy -p --output-format json`.
type AntigravityInvoker struct{}

// Invoke implements Invoker.
func (AntigravityInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	out, err := exec.CommandContext(ctx, "agy", "-p", prompt, "--output-format", "json").Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: agy -p failed: %w", err)
	}
	var res antigravityResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse agy -p output: %w", err)
	}
	if res.Status != "SUCCESS" {
		return "", fmt.Errorf("summarizer: agy -p returned status %q: %s", res.Status, res.Error)
	}
	return res.Response, nil
}

// SummaryItem is one distilled piece of knowledge the model extracted
// from a batch of verbatim observations.
type SummaryItem struct {
	Hall    string `json:"hall"`
	Content string `json:"content"`
}

// Summarize asks invoker to distill observations into hall-classified
// SummaryItems. It returns (nil, nil) without invoking anything if
// observations is empty.
func Summarize(ctx context.Context, invoker Invoker, observations []observation.Observation) ([]SummaryItem, error) {
	if len(observations) == 0 {
		return nil, nil
	}
	text, err := invoker.Invoke(ctx, buildPrompt(observations))
	if err != nil {
		return nil, err
	}
	return parseSummaryItems(text)
}

func buildPrompt(observations []observation.Observation) string {
	var sb strings.Builder
	sb.WriteString("Dưới đây là các quan sát thô (tool call) từ một phiên làm việc. ")
	sb.WriteString("Đúc kết chúng thành các mục tri thức ngắn gọn, mỗi mục thuộc 1 trong 4 loại: ")
	sb.WriteString("fact (quyết định đã chốt), discovery (phát hiện mới), preference (thói quen/sở thích), advice (khuyến nghị/giải pháp). ")
	sb.WriteString(`Trả lời DUY NHẤT bằng 1 JSON array, mỗi phần tử có dạng {"hall":"...","content":"..."}. `)
	sb.WriteString("Nếu không có gì đáng đúc kết, trả về [].\n\nQuan sát:\n")
	for _, o := range observations {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", o.ToolName, o.Content))
	}
	return sb.String()
}

func parseSummaryItems(modelText string) ([]SummaryItem, error) {
	text := strings.TrimSpace(modelText)
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("summarizer: no JSON array found in model reply: %q", truncate(text, 200))
	}

	var items []SummaryItem
	if err := json.Unmarshal([]byte(text[start:end+1]), &items); err != nil {
		return nil, fmt.Errorf("summarizer: parse model reply as JSON: %w", err)
	}
	for _, it := range items {
		if !isValidHallForSummarizer(it.Hall) {
			return nil, fmt.Errorf("summarizer: model returned invalid hall %q", it.Hall)
		}
	}
	return items, nil
}

func isValidHallForSummarizer(hall string) bool {
	switch hall {
	case storage.HallFact, storage.HallDiscovery, storage.HallPreference, storage.HallAdvice:
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/summarizer/... -v`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/summarizer.go internal/summarizer/summarizer_test.go
git commit -m "feat: add headless summarizer with Claude Code and Antigravity CLI invokers"
```

---

### Task 10: Daemon poll loop + memremarkd entrypoint

**Files:**
- Create: `internal/daemon/daemon.go`
- Test: `internal/daemon/daemon_test.go`
- Create: `cmd/memremarkd/main.go`

**Interfaces:**
- Consumes: everything from Tasks 1-9
- Produces: `daemon.New(store *storage.Store, claudeProjectsRoot, antigravitySummariesDB string, claudeInvoker, antigravityInvoker summarizer.Invoker) *Daemon`, `(*Daemon) PollOnce(ctx context.Context, now time.Time) error`

This is the end-to-end smoke test the spec requires (§9): a fake Claude Code transcript is captured as verbatim, and once its session goes idle, summarized via a stubbed invoker — all in one poll loop, with no real CLI or network calls.

- [ ] **Step 1: Write the failing tests**

Create `internal/daemon/daemon_test.go`:
```go
package daemon

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/haminh7036/memremark/internal/storage"
)

type stubInvoker struct{ reply string }

func (s stubInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	return s.reply, nil
}

func mustOpenSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	return db
}

func createEmptySummariesDB(t *testing.T, path string) {
	t.Helper()
	db := mustOpenSQLite(t, path)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id text, workspace_uris text NOT NULL DEFAULT "",
		last_modified_time datetime NOT NULL, PRIMARY KEY (conversation_id)
	)`)
	if err != nil {
		t.Fatalf("create conversation_summaries: %v", err)
	}
}

func writeSampleTranscript(t *testing.T, projectsRoot string) {
	t.Helper()
	projectDir := filepath.Join(projectsRoot, "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcript := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp/project","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","sessionId":"sess-1","cwd":"/tmp/project","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"README.md","is_error":false}]}}
`
	if err := os.WriteFile(filepath.Join(projectDir, "sess-1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestPollOnceCapturesVerbatimFromClaudeCodeTranscript(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects")
	writeSampleTranscript(t, projectsRoot)

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	summariesDB := filepath.Join(dir, "antigravity", "conversation_summaries.db")
	if err := os.MkdirAll(filepath.Dir(summariesDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	createEmptySummariesDB(t, summariesDB)

	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	verbatim, err := store.VerbatimSince(wingID, "sess-1", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(verbatim) != 1 {
		t.Fatalf("expected 1 verbatim drawer, got %d", len(verbatim))
	}
	if verbatim[0].ToolName != "Bash" {
		t.Fatalf("expected tool name Bash, got %q", verbatim[0].ToolName)
	}
}

func TestPollOnceSummarizesAfterSessionGoesIdle(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects")
	writeSampleTranscript(t, projectsRoot)

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	summariesDB := filepath.Join(dir, "antigravity", "conversation_summaries.db")
	if err := os.MkdirAll(filepath.Dir(summariesDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	createEmptySummariesDB(t, summariesDB)

	reply := `[{"hall":"fact","content":"listed the project files"}]`
	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: reply}, stubInvoker{reply: "[]"})

	base := time.Now()
	if err := d.PollOnce(context.Background(), base); err != nil {
		t.Fatalf("first PollOnce: %v", err)
	}
	if err := d.PollOnce(context.Background(), base.Add(10*time.Second)); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary drawer, got %d", len(summaries))
	}
	if summaries[0].Content != "listed the project files" {
		t.Fatalf("unexpected summary content: %q", summaries[0].Content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/...`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

Create `internal/daemon/daemon.go`:
```go
package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/antigravity"
	"github.com/haminh7036/memremark/internal/adapter/claudecode"
	"github.com/haminh7036/memremark/internal/debounce"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// idleWindow is how long a session must go quiet before it's summarized.
//
// ponytail: fixed and untuned -- see spec §10. Adjust based on real usage
// once this is running day to day.
const idleWindow = 5 * time.Second

// Daemon ties transcript reading, storage, debounce, and summarization
// together into one repeatable poll cycle.
type Daemon struct {
	Store   *storage.Store
	Tracker *debounce.Tracker

	claudeProjectsRoot string
	claudeTailer       *claudecode.Tailer
	claudeParsers      map[string]*claudecode.Parser

	antigravitySummariesDB string
	antigravityLastIdx     map[string]int64

	sessionWing    map[string]int64
	sessionInvoker map[string]summarizer.Invoker

	claudeInvoker      summarizer.Invoker
	antigravityInvoker summarizer.Invoker
}

// New builds a Daemon ready to poll. claudeProjectsRoot is typically
// $HOME/.claude/projects; antigravitySummariesDB is typically
// $HOME/.gemini/antigravity-cli/conversation_summaries.db.
func New(store *storage.Store, claudeProjectsRoot, antigravitySummariesDB string, claudeInvoker, antigravityInvoker summarizer.Invoker) *Daemon {
	return &Daemon{
		Store:                  store,
		Tracker:                debounce.NewTracker(),
		claudeProjectsRoot:     claudeProjectsRoot,
		claudeTailer:           claudecode.NewTailer(),
		claudeParsers:          make(map[string]*claudecode.Parser),
		antigravitySummariesDB: antigravitySummariesDB,
		antigravityLastIdx:     make(map[string]int64),
		sessionWing:            make(map[string]int64),
		sessionInvoker:         make(map[string]summarizer.Invoker),
		claudeInvoker:          claudeInvoker,
		antigravityInvoker:     antigravityInvoker,
	}
}

// PollOnce runs one capture pass over both CLIs' transcripts, then
// triggers summarization for any session that has gone idle.
func (d *Daemon) PollOnce(ctx context.Context, now time.Time) error {
	if err := d.pollClaudeCode(now); err != nil {
		log.Printf("daemon: claude code poll error: %v", err)
	}
	if err := d.pollAntigravity(now); err != nil {
		log.Printf("daemon: antigravity poll error: %v", err)
	}
	for _, sessionID := range d.Tracker.Due(now, idleWindow) {
		if err := d.summarizeSession(ctx, sessionID, now); err != nil {
			log.Printf("daemon: summarize session %s failed: %v", sessionID, err)
		}
	}
	return nil
}

func (d *Daemon) pollClaudeCode(now time.Time) error {
	files, err := claudecode.DiscoverTranscriptFiles(d.claudeProjectsRoot)
	if err != nil {
		return err
	}
	for _, file := range files {
		parser, ok := d.claudeParsers[file]
		if !ok {
			parser = claudecode.NewParser()
			d.claudeParsers[file] = parser
		}
		lines, err := d.claudeTailer.ReadNewLines(file)
		if err != nil {
			log.Printf("daemon: read %s: %v", file, err)
			continue
		}
		for _, line := range lines {
			obs, ok, err := parser.Feed(line)
			if err != nil {
				log.Printf("daemon: parse %s: %v", file, err)
				continue
			}
			if !ok {
				continue
			}
			if err := d.recordObservation(obs, d.claudeInvoker, now); err != nil {
				log.Printf("daemon: record observation: %v", err)
			}
		}
	}
	return nil
}

func (d *Daemon) pollAntigravity(now time.Time) error {
	convs, err := antigravity.ListConversations(d.antigravitySummariesDB)
	if err != nil {
		return err
	}
	for _, conv := range convs {
		if conv.WorkspaceURIs == "" {
			continue
		}
		dbPath := filepath.Join(filepath.Dir(d.antigravitySummariesDB), "conversations", conv.ID+".db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		sinceIdx, ok := d.antigravityLastIdx[conv.ID]
		if !ok {
			sinceIdx = -1
		}
		obs, maxIdx, err := antigravity.ReadObservations(dbPath, conv.WorkspaceURIs, conv.ID, conv.LastModified, sinceIdx)
		if err != nil {
			log.Printf("daemon: read antigravity conversation %s: %v", conv.ID, err)
			continue
		}
		d.antigravityLastIdx[conv.ID] = maxIdx
		for _, o := range obs {
			if err := d.recordObservation(o, d.antigravityInvoker, now); err != nil {
				log.Printf("daemon: record observation: %v", err)
			}
		}
	}
	return nil
}

func (d *Daemon) recordObservation(obs observation.Observation, invoker summarizer.Invoker, now time.Time) error {
	wingID, err := d.Store.GetOrCreateWing(obs.WingPath)
	if err != nil {
		return err
	}
	if err := d.Store.InsertVerbatimDrawer(wingID, obs.SessionID, obs.ToolName, obs.Content, obs.Timestamp); err != nil {
		return err
	}
	d.sessionWing[obs.SessionID] = wingID
	d.sessionInvoker[obs.SessionID] = invoker
	d.Tracker.Touch(obs.SessionID, now)
	return nil
}

func (d *Daemon) summarizeSession(ctx context.Context, sessionID string, now time.Time) error {
	wingID, ok := d.sessionWing[sessionID]
	if !ok {
		return nil // never recorded an observation for this session; nothing to summarize
	}
	invoker := d.sessionInvoker[sessionID]

	since, hasPrev, err := d.Store.LastSummaryTime(wingID, sessionID)
	if err != nil {
		return err
	}
	if !hasPrev {
		since = time.Unix(0, 0)
	}

	verbatim, err := d.Store.VerbatimSince(wingID, sessionID, since)
	if err != nil {
		return err
	}
	if len(verbatim) == 0 {
		return nil
	}

	var obs []observation.Observation
	for _, v := range verbatim {
		obs = append(obs, observation.Observation{ToolName: v.ToolName, Content: v.Content})
	}

	items, err := summarizer.Summarize(ctx, invoker, obs)
	if err != nil {
		return err
	}

	coversFrom := verbatim[0].CreatedAt
	coversTo := verbatim[len(verbatim)-1].CreatedAt
	for _, item := range items {
		if err := d.Store.InsertSummaryDrawer(wingID, sessionID, item.Hall, item.Content, coversFrom, coversTo, now); err != nil {
			return err
		}
	}
	return nil
}
```

Create `cmd/memremarkd/main.go`:
```go
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("memremarkd: resolve home dir: %v", err)
	}

	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		log.Fatalf("memremarkd: open storage: %v", err)
	}
	defer store.Close()

	claudeProjectsRoot := filepath.Join(home, ".claude", "projects")
	antigravitySummariesDB := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")

	d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB,
		summarizer.ClaudeCodeInvoker{}, summarizer.AntigravityInvoker{})

	ctx := context.Background()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Println("memremarkd: started, polling every 3s")
	for range ticker.C {
		if err := d.PollOnce(ctx, time.Now()); err != nil {
			log.Printf("memremarkd: poll error: %v", err)
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go build ./...
go test ./... -v
```
Expected: `go build` succeeds for both `cmd/memremarkd` and `cmd/memremark-hook-claude-sessionstart`; all tests across every package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/daemon_test.go cmd/memremarkd/main.go
git commit -m "feat: wire daemon poll loop and add memremarkd entrypoint"
```

---

## What This Plan Deliberately Does Not Cover

- **Daemon process supervision.** Spec §8 calls for the daemon to be "restarted idempotently the next time anything triggers it (a hook, if working, or a lightweight launcher check on CLI startup)." This plan builds `memremarkd` as a plain foreground process (Task 10) with no auto-start-on-hook or crash-restart logic — matching claude-mem's `SessionStart`-spawns-worker-service pattern is real additional work (idempotent "start if not running" semantics, race-safety when multiple hooks fire concurrently). For now, run it manually or via an OS-level supervisor (e.g. a systemd user service); wiring auto-start into Task 6's hook binary is a reasonable follow-up once the daemon itself is proven out.
- **Sync Layer** (multi-device propagation) — separate spec and plan, per the project's own build-order decision.
- **Antigravity CLI context injection** — no confirmed delivery mechanism exists yet (spec §10); needs a research spike (does Antigravity have any equivalent of `hookSpecificOutput`? does *any* hook type actually execute, or is the whole command-hook engine broken in this environment?) before a task can be written without placeholders.
- **Full protobuf schema mapping for Antigravity's `steps` table** — Task 7's heuristic string extraction is the deliberate v1 substitute; upgrading to a real field-mapped decoder is future work if structured tool-name/argument separation becomes necessary.
- **Semantic/vector search, `room` auto-detection, `tunnels`** — explicitly out of scope per spec §3.
