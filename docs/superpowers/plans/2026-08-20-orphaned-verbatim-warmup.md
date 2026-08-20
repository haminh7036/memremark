# Orphaned Verbatim Warmup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix verbatim rows that never get summarized/pruned because a session finished (and the daemon restarted) before its idle-debounce fired, by seeding in-memory session tracking from the DB at daemon startup.

**Architecture:** `Daemon.Tracker` (lastSeen/fired), `Daemon.sessionWing`, and `Daemon.sessionInvoker` are pure in-memory maps populated only by `recordObservation` on new transcript activity. Transcript byte-offsets, by contrast, ARE persisted (`poll_state` table), so a session whose transcript stopped growing before a daemon restart never produces a new observation to re-arm its debounce clock — its verbatim rows are silently orphaned forever, even though the prune-on-summarize logic itself (`summarizeSessionWithBatchSize`) is correct. Fix: add `Store.OrphanedVerbatimSessions()` to find every `(wing_id, session_id)` pair that still has verbatim rows, and `Daemon.Warmup()` to seed any of them not already tracked in-memory with `Tracker.Touch(sessionID, time.Unix(0,0))` (epoch — guarantees `Due()` fires on the very first check) so the next `PollOnce` tick summarizes and prunes them normally. `Warmup()` defaults the recovered invoker to `d.claudeInvoker`; this is safe (not a guess) because `resolveInvokers` in `cmd/memremarkd/main.go` always constructs `ClaudeInvoker` and `AntigravityInvoker` as either the same value (single CLI installed) or each other's `FallbackInvoker.Fallback` (both installed) — there is no case where they diverge in a way that would make the recovered session's summarization actually fail.

**Tech Stack:** Go (standard library only: `database/sql`, `time`, `testing`; existing `internal/storage`, `internal/debounce`, `internal/summarizer` packages).

**Spec:** No separate spec doc — this is a production bug fix. Root cause was diagnosed empirically in-conversation: `~/.memremark/memremark.db` still held 89.6MB / 21,828 verbatim rows across 220 sessions after the v0.1.4 prune-verbatim feature was deployed and running; many affected sessions have `MIN(created_at) == MAX(created_at)` (a single burst, then never touched again), matching the in-memory-tracking-lost-on-restart theory exactly.

## Global Constraints

- No new external dependencies — Go stdlib + existing internal packages only.
- Every non-trivial change ships with a regression test (existing project convention — see every `internal/daemon/*_test.go` and `internal/storage/*_test.go` file).
- Must not alter behavior for sessions that ARE actively tracked in-memory (no clobbering a live session's debounce clock with the epoch seed value).
- Must not lose data: a recovered session's verbatim rows are only ever deleted after a successful `Summarize` call, exactly like the existing live path (`summarizeSessionWithBatchSize` already guarantees this — `Warmup()` must not bypass it).
- Follow existing code patterns: `storage.Store` methods return `([]T, error)` and wrap errors with `fmt.Errorf("storage: ...: %w", err)`; `daemon` tests use `storage.Open(tempDBPath(t))` / `stubInvoker` from `internal/daemon/daemon_test.go` and `New(store, claudeProjectsRoot, antigravitySummariesDB, claudeInvoker, antigravityInvoker)`.

---

### Task 1: `Store.OrphanedVerbatimSessions`

**Files:**
- Modify: `internal/storage/drawers.go`
- Test: `internal/storage/drawers_test.go`

**Interfaces:**
- Consumes: existing `drawers` table (`wing_id`, `session_id`, `type` columns — see `internal/storage/storage.go`'s `CREATE TABLE drawers`).
- Produces:
  ```go
  type SessionRef struct {
      WingID    int64
      SessionID string
  }
  func (s *Store) OrphanedVerbatimSessions() ([]SessionRef, error)
  ```
  Later tasks (Task 2) call this and iterate the returned `[]SessionRef`.

- [ ] **Step 1: Write the failing test**

Add to `internal/storage/drawers_test.go`:

```go
func TestStore_OrphanedVerbatimSessions(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingA, err := s.GetOrCreateWing("/tmp/project-a")
	if err != nil {
		t.Fatalf("GetOrCreateWing(a): %v", err)
	}
	wingB, err := s.GetOrCreateWing("/tmp/project-b")
	if err != nil {
		t.Fatalf("GetOrCreateWing(b): %v", err)
	}

	now := time.Now()
	// Two verbatim rows in the same session -- must collapse to one ref.
	if err := s.InsertVerbatimDrawer(wingA, "sess-orphan-1", "Bash", "cmd 1", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}
	if err := s.InsertVerbatimDrawer(wingA, "sess-orphan-1", "Bash", "cmd 2", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}
	// A second, distinct orphaned session in a different wing.
	if err := s.InsertVerbatimDrawer(wingB, "sess-orphan-2", "Read", "read foo", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}
	// A session with only a summary row (already fully pruned) must NOT appear.
	if err := s.InsertSummaryDrawer(wingA, "sess-done", HallFact, "already summarized", now, now, now); err != nil {
		t.Fatalf("InsertSummaryDrawer: %v", err)
	}

	refs, err := s.OrphanedVerbatimSessions()
	if err != nil {
		t.Fatalf("OrphanedVerbatimSessions: %v", err)
	}

	got := make(map[SessionRef]bool)
	for _, r := range refs {
		got[r] = true
	}
	want := map[SessionRef]bool{
		{WingID: wingA, SessionID: "sess-orphan-1"}: true,
		{WingID: wingB, SessionID: "sess-orphan-2"}: true,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d distinct session refs, got %d: %+v", len(want), len(got), refs)
	}
	for w := range want {
		if !got[w] {
			t.Fatalf("expected session ref %+v in result, got %+v", w, refs)
		}
	}
	if got[SessionRef{WingID: wingA, SessionID: "sess-done"}] {
		t.Fatalf("summary-only session must not appear in orphaned verbatim sessions, got %+v", refs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/... -run TestStore_OrphanedVerbatimSessions -v`
Expected: FAIL — `SessionRef` and `OrphanedVerbatimSessions` undefined (compile error).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/storage/drawers.go` (near `DeleteDrawers`, since both are used by the same daemon-side cleanup/recovery flow):

```go
// SessionRef identifies one session's rows in a specific wing.
type SessionRef struct {
	WingID    int64
	SessionID string
}

// OrphanedVerbatimSessions returns every distinct (wing_id, session_id) pair
// that currently has at least one verbatim row. The daemon uses this at
// startup to recover sessions whose in-memory debounce/tracking state was
// lost on a previous restart before their idle window fired -- without
// this, those sessions' verbatim rows would never be summarized or pruned.
func (s *Store) OrphanedVerbatimSessions() ([]SessionRef, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT wing_id, session_id FROM drawers WHERE type = 'verbatim'`,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: query orphaned verbatim sessions: %w", err)
	}
	defer rows.Close()

	var refs []SessionRef
	for rows.Next() {
		var ref SessionRef
		if err := rows.Scan(&ref.WingID, &ref.SessionID); err != nil {
			return nil, fmt.Errorf("storage: scan orphaned verbatim session: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate orphaned verbatim sessions: %w", err)
	}
	return refs, nil
}
```

Check `internal/storage/drawers.go`'s existing imports already include `"fmt"` (used by `DeleteDrawer`/`DeleteDrawers`) — no new import needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/... -run TestStore_OrphanedVerbatimSessions -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/drawers.go internal/storage/drawers_test.go
git commit -m "feat(storage): add OrphanedVerbatimSessions to find unpruned session backlog"
```

---

### Task 2: `Daemon.Warmup`

**Files:**
- Modify: `internal/daemon/daemon.go`
- Test: `internal/daemon/daemon_summarize_test.go`

**Interfaces:**
- Consumes: `storage.SessionRef`, `Store.OrphanedVerbatimSessions() ([]storage.SessionRef, error)` (Task 1); existing `Daemon` fields `sessionWing map[string]int64`, `sessionInvoker map[string]summarizer.Invoker`, `Tracker *debounce.Tracker`, `claudeInvoker summarizer.Invoker`; existing `Tracker.Touch(sessionID string, at time.Time)` (`internal/debounce/debounce.go`).
- Produces:
  ```go
  func (d *Daemon) Warmup() error
  ```
  Task 3 calls this once from `cmd/memremarkd/main.go` right after `daemon.New(...)`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/daemon/daemon_summarize_test.go`:

```go
// TestDaemon_Warmup_RecoversOrphanedSessionAcrossRestart is the regression
// test for the production incident: verbatim rows from a session that
// finished before a daemon restart never got summarized/pruned, because
// Tracker/sessionWing/sessionInvoker are in-memory only and only get
// populated by recordObservation on NEW transcript activity -- which an
// already-finished session will never produce again. Warmup() must recover
// such sessions from the DB so the very next PollOnce tick prunes them.
func TestDaemon_Warmup_RecoversOrphanedSessionAcrossRestart(t *testing.T) {
	store, err := storage.Open(tempDBPath(t))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	// Simulate verbatim left behind by a PREVIOUS daemon process: inserted
	// directly via the store, with no recordObservation call in this
	// process, so sessionWing/sessionInvoker/Tracker start out empty for it.
	now := time.Now()
	if err := store.InsertVerbatimDrawer(wingID, "sess-orphan", "Bash", "go test ./...", now.Add(-time.Hour)); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}

	invoker := stubInvoker{reply: `[{"hall":"fact","content":"recovered"}]`}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)

	if err := d.Warmup(); err != nil {
		t.Fatalf("Warmup: %v", err)
	}

	// One PollOnce tick, well past idleWindow, should now summarize + prune
	// the recovered session even though this process never touched it.
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	remaining, err := store.VerbatimSince(wingID, "sess-orphan", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected orphaned verbatim to be pruned after Warmup+PollOnce, %d row(s) remain", len(remaining))
	}

	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Content != "recovered" {
		t.Fatalf("expected the recovered session to be distilled, got: %+v", summaries)
	}
}

// TestDaemon_Warmup_DoesNotClobberLiveSessionDebounceClock ensures Warmup
// only seeds sessions it doesn't already know about -- it must never reset
// an actively-tracked session's idle clock back to the epoch, which would
// force-flush a still-in-progress conversation on the very next tick
// instead of waiting for it to actually go idle.
func TestDaemon_Warmup_DoesNotClobberLiveSessionDebounceClock(t *testing.T) {
	store, err := storage.Open(tempDBPath(t))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now()
	if err := store.InsertVerbatimDrawer(wingID, "sess-live", "Bash", "still typing", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}

	invoker := stubInvoker{reply: `[{"hall":"fact","content":"should not fire yet"}]`}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)

	// Simulate this process having JUST recorded an observation for
	// sess-live (i.e. it's actively being tracked, mid-conversation).
	d.sessionWing["sess-live"] = wingID
	d.sessionInvoker["sess-live"] = invoker
	d.Tracker.Touch("sess-live", now)

	if err := d.Warmup(); err != nil {
		t.Fatalf("Warmup: %v", err)
	}

	// Immediately after Warmup, at the same instant as the last Touch,
	// sess-live must NOT be due yet -- Warmup must not have reset its
	// debounce clock to the epoch.
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	remaining, err := store.VerbatimSince(wingID, "sess-live", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected the live session's verbatim row to survive (not due yet), %d row(s) remain", len(remaining))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/daemon/... -run TestDaemon_Warmup -v`
Expected: FAIL — `d.Warmup` undefined (compile error).

- [ ] **Step 3: Write minimal implementation**

Add to `internal/daemon/daemon.go` (after the `New` constructor):

```go
// Warmup seeds in-memory session tracking from any verbatim backlog left
// over from a previous daemon process -- e.g. a session that went idle and
// finished before the daemon restarted. Without this, such a session would
// never produce a new observation to re-arm its debounce clock (transcript
// byte-offsets ARE persisted across restarts in poll_state, so no new
// lines means no new Touch), leaving its verbatim rows orphaned forever
// even though summarizeSessionWithBatchSize's prune logic is correct.
//
// The recovered invoker always defaults to d.claudeInvoker: resolveInvokers
// (cmd/memremarkd/main.go) constructs ClaudeInvoker and AntigravityInvoker
// as either the same value (only one CLI installed) or each other's
// FallbackInvoker.Fallback (both installed), so there is no configuration
// where this default produces a session that fails to summarize.
//
// Call this once, right after New, before the poll loop starts. It is
// idempotent and safe to call again later: sessions already known in
// sessionWing are skipped so a live session's debounce clock is never
// clobbered back to the epoch.
func (d *Daemon) Warmup() error {
	refs, err := d.Store.OrphanedVerbatimSessions()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, tracked := d.sessionWing[ref.SessionID]; tracked {
			continue
		}
		d.sessionWing[ref.SessionID] = ref.WingID
		d.sessionInvoker[ref.SessionID] = d.claudeInvoker
		// Epoch guarantees Due() fires on the very first check, regardless
		// of idleWindow, since now.Sub(epoch) is always far past it.
		d.Tracker.Touch(ref.SessionID, time.Unix(0, 0))
	}
	return nil
}
```

Check `internal/daemon/daemon.go`'s existing imports already include `"time"` (used by `idleWindow`, `dbMeta.modTime`) — no new import needed.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/daemon/... -run TestDaemon_Warmup -v`
Expected: PASS (both tests)

Also run the full existing daemon + storage suites to confirm no regression:

Run: `go test ./internal/daemon/... ./internal/storage/... -v`
Expected: PASS (all tests, including the pre-existing ones)

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/daemon_summarize_test.go
git commit -m "feat(daemon): add Warmup to recover orphaned verbatim sessions on restart"
```

---

### Task 3: Wire `Warmup` into `memremarkd` startup

**Files:**
- Modify: `cmd/memremarkd/main.go`

**Interfaces:**
- Consumes: `Daemon.Warmup() error` (Task 2).
- Produces: nothing new for later tasks — this is the final integration point.

- [ ] **Step 1: Call `Warmup()` after `daemon.New(...)` and log the result**

In `cmd/memremarkd/main.go`, immediately after:

```go
	d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB,
		setup.ClaudeInvoker, setup.AntigravityInvoker, targetLang)
```

add:

```go
	if err := d.Warmup(); err != nil {
		log.Printf("memremarkd: warmup (recover orphaned verbatim sessions) failed: %v", err)
	}
```

(No count of recovered sessions is logged here — `Warmup()` intentionally returns only `error`, matching every other `Daemon` method's signature in this file; adding a return value just for a log line would be a needless interface change for a one-line diagnostic. If recovery activity needs visibility later, the existing per-session `daemon: summarize session %s failed/succeeded` logging in `PollOnce` already covers it once the next tick runs.)

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./...`
Expected: success, no errors.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/memremarkd/main.go
git commit -m "fix(memremarkd): recover orphaned verbatim sessions on daemon startup"
```

- [ ] **Step 5: Manual end-to-end verification against the real database**

This is the step that actually confirms the production bug is fixed (unit tests alone use throwaway temp DBs).

1. Stop the running daemon: `systemctl --user stop memremarkd` (or however it's currently supervised — check `ps aux | grep memremarkd` first if unsure).
2. Record current verbatim size: `sqlite3 ~/.memremark/memremark.db "SELECT COUNT(*), ROUND(SUM(LENGTH(content))/1024.0/1024.0,2) FROM drawers WHERE type='verbatim';"`
3. Rebuild and restart the daemon with the fix (build command / restart mechanism as previously used for the v0.1.4 deploy).
4. Wait at least a few poll ticks (daemon polls every 3s; idleWindow is 5s) — 30-60s is plenty.
5. Re-run the same query from step 2 and confirm the verbatim row count / MB has dropped substantially (it will not necessarily hit zero in one pass, since `maxSummarizeBatchBytes` bounds each session to one batch per tick for very large backlogs — but it should now be actively draining instead of static).
6. Check `journalctl` / daemon logs for any `daemon: summarize session ... failed` entries during this window — a failure there means an orphaned session's invoker call itself failed (e.g. CLI auth issue), not a Warmup bug, and should be triaged separately.

---

## Self-Review

**Spec coverage:** Root-cause paragraph (in-memory tracking lost on restart, offsets persisted) → Task 1 (find orphaned sessions in DB) + Task 2 (seed them back into memory) + Task 3 (run it at startup) covers the full fix end to end, including the "don't clobber live sessions" constraint (explicit test in Task 2) and the "no data loss" constraint (recovery reuses the existing, already-tested `summarizeSessionWithBatchSize` prune path unchanged).

**Placeholder scan:** No TBD/TODO; every step has runnable code and exact commands.

**Type consistency:** `storage.SessionRef{WingID int64, SessionID string}` and `Store.OrphanedVerbatimSessions() ([]SessionRef, error)` (Task 1) match the types consumed in `Daemon.Warmup()` (Task 2). `Daemon.Warmup() error` (Task 2) matches the call site in Task 3. Field names `sessionWing`, `sessionInvoker`, `claudeInvoker`, `Tracker` match their existing declarations in `internal/daemon/daemon.go`.
