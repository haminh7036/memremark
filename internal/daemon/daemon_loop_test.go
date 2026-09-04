package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

func TestPollClaudeCode_SkipsWingSummarySessions(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	projectPath := "/tmp/project-claude"
	wingID, err := store.GetOrCreateWing(projectPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	projectsRoot := filepath.Join(dir, "claude-projects")
	projectDir := filepath.Join(projectsRoot, "-tmp-project-claude")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	summaryUUID := storage.WingSummarySessionID(projectPath)
	transcript := `{"type":"assistant","sessionId":"` + summaryUUID + `","cwd":"/tmp/project-claude","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","sessionId":"` + summaryUUID + `","cwd":"/tmp/project-claude","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"README.md","is_error":false}]}}
`
	if err := os.WriteFile(filepath.Join(projectDir, summaryUUID+".jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	summariesDB := filepath.Join(dir, "antigravity", "conversation_summaries.db")
	if err := os.MkdirAll(filepath.Dir(summariesDB), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	createEmptySummariesDB(t, summariesDB)

	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d.pollClaudeCode(time.Now()); err != nil {
		t.Fatalf("pollClaudeCode: %v", err)
	}

	verbatim, err := store.VerbatimSince(wingID, summaryUUID, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(verbatim) != 0 {
		t.Fatalf("expected 0 verbatim drawers for summary session, got %d", len(verbatim))
	}
}

func TestPollAntigravity_SkipsWingSummarySessions(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	projectPath := "/tmp/project-agy"
	wingID, err := store.GetOrCreateWing(projectPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	summaryUUID := storage.WingSummarySessionID(projectPath)
	antigravityDir := filepath.Join(dir, "antigravity")
	if err := os.MkdirAll(filepath.Join(antigravityDir, "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	summariesDB := filepath.Join(antigravityDir, "conversation_summaries.db")
	createSummariesDBWithConversation(t, summariesDB, summaryUUID, projectPath, "2026-08-11 10:00:00.000000000+00:00")

	conversationDB := filepath.Join(antigravityDir, "conversations", summaryUUID+".db")
	mustExecSQLite(t, conversationDB, `CREATE TABLE steps (idx integer, step_payload blob)`)
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (0, ?)`,
		buildProtobufPromptBlob("summary prompt and observations"))

	projectsRoot := filepath.Join(dir, "claude-projects")
	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d.pollAntigravity(time.Now()); err != nil {
		t.Fatalf("pollAntigravity: %v", err)
	}

	verbatim, err := store.VerbatimSince(wingID, summaryUUID, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(verbatim) != 0 {
		t.Fatalf("expected 0 verbatim drawers for summary session, got %d", len(verbatim))
	}
}

// TestPollOnce_MultiSessionConcurrentObservations_OneShotSummarization verifies
// Phase 2 acceptance criteria:
// - 3 sessions record observations concurrently in the same wing.
// - Advance clock past idleWindow.
// - PollOnce executes exactly 1 summarization call for the workspace.
// - Summary drawer is stored and all 3 sessions' verbatim rows are pruned.
func TestPollOnce_MultiSessionConcurrentObservations_OneShotSummarization(t *testing.T) {
	dir := t.TempDir()
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

	invoker := &stubInvokerCount{
		reply: `[{"hall":"fact","content":"Unified workspace fact from 3 concurrent sessions","narrative":"Distilled multi-session observations"}]`,
	}

	workspacePath := "/tmp/shared-workspace"
	projectsRoot := filepath.Join(dir, "claude-projects")
	d := New(store, projectsRoot, summariesDB, invoker, invoker, locale.TargetLanguage{Code: "en"})

	base := time.Now()

	// 3 sessions record observations concurrently in the same wing (workspace)
	var wg sync.WaitGroup
	errCh := make(chan error, 3)
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			obs := observation.Observation{
				WingPath:  workspacePath,
				SessionID: fmt.Sprintf("session-%d", idx),
				ToolName:  "Bash",
				Content:   fmt.Sprintf("echo 'running build step %d'", idx),
				Timestamp: base.Add(time.Duration(idx) * time.Millisecond),
			}
			if err := d.recordObservation(obs, invoker, base); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("recordObservation: %v", err)
	}

	wingID, err := store.GetOrCreateWing(workspacePath)
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	// Verify all 3 sessions recorded unsummarized verbatim rows
	verbatim, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(verbatim) != 3 {
		t.Fatalf("expected 3 unsummarized verbatim rows, got %d", len(verbatim))
	}

	// Verify that before idleWindow has elapsed, PollOnce does NOT summarize
	if err := d.PollOnce(context.Background(), base.Add(10*time.Second)); err != nil {
		t.Fatalf("PollOnce before idleWindow: %v", err)
	}
	if invoker.callCount != 0 {
		t.Fatalf("expected 0 invoker calls before idleWindow, got %d", invoker.callCount)
	}

	// Advance clock past idleWindow
	now := base.Add(idleWindow + time.Second)

	// PollOnce executes exactly 1 summarization call for the workspace
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce after idleWindow: %v", err)
	}
	if invoker.callCount != 1 {
		t.Fatalf("expected exactly 1 summarization call for the workspace, got %d", invoker.callCount)
	}

	// Summary drawer is stored under the wing with the deterministic summary session ID
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected exactly 1 summary drawer, got %d", len(summaries))
	}
	if summaries[0].Content != "Unified workspace fact from 3 concurrent sessions" {
		t.Fatalf("unexpected summary content: %q", summaries[0].Content)
	}
	expectedSessionID := storage.WingSummarySessionID(workspacePath)
	bySession, err := store.GetDrawersBySession(wingID, expectedSessionID, "summary")
	if err != nil {
		t.Fatalf("GetDrawersBySession: %v", err)
	}
	if len(bySession) != 1 {
		t.Fatalf("expected 1 summary drawer with session ID %q, got %d", expectedSessionID, len(bySession))
	}

	// All 3 sessions' verbatim rows are pruned
	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining verbatim rows across the wing, got %d", len(remaining))
	}
	for i := 1; i <= 3; i++ {
		sID := fmt.Sprintf("session-%d", i)
		sRows, err := store.VerbatimSince(wingID, sID, time.Unix(0, 0))
		if err != nil {
			t.Fatalf("VerbatimSince for %s: %v", sID, err)
		}
		if len(sRows) != 0 {
			t.Fatalf("expected 0 verbatim rows remaining for %s, got %d", sID, len(sRows))
		}
	}

	// Subsequent PollOnce does not re-invoke summarization
	if err := d.PollOnce(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatalf("subsequent PollOnce: %v", err)
	}
	if invoker.callCount != 1 {
		t.Fatalf("expected call count to remain 1 on subsequent poll, got %d", invoker.callCount)
	}
}

// TestPollOnce_MultiSessionTranscriptIngestion_OneShotSummarization verifies the
// end-to-end transcript discovery, ingestion, debounce, and one-shot summarization
// when 3 Claude Code sessions concurrently write transcript files in the same workspace.
func TestPollOnce_MultiSessionTranscriptIngestion_OneShotSummarization(t *testing.T) {
	dir := t.TempDir()
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

	invoker := &stubInvokerCount{
		reply: `[{"hall":"fact","content":"Transcript distilled workspace summary","narrative":"3 sessions consolidated"}]`,
	}

	projectsRoot := filepath.Join(dir, "claude-projects")
	d := New(store, projectsRoot, summariesDB, invoker, invoker, locale.TargetLanguage{Code: "en"})

	workspacePath := "/tmp/project-multi-transcript"
	projectDir := filepath.Join(projectsRoot, "-tmp-project-multi-transcript")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Concurrently write 3 transcript files for 3 sessions in the workspace
	var wg sync.WaitGroup
	errCh := make(chan error, 3)
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessID := fmt.Sprintf("trans-sess-%d", idx)
			transcript := fmt.Sprintf(`{"type":"assistant","sessionId":"%s","cwd":"%s","message":{"content":[{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"make target-%d"}}]}}
{"type":"user","sessionId":"%s","cwd":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"t%d","content":"target %d done","is_error":false}]}}
`, sessID, workspacePath, idx, idx, sessID, workspacePath, idx, idx)
			if err := os.WriteFile(filepath.Join(projectDir, sessID+".jsonl"), []byte(transcript), 0o644); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("write transcript: %v", err)
	}

	base := time.Now()

	// First PollOnce: ingests observations from all 3 transcript files
	if err := d.PollOnce(context.Background(), base); err != nil {
		t.Fatalf("first PollOnce: %v", err)
	}

	wingID, err := store.GetOrCreateWing(workspacePath)
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	// Verify all 3 sessions have verbatim drawers
	verbatim, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(verbatim) != 3 {
		t.Fatalf("expected 3 verbatim drawers from 3 transcripts, got %d", len(verbatim))
	}

	// Verify 0 invoker calls during active capture
	if invoker.callCount != 0 {
		t.Fatalf("expected 0 invoker calls during active capture, got %d", invoker.callCount)
	}

	// Advance clock past idleWindow
	now := base.Add(idleWindow + time.Second)

	// Second PollOnce: debounces and executes exactly 1 summarization call
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}
	if invoker.callCount != 1 {
		t.Fatalf("expected exactly 1 invoker call, got %d", invoker.callCount)
	}

	// Verify summary drawer is stored
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary drawer, got %d", len(summaries))
	}
	if summaries[0].Content != "Transcript distilled workspace summary" {
		t.Fatalf("unexpected summary content: %q", summaries[0].Content)
	}

	// Verify all 3 sessions' verbatim rows are pruned
	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 unsummarized verbatim rows remaining, got %d", len(remaining))
	}
}

// TestSummarizeWing_NilInvoker_SafeNoOp verifies that summarizeWing safely returns nil
// without panicking when no invoker is configured.
func TestSummarizeWing_NilInvoker_SafeNoOp(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	wingPath := "/tmp/nil-invoker-ws"
	wingID, err := store.GetOrCreateWing(wingPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now()
	if err := store.InsertVerbatimDrawer(wingID, "sess-1", "Bash", "ls -la", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}

	// Daemon with no invokers configured
	d := New(store, t.TempDir(), filepath.Join(t.TempDir(), "conv.db"), nil, nil)

	// Should not panic and return nil
	if err := d.summarizeWing(context.Background(), wingID, now); err != nil {
		t.Fatalf("expected nil error on nil invoker, got: %v", err)
	}

	// Verbatim row should still be intact (not pruned without summarization)
	remaining, err := store.UnsummarizedVerbatimByWing(wingID, 10)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected verbatim row to remain unsummarized, got %d", len(remaining))
	}
}
