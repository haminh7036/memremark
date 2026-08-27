package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

type dualStubInvoker struct {
	summaryJSON string
	digestJSON  string
	digestErr   error
	calls       int
}

func (d *dualStubInvoker) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	d.calls++
	if strings.Contains(prompt, "structured session digest") || strings.Contains(prompt, "Distilled Memory Items") {
		if d.digestErr != nil {
			return "", d.digestErr
		}
		return d.digestJSON, nil
	}
	return d.summaryJSON, nil
}

// recordingInvoker remembers every batch of observations it was asked to
// summarize, so tests can assert on how the caller chunked its input.
type recordingInvoker struct {
	batches [][]observation.Observation
	opts    []summarizer.InvokerOptions
	reply   string
}

func (r *recordingInvoker) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	if !strings.Contains(prompt, "structured session digest") && !strings.Contains(prompt, "Distilled Memory Items") {
		r.batches = append(r.batches, nil) // placeholder; prompt itself isn't parsed back into observations
	}
	if len(opts) > 0 {
		r.opts = append(r.opts, opts[0])
	}
	return r.reply, nil
}

func TestTakeBatchStaysWithinByteBudget(t *testing.T) {
	verbatim := []storage.Drawer{
		{Content: "aaaa"}, // 4 bytes
		{Content: "bbbb"}, // 4 bytes, running total 8
		{Content: "cccc"}, // 4 bytes, running total would be 12 > budget of 10
		{Content: "dddd"},
	}

	batch := takeBatch(verbatim, 10)
	if len(batch) != 2 {
		t.Fatalf("expected first 2 rows (8 bytes <= budget 10), got %d rows", len(batch))
	}
}

func TestTakeBatchAlwaysIncludesAtLeastOneRowEvenIfOversized(t *testing.T) {
	verbatim := []storage.Drawer{
		{Content: strings.Repeat("x", 100)}, // a single row bigger than the budget
		{Content: "y"},
	}

	batch := takeBatch(verbatim, 10)
	if len(batch) != 1 {
		t.Fatalf("expected exactly the first (oversized) row alone, got %d rows", len(batch))
	}
}

func TestTakeBatchOnEmptyInputReturnsNil(t *testing.T) {
	if got := takeBatch(nil, 10); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

// TestSummarizeSessionChunksLargeBacklogAcrossMultipleInvokerCalls is the
// regression test for the production incident: a session whose verbatim
// backlog never summarized successfully grew to 1315 rows / ~3.5MB and
// blew past the OS's ARG_MAX (2MB) on every `claude -p <prompt>` attempt,
// forever, because summarizeSession passed the *entire* backlog to one
// Invoke call with no size cap. This confirms a large backlog is now
// drained across several bounded Invoke calls instead of one unbounded one.
func TestSummarizeSessionChunksLargeBacklogAcrossMultipleInvokerCalls(t *testing.T) {
	store, err := storage.Open(tempDBPath(t))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	const rowContentBytes = 200_000
	const numRows = 6 // 6 * 200_000 = 1,200,000 bytes total, comfortably over a small test budget
	base := time.Now().Add(-time.Hour)
	for i := 0; i < numRows; i++ {
		content := strings.Repeat("x", rowContentBytes)
		if err := store.InsertVerbatimDrawer(wingID, "sess-1", "Bash", content, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("InsertVerbatimDrawer %d: %v", i, err)
		}
	}

	invoker := &recordingInvoker{reply: `[{"hall":"fact","content":"chunk summarized"}]`}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)
	d.sessionWing["sess-1"] = wingID
	d.sessionInvoker["sess-1"] = invoker

	// Use a small test-scale byte budget (500_000) so this test runs fast
	// without allocating a real multi-megabyte string, while still proving
	// the same chunking logic production uses at its real budget.
	if err := d.summarizeSessionWithBatchSize(context.Background(), "sess-1", time.Now(), 500_000); err != nil {
		t.Fatalf("summarizeSessionWithBatchSize: %v", err)
	}

	if len(invoker.batches) < 2 {
		t.Fatalf("expected the 1.2MB backlog to be split across multiple Invoke calls at a 500KB budget, got %d call(s)", len(invoker.batches))
	}

	summaries, err := store.RecentSummaries(wingID, 100)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != len(invoker.batches) {
		t.Fatalf("expected one summary drawer per Invoke call (%d), got %d summary drawers", len(invoker.batches), len(summaries))
	}
}

// TestSummarizeSessionPrunesVerbatimAfterSuccessfulSummarize is the
// regression test for the DB-bloat fix: verbatim rows have done their job
// once they're distilled into a summary drawer, so they should be deleted
// rather than accumulating forever (production incident: 101MB DB, 89.9MB
// of it verbatim rows never cleaned up).
func TestSummarizeSessionPrunesVerbatimAfterSuccessfulSummarize(t *testing.T) {
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
	if err := store.InsertVerbatimDrawer(wingID, "sess-1", "Bash", "go test ./...", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}

	invoker := stubInvoker{reply: `[{"hall":"fact","content":"summarized"}]`}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)
	d.sessionWing["sess-1"] = wingID
	d.sessionInvoker["sess-1"] = invoker

	if err := d.summarizeSession(context.Background(), "sess-1", now); err != nil {
		t.Fatalf("summarizeSession: %v", err)
	}

	remaining, err := store.VerbatimSince(wingID, "sess-1", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected the summarized verbatim row to be pruned, %d row(s) remain", len(remaining))
	}

	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Content != "summarized" {
		t.Fatalf("expected the distilled summary to survive pruning, got: %+v", summaries)
	}
}

// TestSummarizeSessionKeepsVerbatimWhenInvokerFails ensures a failed
// summarize call leaves the batch untouched -- pruning must only happen
// after the verbatim rows have actually been distilled, never before.
func TestSummarizeSessionKeepsVerbatimWhenInvokerFails(t *testing.T) {
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
	if err := store.InsertVerbatimDrawer(wingID, "sess-1", "Bash", "go test ./...", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}

	invoker := stubInvoker{err: errors.New("invoker unavailable")}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)
	d.sessionWing["sess-1"] = wingID
	d.sessionInvoker["sess-1"] = invoker

	if err := d.summarizeSession(context.Background(), "sess-1", now); err == nil {
		t.Fatalf("expected summarizeSession to return the invoker error")
	}

	remaining, err := store.VerbatimSince(wingID, "sess-1", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected the unsummarized verbatim row to survive a failed summarize, %d row(s) remain", len(remaining))
	}
}

func tempDBPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/memremark.db"
}

func TestDaemon_SummarizeSession_FallbackIntegration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	primary := &stubInvoker{err: errors.New("claude -p 429 quota exceeded")}
	fallback := &stubInvoker{reply: `[{"hall":"fact","content":"fallback worked"}]`}
	fallbackTriggered := false

	invoker := summarizer.FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
		OnFallback: func(err error) {
			fallbackTriggered = true
		},
	}

	d := New(store, t.TempDir(), filepath.Join(t.TempDir(), "antigravity.db"), invoker, invoker)

	now := time.Now()
	obs := observation.Observation{
		SessionID: "session-fallback-test",
		WingPath:  "/test/ws",
		ToolName:  "ViewFile",
		Content:   "viewed file",
		Timestamp: now,
	}

	if err := d.recordObservation(obs, invoker, now); err != nil {
		t.Fatalf("recordObservation: %v", err)
	}

	if err := d.summarizeSession(context.Background(), obs.SessionID, now); err != nil {
		t.Fatalf("summarizeSession: %v", err)
	}

	if !fallbackTriggered {
		t.Fatalf("expected fallback callback to have triggered")
	}

	wingID, err := store.GetOrCreateWing("/test/ws")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	drawers, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(drawers) != 1 || drawers[0].Content != "fallback worked" || drawers[0].Hall != "fact" {
		t.Fatalf("expected 1 drawer with content 'fallback worked' in hall 'fact', got: %+v", drawers)
	}
}

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

func TestDaemon_SummarizeSession_PassesDeterministicSessionIDAndWorkDir(t *testing.T) {
	store, err := storage.Open(tempDBPath(t))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	projectPath := "/tmp/test-project-deterministic"
	wingID, err := store.GetOrCreateWing(projectPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now()
	if err := store.InsertVerbatimDrawer(wingID, "sess-det-1", "Bash", "git status", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}

	invoker := &recordingInvoker{reply: `[{"hall":"fact","content":"summarized"}]`}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)
	d.sessionWing["sess-det-1"] = wingID
	d.sessionInvoker["sess-det-1"] = invoker

	if err := d.summarizeSession(context.Background(), "sess-det-1", now); err != nil {
		t.Fatalf("summarizeSession: %v", err)
	}

	if len(invoker.opts) == 0 {
		t.Fatalf("expected opts recorded, got %d", len(invoker.opts))
	}

	expectedSessionID := storage.WingSummarySessionID(projectPath)
	for i, opt := range invoker.opts {
		if opt.SessionID != expectedSessionID {
			t.Errorf("call %d: expected SessionID %q, got %q", i, expectedSessionID, opt.SessionID)
		}
		if opt.WorkDir != projectPath {
			t.Errorf("call %d: expected WorkDir %q, got %q", i, projectPath, opt.WorkDir)
		}
	}
}

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

func TestDaemon_SummarizeSession_DigestSynthesisErrorNonBlocking(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, _ := store.GetOrCreateWing("/home/user/proj")
	sessionID := "test-session-digest-err"

	invoker := &dualStubInvoker{
		summaryJSON: `[{"hall":"fact","content":"Summary works","narrative":"Narrative works"}]`,
		digestErr:   errors.New("LLM error on digest synthesis"),
	}

	d := New(store, t.TempDir(), "", invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})
	d.sessionWing[sessionID] = wingID
	d.sessionInvoker[sessionID] = invoker

	now := time.Now()
	_ = store.InsertVerbatimDrawer(wingID, sessionID, "edit", "some changes", now)

	if err := d.summarizeSession(context.Background(), sessionID, now); err != nil {
		t.Fatalf("summarizeSession failed unexpectedly: %v", err)
	}

	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries failed: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Content != "Summary works" {
		t.Errorf("expected 1 summary drawer, got %+v", summaries)
	}

	digest, err := store.GetSessionDigest(sessionID)
	if err != nil {
		t.Fatalf("GetSessionDigest failed: %v", err)
	}
	if digest != nil {
		t.Errorf("expected no digest when synthesis fails, got %+v", digest)
	}
}

func TestDaemon_SynthesizeSessionDigest_Direct(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, _ := store.GetOrCreateWing("/home/user/proj")
	sessionID := "test-session-direct"

	now := time.Now().Truncate(time.Second)
	_ = store.InsertSummaryDrawer(wingID, sessionID, "fact", "Cache added", "Added Redis cache", now, now, now)

	digestJSON := `{"request":"Optimize latency","investigated":"Profiled DB","learned":"Queries slow","completed":"Added Redis cache","next_steps":"","notes":""}`
	invoker := &dualStubInvoker{digestJSON: digestJSON}

	d := New(store, t.TempDir(), "", invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})

	if err := d.synthesizeSessionDigest(context.Background(), sessionID, wingID, invoker, now); err != nil {
		t.Fatalf("synthesizeSessionDigest failed: %v", err)
	}

	digest, err := store.GetSessionDigest(sessionID)
	if err != nil {
		t.Fatalf("GetSessionDigest failed: %v", err)
	}
	if digest == nil {
		t.Fatalf("expected digest to be created, got nil")
	}
	if digest.Request != "Optimize latency" || digest.Completed != "Added Redis cache" {
		t.Errorf("unexpected digest fields: %+v", digest)
	}
}

