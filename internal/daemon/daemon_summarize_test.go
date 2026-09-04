package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
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

type stubInvokerCount struct {
	reply     string
	callCount int
}

func (s *stubInvokerCount) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	s.callCount++
	return s.reply, nil
}

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

// TestSummarizeWing_BoundsLargeBacklogToOneInvocation verifies that a large backlog
// exceeding the 70 KB budget triggers exactly ONE invoker call (One-Shot distillation),
// prunes only the batch that was distilled, and leaves the remaining rows unsummarized.
func TestSummarizeWing_BoundsLargeBacklogToOneInvocation(t *testing.T) {
	store, err := storage.Open(tempDBPath(t))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	const numRows = 100 // 100 * ~832 bytes = ~83.2 KB > 70 KB budget
	base := time.Now().Add(-time.Hour)
	for i := 0; i < numRows; i++ {
		content := strings.Repeat("x", 1000)
		if err := store.InsertVerbatimDrawer(wingID, "sess-1", "Bash", content, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("InsertVerbatimDrawer %d: %v", i, err)
		}
	}

	invoker := &stubInvokerCount{reply: `[{"hall":"fact","content":"chunk summarized"}]`}
	d := New(store, t.TempDir(), t.TempDir()+"/conversation_summaries.db", invoker, invoker)
	d.wingInvoker[wingID] = invoker

	if err := d.summarizeWing(context.Background(), wingID, time.Now()); err != nil {
		t.Fatalf("summarizeWing: %v", err)
	}

	// Must be exactly one invocation (One-Shot distillation)
	if invoker.callCount != 1 {
		t.Fatalf("expected exactly 1 invoker call, got %d", invoker.callCount)
	}

	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary drawer from the single invocation, got %d", len(summaries))
	}

	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 200)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(remaining) == 0 || len(remaining) >= numRows {
		t.Fatalf("expected some rows to remain unsummarized, got %d remaining out of %d", len(remaining), numRows)
	}
}

// TestSummarizeWingPrunesVerbatimAfterSuccessfulSummarize verifies that
// verbatim rows are deleted once distilled into summary drawers.
func TestSummarizeWingPrunesVerbatimAfterSuccessfulSummarize(t *testing.T) {
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
	d.wingInvoker[wingID] = invoker

	if err := d.summarizeWing(context.Background(), wingID, now); err != nil {
		t.Fatalf("summarizeWing: %v", err)
	}

	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
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

// TestSummarizeWingKeepsVerbatimWhenInvokerFails ensures a failed
// summarize call leaves the batch untouched -- pruning must only happen
// after the verbatim rows have actually been distilled, never before.
func TestSummarizeWingKeepsVerbatimWhenInvokerFails(t *testing.T) {
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
	d.wingInvoker[wingID] = invoker

	if err := d.summarizeWing(context.Background(), wingID, now); err == nil {
		t.Fatalf("expected summarizeWing to return the invoker error")
	}

	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected the unsummarized verbatim row to survive a failed summarize, %d row(s) remain", len(remaining))
	}
}

func tempDBPath(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/memremark.db"
}

func TestDaemon_SummarizeWing_FallbackIntegration(t *testing.T) {
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

	wingID, err := store.GetOrCreateWing("/test/ws")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	if err := d.summarizeWing(context.Background(), wingID, now); err != nil {
		t.Fatalf("summarizeWing: %v", err)
	}

	if !fallbackTriggered {
		t.Fatalf("expected fallback callback to have triggered")
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

	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
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
	wingKey := strconv.FormatInt(wingID, 10)
	d.wingInvoker[wingID] = invoker
	d.Tracker.Touch(wingKey, now)

	if err := d.Warmup(); err != nil {
		t.Fatalf("Warmup: %v", err)
	}

	// Immediately after Warmup, at the same instant as the last Touch,
	// sess-live must NOT be due yet -- Warmup must not have reset its
	// debounce clock to the epoch.
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected the live session's verbatim row to survive (not due yet), %d row(s) remain", len(remaining))
	}
}

func TestDaemon_SummarizeWing_PassesDeterministicSessionIDAndWorkDir(t *testing.T) {
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
	d.wingInvoker[wingID] = invoker

	if err := d.summarizeWing(context.Background(), wingID, now); err != nil {
		t.Fatalf("summarizeWing: %v", err)
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

