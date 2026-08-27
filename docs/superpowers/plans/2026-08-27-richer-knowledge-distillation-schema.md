# Richer Knowledge Distillation Schema Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement richer knowledge distillation by adding a `narrative` column to summary drawers for deeper context and creating a `session_digests` table with automated and on-demand macro session synthesis.

**Architecture:** Extend SQLite storage schema with `drawers.narrative` and new table `session_digests`. Update summarizer prompts and API schemas (Gemini, Anthropic, CLI invokers) to extract both `content` (terse bullets) and `narrative` (rich prose). Wire daemon debounce lifecycle to synthesize structured session digests into SQLite, and expose through MCP tools (`get_session_digest`, `search_memory`) and Web UI without adding token overhead to session start hooks.

**Tech Stack:** Go 1.22+, modernc.org/sqlite, standard library `net/http`, `context`, `encoding/json`.

## Global Constraints

- Safe SQLite migrations: idempotent table inspection and alterations via `PRAGMA table_info`.
- Preserve hook context zero-overhead invariant: `hookctx.FormatSummaries` must ONLY use `d.Content`.
- All database operations must respect `PRAGMA busy_timeout = 5000;`.
- Standard library and existing dependencies only (no new third-party packages).
- Strict TDD: failing test -> implementation -> pass -> commit.

---

### Task 1: Storage Layer - Migration, `drawers.narrative`, and `session_digests` Table

**Files:**
- Create: `internal/storage/digests.go`
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/drawers.go`
- Modify: `internal/storage/drawers_test.go`
- Create: `internal/storage/digests_test.go`

**Interfaces:**
- Produces:
  - `type SessionDigest struct { ID, WingID int64; SessionID, Request, Investigated, Learned, Completed, NextSteps, Notes string; CreatedAt time.Time }`
  - `func (s *Store) InsertSummaryDrawer(wingID int64, sessionID, hall, content, narrative string, coversFrom, coversTo, createdAt time.Time) error`
  - `func (s *Store) UpsertSessionDigest(digest SessionDigest) error`
  - `func (s *Store) GetSessionDigest(sessionID string) (*SessionDigest, error)`
  - `func (s *Store) ListSessionDigests(wingID int64, limit int) ([]SessionDigest, error)`
  - `func (s *Store) GetDrawersBySession(wingID int64, sessionID string, drawerType string) ([]Drawer, error)`

- [ ] **Step 1: Write failing tests in `internal/storage/digests_test.go` and update `drawers_test.go`**

```go
package storage_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestSessionDigest_UpsertAndGet(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, err := store.GetOrCreateWing("/home/user/proj")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	digest := storage.SessionDigest{
		WingID:       wingID,
		SessionID:    "sess-123",
		Request:      "Add search feature",
		Investigated: "Checked SQLite FTS5",
		Learned:      "FTS5 is fast",
		Completed:    "Added search query",
		NextSteps:    "Add benchmarks",
		Notes:        "Keep schema clean",
		CreatedAt:    time.Now().Truncate(time.Second),
	}

	if err := store.UpsertSessionDigest(digest); err != nil {
		t.Fatalf("UpsertSessionDigest failed: %v", err)
	}

	got, err := store.GetSessionDigest("sess-123")
	if err != nil {
		t.Fatalf("GetSessionDigest failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected digest, got nil")
	}
	if got.Request != digest.Request || got.Completed != digest.Completed {
		t.Errorf("mismatched digest fields: %+v vs %+v", got, digest)
	}

	// Update existing digest
	digest.Completed = "Added search query and benchmarks"
	if err := store.UpsertSessionDigest(digest); err != nil {
		t.Fatalf("UpsertSessionDigest update failed: %v", err)
	}
	got2, err := store.GetSessionDigest("sess-123")
	if err != nil || got2.Completed != "Added search query and benchmarks" {
		t.Fatalf("failed to update digest: %v", got2)
	}
}

func TestDrawer_NarrativePersistence(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, _ := store.GetOrCreateWing("/home/user/proj")
	now := time.Now().Truncate(time.Second)

	err := store.InsertSummaryDrawer(wingID, "sess-1", storage.HallFact, "Bullet point fact", "Detailed narrative explaining the fact context", now, now, now)
	if err != nil {
		t.Fatalf("InsertSummaryDrawer failed: %v", err)
	}

	drawers, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries failed: %v", err)
	}
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
	if drawers[0].Narrative != "Detailed narrative explaining the fact context" {
		t.Errorf("expected narrative, got %q", drawers[0].Narrative)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/storage -run "TestSessionDigest|TestDrawer_NarrativePersistence"`
Expected: FAIL (types and methods undefined)

- [ ] **Step 3: Update `schema` and implement migration in `internal/storage/storage.go`, update `drawers.go`, and create `digests.go`**

In `internal/storage/storage.go`:
- Add `narrative TEXT` to `drawers` table definition.
- Add `CREATE TABLE IF NOT EXISTS session_digests ...` to `schema`.
- Add migration helper `migrateNarrativeColumn(db *sql.DB)` in `Open`.

In `internal/storage/drawers.go`:
- Update `Drawer` struct to include `Narrative string`.
- Update `InsertSummaryDrawer` signature to accept `narrative string`.
- Update `scanDrawerRows` and SQL queries to select `narrative`.
- Implement `GetDrawersBySession(wingID int64, sessionID string, drawerType string) ([]Drawer, error)`.

In `internal/storage/digests.go`:
- Implement `UpsertSessionDigest`, `GetSessionDigest`, and `ListSessionDigests`.

- [ ] **Step 4: Fix existing callers and run tests**

Run: `go test -v ./internal/storage/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/
git commit -m "feat(storage): add narrative column to drawers and create session_digests table"
```

---

### Task 2: Summarizer Layer - Structured Distillation & Session Digest Synthesis

**Files:**
- Modify: `internal/summarizer/summarizer.go`
- Modify: `internal/summarizer/gemini.go`
- Modify: `internal/summarizer/anthropic.go`
- Create: `internal/summarizer/digest.go`
- Modify: `internal/summarizer/summarizer_test.go`
- Create: `internal/summarizer/digest_test.go`

**Interfaces:**
- Consumes: `storage.Drawer`, `locale.TargetLanguage`
- Produces:
  - `type SummaryItem struct { Hall, Content, Narrative string }`
  - `type SessionDigestResult struct { Request, Investigated, Learned, Completed, NextSteps, Notes string }`
  - `func SynthesizeSessionDigest(ctx context.Context, invoker Invoker, summaries []storage.Drawer, fallbackVerbatim []storage.Drawer, lang locale.TargetLanguage, opts ...InvokerOptions) (*SessionDigestResult, error)`

- [ ] **Step 1: Write unit tests in `internal/summarizer/digest_test.go` and update `summarizer_test.go`**

```go
package summarizer_test

import (
	"context"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

type stubDigestInvoker struct {
	response string
}

func (s *stubDigestInvoker) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	return s.response, nil
}

func TestSynthesizeSessionDigest(t *testing.T) {
	jsonReply := `{
		"request": "Implement SQLite caching",
		"investigated": "Looked into memory vs disk trade-offs",
		"learned": "Busy timeout avoids locking errors",
		"completed": "Added timeout and WAL mode",
		"next_steps": "Write benchmark tests",
		"notes": "Ensure clean shutdown"
	}`

	invoker := &stubDigestInvoker{response: jsonReply}
	summaries := []storage.Drawer{
		{
			Hall:      "fact",
			Content:   "SQLite requires busy_timeout",
			Narrative: "Setting busy_timeout prevents SQLITE_BUSY error under concurrency",
			CreatedAt: time.Now(),
		},
	}

	result, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		summaries,
		nil,
		locale.TargetLanguage{Code: "en", Name: "English"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Request != "Implement SQLite caching" || result.Completed != "Added timeout and WAL mode" {
		t.Errorf("unexpected digest result: %+v", result)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/summarizer -run TestSynthesizeSessionDigest`
Expected: FAIL (function undefined)

- [ ] **Step 3: Implement `SummaryItem` with narrative and `SynthesizeSessionDigest`**

In `internal/summarizer/summarizer.go`:
- Update `SummaryItem` struct with `Narrative string json:"narrative,omitempty"`.
- Update `buildPrompt` to guide LLM for both `content` (bullet point) and `narrative` (detailed context).

In `internal/summarizer/gemini.go` and `internal/summarizer/anthropic.go`:
- Add `narrative` property to JSON Schemas.

In `internal/summarizer/digest.go`:
- Implement `SynthesizeSessionDigest` building prompt and parsing JSON object `SessionDigestResult`.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/summarizer/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/
git commit -m "feat(summarizer): add narrative to summary items and implement SynthesizeSessionDigest"
```

---

### Task 3: Daemon Pipeline - Debounced Session Digest Generation

**Files:**
- Modify: `internal/daemon/daemon_summarize.go`
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/daemon_test.go`
- Modify: `internal/daemon/daemon_summarize_test.go`

**Interfaces:**
- Consumes: `storage.Store`, `summarizer.SynthesizeSessionDigest`
- Produces: `func (d *Daemon) synthesizeSessionDigest(ctx context.Context, sessionID string, wingID int64, invoker summarizer.Invoker, now time.Time) error`

- [ ] **Step 1: Write daemon test for automatic session digest creation**

In `internal/daemon/daemon_summarize_test.go`:
```go
func TestDaemon_SummarizeSession_CreatesSessionDigest(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, _ := store.GetOrCreateWing("/home/user/proj")
	sessionID := "test-session-digest"

	digestJSON := `{"request":"Fix bug","investigated":"Logs","learned":"Root cause found","completed":"Patched logic","next_steps":"Deploy","notes":""}`
	summaryJSON := `[{"hall":"fact","content":"Bug is fixed","narrative":"Fixed bug by checking null pointers"}]`

	invoker := &dualStubInvoker{
		summaryJSON: summaryJSON,
		digestJSON:  digestJSON,
	}

	d := New(store, t.TempDir(), "", invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})
	d.sessionWing[sessionID] = wingID
	d.sessionInvoker[sessionID] = invoker

	now := time.Now()
	_ = store.InsertVerbatimDrawer(wingID, sessionID, "edit", "fixed nil pointer", now)

	if err := d.summarizeSession(context.Background(), sessionID, now); err != nil {
		t.Fatalf("summarizeSession failed: %v", err)
	}

	digest, err := store.GetSessionDigest(sessionID)
	if err != nil {
		t.Fatalf("GetSessionDigest failed: %v", err)
	}
	if digest == nil {
		t.Fatalf("expected session digest to be created, got nil")
	}
	if digest.Request != "Fix bug" || digest.Completed != "Patched logic" {
		t.Errorf("unexpected digest fields: %+v", digest)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/daemon -run TestDaemon_SummarizeSession_CreatesSessionDigest`
Expected: FAIL

- [ ] **Step 3: Implement digest synthesis in `internal/daemon/daemon_summarize.go`**

Add `synthesizeSessionDigest` step at the conclusion of `summarizeSessionWithBatchSize`.

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/daemon/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "feat(daemon): trigger session digest synthesis after debounced batch summarization"
```

---

### Task 4: Hook Context Invariant & MCP Server Tools

**Files:**
- Modify: `internal/hookctx/hookctx.go`
- Modify: `internal/hookctx/hookctx_test.go`
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/server_test.go`

**Interfaces:**
- Produces:
  - MCP Tool `get_session_digest` (`session_id`, `wing_path`, `regenerate`)
  - Updated MCP Tool `search_memory` including `narrative`
  - Invariant: `hookctx.FormatSummaries` output format unchanged.

- [ ] **Step 1: Write test for hookctx invariant and new MCP tools**

In `internal/hookctx/hookctx_test.go`:
- Verify `FormatSummaries` outputs `- [fact] Content only` even when `Narrative` is populated.

In `internal/mcp/server_test.go`:
- Test calling `tools/call` for `get_session_digest` and `search_memory`.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/mcp ./internal/hookctx`
Expected: FAIL on missing `get_session_digest` tool

- [ ] **Step 3: Implement MCP tool handlers and schemas**

In `internal/mcp/server.go`:
- Register `get_session_digest` in `handleToolsList`.
- Implement `handleGetSessionDigest` in `handleToolsCall`.
- Enhance `search_memory` result serialization with `narrative`.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/mcp/... ./internal/hookctx/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/ internal/hookctx/
git commit -m "feat(mcp): add get_session_digest tool and surface narrative in search_memory"
```

---

### Task 5: Web UI API Endpoints & Frontend Visuals

**Files:**
- Modify: `internal/ui/server.go`
- Modify: `internal/ui/server_test.go`
- Modify: `web/src/` (or UI components if applicable)

**Interfaces:**
- Produces:
  - `GET /api/wings/{id}/digests`
  - `GET /api/sessions/{sessionId}/digest`

- [ ] **Step 1: Write tests for new UI API endpoints**

In `internal/ui/server_test.go`:
- Test `GET /api/wings/1/digests` returns 200 OK and list of digests.
- Test `GET /api/sessions/sess-123/digest` returns 200 OK and digest details.

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/ui -run "TestDigestsAPI"`
Expected: FAIL (404 Not Found)

- [ ] **Step 3: Implement endpoints in `internal/ui/server.go`**

Wire routes and query store methods `ListSessionDigests` and `GetSessionDigest`.

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/ui/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): add API endpoints for session digests and narrative rendering"
```

---

### Task 6: Full Verification, Clean Build & Closing Issue #3

**Files:**
- Verify: Full repository build and race detector

- [ ] **Step 1: Run complete test suite and race detector**

```bash
go build ./...
go test -v -race ./...
```

- [ ] **Step 2: Update/Close GitHub Issue #3**

```bash
gh issue close 3 --comment "Completed in implementation: narrative field added to drawers, session_digests table created with automated debounced synthesis, MCP get_session_digest tool added, and Web UI endpoints exposed."
```

- [ ] **Step 3: Commit and tag if appropriate**

```bash
git commit --allow-empty -m "chore: complete richer knowledge distillation schema implementation"
```
