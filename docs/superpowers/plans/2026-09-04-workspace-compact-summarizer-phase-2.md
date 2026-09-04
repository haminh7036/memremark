# Workspace-Level Compact Summarizer (Phase 2: Workspace-Level Batching & One-Shot Distillation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Phase 2 of the Workspace-Level Compact Summarizer: aggregate unsummarized verbatim observations across all sessions by workspace (`wing_id`), pre-compact noisy tool outputs with a 70 KB budget, execute exactly one bounded CLI invocation per idle cycle, and atomically store pure JSON array memories while pruning raw verbatim.

**Architecture:**
- In `internal/summarizer`, add `compact.go` with ANSI stripping, middle-truncation for long tool outputs (>800 chars), and `CompactAndBudget` ensuring prompts remain safely $\le 70\text{ KB}$ (under Linux 128 KiB `MAX_ARG_STRLEN`).
- In `internal/storage`, add `UnsummarizedVerbatimByWing` and `OrphanedVerbatimWings` to retrieve all unsummarized verbatim across sessions for a wing.
- In `internal/daemon`, update `Tracker` to key on `wing_id`, update `Warmup` to seed pending wings, and replace `summarizeSession` and multi-batch loops with `summarizeWing` (one single CLI call per workspace idle event).

**Tech Stack:** Go 1.26+, modernc SQLite, POSIX regex/strings, sync/atomic.

## Global Constraints

- Linux `MAX_ARG_STRLEN` compliance: Total prompt payload must never exceed 70 KB.
- One-Shot rule: Exactly one CLI invocation per workspace idle event (no inner multi-batch loops, no standalone digest calls).
- Atomic cleanup: Verbatim records included in the batch are deleted and incremental vacuum is triggered only after successful summary insertion.
- Fault tolerance: Invocations that fail or timeout preserve verbatim rows without deletion.

---

### Task 1: Observation Pre-Compaction & Payload Budgeter

**Files:**
- Create: `internal/summarizer/compact.go`
- Test: `internal/summarizer/compact_test.go`

**Interfaces:**
- Consumes: `storage.Drawer` slices, `observation.Observation`.
- Produces: `CompactAndBudget(drawers []storage.Drawer, maxBytes int) ([]storage.Drawer, []observation.Observation)`, `CleanANSI(string) string`, `TruncateContent(content string, maxLen int) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/summarizer/compact_test.go`:
```go
package summarizer

import (
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestCleanANSI(t *testing.T) {
	input := "\x1b[31mRed Text\x1b[0m \x1b[1;32mGreen Bold\x1b[0m"
	expected := "Red Text Green Bold"
	got := CleanANSI(input)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestTruncateContent(t *testing.T) {
	short := "short text"
	if got := TruncateContent(short, 800); got != short {
		t.Fatalf("expected %q, got %q", short, got)
	}

	long := strings.Repeat("A", 500) + strings.Repeat("B", 500)
	got := TruncateContent(long, 800)
	if len(got) >= len(long) {
		t.Fatalf("expected truncated length < %d, got %d", len(long), len(got))
	}
	if !strings.HasPrefix(got, strings.Repeat("A", 400)) {
		t.Fatal("expected head to be preserved")
	}
	if !strings.HasSuffix(got, strings.Repeat("B", 400)) {
		t.Fatal("expected tail to be preserved")
	}
	if !strings.Contains(got, "[truncated") {
		t.Fatal("expected truncation indicator")
	}
}

func TestCompactAndBudget(t *testing.T) {
	now := time.Now()
	drawers := []storage.Drawer{
		{ID: 1, ToolName: "bash", Content: strings.Repeat("x", 1000), CreatedAt: now},
		{ID: 2, ToolName: "edit", Content: strings.Repeat("y", 1000), CreatedAt: now.Add(time.Second)},
		{ID: 3, ToolName: "read", Content: strings.Repeat("z", 1000), CreatedAt: now.Add(2 * time.Second)},
	}

	// Budget that fits only 2 truncated drawers (~800 chars each + metadata)
	batch, obs := CompactAndBudget(drawers, 2000)
	if len(batch) != 2 {
		t.Fatalf("expected 2 drawers in batch, got %d", len(batch))
	}
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obs))
	}
	if batch[0].ID != 1 || batch[1].ID != 2 {
		t.Fatalf("unexpected batch IDs: %v", batch)
	}
	if len(obs[0].Content) > 850 {
		t.Fatalf("expected observation content to be truncated, got len %d", len(obs[0].Content))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/summarizer -run TestCompactAndBudget`
Expected: FAIL (undefined: CleanANSI, TruncateContent, CompactAndBudget)

- [ ] **Step 3: Implement `internal/summarizer/compact.go`**

Create `internal/summarizer/compact.go`:
```go
package summarizer

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

// MaxWorkspacePromptBytes limits the payload budget sent to the LLM to 70 KB,
// comfortably below Linux's 128 KiB MAX_ARG_STRLEN.
const MaxWorkspacePromptBytes = 70_000

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// CleanANSI removes ANSI terminal color and control escape sequences.
func CleanANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// TruncateContent limits content to maxLen characters by keeping the head
// and tail and placing a truncation marker in between.
func TruncateContent(content string, maxLen int) string {
	cleaned := CleanANSI(content)
	if len(cleaned) <= maxLen || maxLen < 200 {
		return cleaned
	}
	half := (maxLen - 50) / 2
	omitted := len(cleaned) - (half * 2)
	return fmt.Sprintf("%s\n... [truncated %d bytes] ...\n%s", cleaned[:half], omitted, cleaned[len(cleaned)-half:])
}

// CompactAndBudget pre-compacts raw verbatim drawers and selects the longest
// chronological prefix that fits within maxBytes. It always includes at least
// the first drawer even if it exceeds the budget alone.
func CompactAndBudget(drawers []storage.Drawer, maxBytes int) ([]storage.Drawer, []observation.Observation) {
	if len(drawers) == 0 {
		return nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = MaxWorkspacePromptBytes
	}

	var batch []storage.Drawer
	var obs []observation.Observation
	totalBytes := 0

	for i, d := range drawers {
		compacted := TruncateContent(d.Content, 800)
		entrySize := len(compacted) + len(d.ToolName) + 32

		if i > 0 && totalBytes+entrySize > maxBytes {
			break
		}

		batch = append(batch, d)
		obs = append(obs, observation.Observation{
			ToolName: d.ToolName,
			Content:  compacted,
		})
		totalBytes += entrySize
	}

	return batch, obs
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/summarizer -run TestCompactAndBudget`
Expected: PASS

- [ ] **Step 5: Run all summarizer tests**

Run: `go test -v -race ./internal/summarizer/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/summarizer/compact.go internal/summarizer/compact_test.go
git commit -m "feat(summarizer): add observation pre-compaction and payload budgeter"
```

---

### Task 2: Storage Layer Multi-Session Query for Workspace Verbatim

**Files:**
- Modify: `internal/storage/drawers.go:150-180`
- Test: `internal/storage/drawers_wing_test.go`

**Interfaces:**
- Consumes: SQLite database connection.
- Produces: `UnsummarizedVerbatimByWing(wingID int64, limit int) ([]Drawer, error)`, `OrphanedVerbatimWings() ([]int64, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/storage/drawers_wing_test.go`:
```go
package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUnsummarizedVerbatimByWing(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/path/to/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	now := time.Now()
	// Insert verbatim across 2 different sessions in the same wing
	_ = store.InsertVerbatimDrawer(wingID, "sess-1", "bash", "ls -la", now)
	_ = store.InsertVerbatimDrawer(wingID, "sess-2", "edit", "main.go", now.Add(time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "sess-1", "read", "go.mod", now.Add(2*time.Second))

	// Retrieve all unsummarized verbatim for the wing
	rows, err := store.UnsummarizedVerbatimByWing(wingID, 100)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing failed: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows across sessions, got %d", len(rows))
	}
	if rows[0].SessionID != "sess-1" || rows[1].SessionID != "sess-2" || rows[2].SessionID != "sess-1" {
		t.Fatalf("unexpected ordering: %+v", rows)
	}

	// Test OrphanedVerbatimWings
	wings, err := store.OrphanedVerbatimWings()
	if err != nil {
		t.Fatalf("OrphanedVerbatimWings failed: %v", err)
	}
	if len(wings) != 1 || wings[0] != wingID {
		t.Fatalf("expected wingID %d, got %v", wingID, wings)
	}

	// Once deleted, wing is no longer orphaned
	_ = store.DeleteDrawers([]int64{rows[0].ID, rows[1].ID, rows[2].ID})
	wingsAfter, err := store.OrphanedVerbatimWings()
	if err != nil {
		t.Fatalf("OrphanedVerbatimWings failed: %v", err)
	}
	if len(wingsAfter) != 0 {
		t.Fatalf("expected 0 orphaned wings, got %d", len(wingsAfter))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/storage -run TestUnsummarizedVerbatimByWing`
Expected: FAIL (undefined: UnsummarizedVerbatimByWing, OrphanedVerbatimWings)

- [ ] **Step 3: Implement `UnsummarizedVerbatimByWing` and `OrphanedVerbatimWings` in `internal/storage/drawers.go`**

Add to `internal/storage/drawers.go`:
```go
// UnsummarizedVerbatimByWing returns pending verbatim drawers for a wing across
// all sessions, ordered chronologically by created_at ASC.
func (s *Store) UnsummarizedVerbatimByWing(wingID int64, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(
		`SELECT id, content, tool_name, session_id, created_at FROM drawers
		 WHERE wing_id = ? AND type = 'verbatim'
		 ORDER BY created_at ASC, id ASC LIMIT ?`,
		wingID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: query unsummarized verbatim by wing: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var toolName sql.NullString
		var createdAt int64
		if err := rows.Scan(&d.ID, &d.Content, &toolName, &d.SessionID, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan unsummarized verbatim row: %w", err)
		}
		d.WingID = wingID
		d.Type = "verbatim"
		d.ToolName = toolName.String
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// OrphanedVerbatimWings returns unique wing IDs that have unsummarized verbatim rows.
func (s *Store) OrphanedVerbatimWings() ([]int64, error) {
	rows, err := s.db.Query(`SELECT DISTINCT wing_id FROM drawers WHERE type = 'verbatim' ORDER BY wing_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: query orphaned verbatim wings: %w", err)
	}
	defer rows.Close()

	var wingIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: scan orphaned verbatim wing: %w", err)
		}
		wingIDs = append(wingIDs, id)
	}
	return wingIDs, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/storage -run TestUnsummarizedVerbatimByWing`
Expected: PASS

- [ ] **Step 5: Run all storage tests**

Run: `go test -v -race ./internal/storage/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/storage/drawers.go internal/storage/drawers_wing_test.go
git commit -m "feat(storage): add multi-session verbatim queries by wing"
```

---

### Task 3: Workspace-Level Debounce Tracking & Warmup in Daemon

**Files:**
- Modify: `internal/daemon/daemon.go:45-51` (add `wingInvoker` map, replace `sessionWing`)
- Modify: `internal/daemon/daemon.go:97-121` (`Warmup` seeds wings instead of sessions)
- Modify: `internal/daemon/daemon_summarize.go:13-34` (`recordObservation` touches wing)
- Test: `internal/daemon/daemon_wing_test.go`

**Interfaces:**
- Consumes: `Tracker.Touch(wingKey, time)`, `Store.OrphanedVerbatimWings()`.
- Produces: Debounce tracking keyed on workspace (`wing_id`).

- [ ] **Step 1: Write the failing test**

Create `internal/daemon/daemon_wing_test.go`:
```go
package daemon

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

func TestDaemonRecordObservationTouchesWing(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), nil, nil, locale.TargetLanguage{Code: "en"})

	now := time.Now()
	obs1 := observation.Observation{WingPath: tempDir, SessionID: "sess-1", ToolName: "bash", Content: "echo 1", Timestamp: now}
	obs2 := observation.Observation{WingPath: tempDir, SessionID: "sess-2", ToolName: "edit", Content: "edit 2", Timestamp: now.Add(time.Second)}

	_ = d.recordObservation(obs1, nil, now)
	_ = d.recordObservation(obs2, nil, now.Add(time.Second))

	wingID, _ := store.GetOrCreateWing(tempDir)
	wingKey := strconv.FormatInt(wingID, 10)

	// Since both sessions share the wing, only the wing should be due
	due := d.Tracker.Due(now.Add(50*time.Second), idleWindow)
	if len(due) != 1 || due[0] != wingKey {
		t.Fatalf("expected 1 due wing (%s), got %v", wingKey, due)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/daemon -run TestDaemonRecordObservationTouchesWing`
Expected: FAIL (due returns session IDs instead of wingKey)

- [ ] **Step 3: Update `daemon.go` and `recordObservation`**

In `internal/daemon/daemon.go`:
1. In `Daemon` struct, add `wingInvoker map[int64]summarizer.Invoker`:
```go
	wingInvoker map[int64]summarizer.Invoker
```
Initialize in `New`:
```go
	wingInvoker: make(map[int64]summarizer.Invoker),
```
2. Update `Warmup`:
```go
func (d *Daemon) Warmup() error {
	wingIDs, err := d.Store.OrphanedVerbatimWings()
	if err != nil {
		return err
	}
	now := time.Now()
	for i, wingID := range wingIDs {
		wingKey := strconv.FormatInt(wingID, 10)
		d.wingInvoker[wingID] = d.claudeInvoker
		touchTime := now.Add(-idleWindow - time.Duration(i+1)*time.Second)
		d.Tracker.Touch(wingKey, touchTime)
	}
	return nil
}
```

3. In `internal/daemon/daemon_summarize.go`, update `recordObservation`:
```go
func (d *Daemon) recordObservation(obs observation.Observation, invoker summarizer.Invoker, now time.Time) error {
	wingID, err := d.Store.GetOrCreateWing(obs.WingPath)
	if err != nil {
		return err
	}
	createdAt := obs.Timestamp
	if createdAt.IsZero() {
		createdAt = now
	}
	if err := d.Store.InsertVerbatimDrawer(wingID, obs.SessionID, obs.ToolName, obs.Content, createdAt); err != nil {
		return err
	}
	if invoker != nil {
		d.wingInvoker[wingID] = invoker
	}
	d.sessionWing[obs.SessionID] = wingID
	d.sessionInvoker[obs.SessionID] = invoker
	d.Tracker.Touch(strconv.FormatInt(wingID, 10), now)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/daemon -run TestDaemonRecordObservationTouchesWing`
Expected: PASS

- [ ] **Step 5: Run all daemon tests**

Run: `go test -v ./internal/daemon/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/daemon_summarize.go internal/daemon/daemon_wing_test.go
git commit -m "feat(daemon): track debounce and warmup at workspace level"
```

---

### Task 4: One-Shot Workspace Summarization Flow

**Files:**
- Modify: `internal/daemon/daemon_summarize.go:44-122` (implement `summarizeWing`, remove multi-batch loop)
- Modify: `internal/daemon/daemon.go:132-160` (`PollOnce` processes due wings)
- Test: `internal/daemon/daemon_summarize_test.go`
- Test: `internal/daemon/concurrency_test.go`

**Interfaces:**
- Consumes: `d.summarizeWing(ctx, wingID, now)`.
- Produces: Exactly one bounded CLI call per workspace idle tick, storing distilled summary drawers and pruning verbatim.

- [ ] **Step 1: Write the failing test**

In `internal/daemon/daemon_summarize_test.go`:
```go
func TestSummarizeWing_OneShotDistillation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	inv := &stubInvokerCount{
		reply: `[{"hall":"fact","content":"Unified workspace fact","narrative":"Distilled across sessions"}]`,
	}
	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), inv, inv, locale.TargetLanguage{Code: "en"})

	wingID, _ := store.GetOrCreateWing(tempDir)
	now := time.Now()

	// 2 observations across 2 sessions
	_ = store.InsertVerbatimDrawer(wingID, "sess-1", "bash", "echo 1", now)
	_ = store.InsertVerbatimDrawer(wingID, "sess-2", "edit", "echo 2", now.Add(time.Second))

	if err := d.summarizeWing(context.Background(), wingID, now.Add(time.Minute)); err != nil {
		t.Fatalf("summarizeWing failed: %v", err)
	}

	// Must have invoked LLM exactly ONCE
	if inv.callCount != 1 {
		t.Fatalf("expected exactly 1 invoker call, got %d", inv.callCount)
	}

	// Verify summary drawer was written
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("expected 1 summary drawer, got %d (err: %v)", len(summaries), err)
	}
	if summaries[0].Content != "Unified workspace fact" {
		t.Fatalf("unexpected summary content: %s", summaries[0].Content)
	}

	// Verify verbatim was pruned
	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("expected 0 remaining verbatim, got %d", len(remaining))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/daemon -run TestSummarizeWing_OneShotDistillation`
Expected: FAIL (undefined: `summarizeWing`)

- [ ] **Step 3: Implement `summarizeWing` and update `PollOnce`**

In `internal/daemon/daemon_summarize.go`:
```go
func (d *Daemon) summarizeWing(ctx context.Context, wingID int64, now time.Time) error {
	d.cliMutex.Lock()
	defer d.cliMutex.Unlock()

	if d.IsPaused() {
		return nil
	}

	wing, err := d.Store.GetWingByID(wingID)
	if err != nil || wing == nil {
		return err
	}

	verbatim, err := d.Store.UnsummarizedVerbatimByWing(wingID, 500)
	if err != nil || len(verbatim) == 0 {
		return err
	}

	batch, obs := summarizer.CompactAndBudget(verbatim, summarizer.MaxWorkspacePromptBytes)
	if len(batch) == 0 {
		return nil
	}

	invoker := d.wingInvoker[wingID]
	if invoker == nil {
		invoker = d.claudeInvoker
	}
	if invoker == nil {
		invoker = d.antigravityInvoker
	}

	opts := summarizer.InvokerOptions{
		SessionID: storage.WingSummarySessionID(wing.Path),
		WorkDir:   wing.Path,
	}

	callCtx, cancel := context.WithCancel(ctx)
	d.RegisterActiveCancel(cancel)
	items, err := summarizer.SummarizeWithOptions(callCtx, invoker, obs, d.TargetLanguage, opts)
	d.ClearActiveCancel()
	cancel()
	if err != nil {
		return err
	}

	coversFrom := batch[0].CreatedAt
	coversTo := batch[len(batch)-1].CreatedAt
	summarySessionID := storage.WingSummarySessionID(wing.Path)

	for _, item := range items {
		if err := d.Store.InsertSummaryDrawer(wingID, summarySessionID, item.Hall, item.Content, item.Narrative, coversFrom, coversTo, now); err != nil {
			return err
		}
	}

	ids := make([]int64, len(batch))
	for i, v := range batch {
		ids[i] = v.ID
	}
	if err := d.Store.DeleteDrawers(ids); err != nil {
		return err
	}

	return d.Store.IncrementalVacuum()
}
```

In `internal/daemon/daemon.go`, update `PollOnce`:
```go
// PollOnce runs one capture pass over transcripts, then triggers summarization
// for any workspace that has gone idle.
func (d *Daemon) PollOnce(ctx context.Context, now time.Time) error {
	if d.IsPaused() {
		return nil
	}

	if err := d.pollClaudeCode(now); err != nil {
		log.Printf("daemon: claude code poll error: %v", err)
	}
	if err := d.pollAntigravity(now); err != nil {
		log.Printf("daemon: antigravity poll error: %v", err)
	}

	processed := 0
	for _, wingKey := range d.Tracker.Due(now, idleWindow) {
		if ctx.Err() != nil {
			break
		}
		if processed >= maxSessionsPerTick {
			break
		}
		wingID, err := strconv.ParseInt(wingKey, 10, 64)
		if err != nil {
			d.Tracker.Consume(wingKey)
			continue
		}
		if err := d.summarizeWing(ctx, wingID, now); err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("daemon: summarize wing %d failed: %v", wingID, err)
			continue
		}
		d.Tracker.Consume(wingKey)
		processed++
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/daemon -run TestSummarizeWing_OneShotDistillation`
Expected: PASS

- [ ] **Step 5: Run all daemon tests**

Run: `go test -v -race ./internal/daemon/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/daemon_summarize.go internal/daemon/daemon.go internal/daemon/daemon_summarize_test.go internal/daemon/concurrency_test.go
git commit -m "feat(daemon): implement one-shot workspace summarization and prune loop"
```

---

### Task 5: Multi-Session Integration Testing & Phase 2 Verification

**Files:**
- Modify: `internal/daemon/daemon_loop_test.go`
- Test: Full repository test suite

- [ ] **Step 1: Write multi-session integration test in `internal/daemon/daemon_loop_test.go`**

Verify that:
- 3 sessions record observations concurrently in the same wing.
- Advance clock past `idleWindow`.
- `PollOnce` executes exactly 1 summarization call for the workspace.
- Summary drawer is stored and all 3 sessions' verbatim rows are pruned.

- [ ] **Step 2: Run full repository test suite with race detector**

Run: `go test -v -race ./...`
Expected: PASS with 0 race conditions.

- [ ] **Step 3: Build all binaries**

Run: `make build`
Expected: Clean build of all 5 binaries in `bin/`.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/daemon_loop_test.go
git commit -m "test(daemon): add multi-session workspace summarization integration tests"
```
