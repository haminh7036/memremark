package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// recordingInvoker remembers every batch of observations it was asked to
// summarize, so tests can assert on how the caller chunked its input.
type recordingInvoker struct {
	batches [][]observation.Observation
	reply   string
}

func (r *recordingInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	r.batches = append(r.batches, nil) // placeholder; prompt itself isn't parsed back into observations
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
