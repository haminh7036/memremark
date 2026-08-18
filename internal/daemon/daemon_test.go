package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encoding/binary"

	_ "modernc.org/sqlite"

	"github.com/haminh7036/memremark/internal/storage"
)

type stubInvoker struct {
	reply string
	err   error
}

func (s stubInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

// flakyInvoker fails its first `failures` calls with a transient-looking
// error, then succeeds -- simulating exactly the scenarios the design spec
// names (storage error, expired auth, network down, rate limiting).
type flakyInvoker struct {
	failures int
	calls    int
}

func (f *flakyInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	f.calls++
	if f.calls <= f.failures {
		return "", fmt.Errorf("transient failure")
	}
	return `[{"hall":"fact","content":"listed the project files"}]`, nil
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

// mustExecSQLite runs one statement against the SQLite database at path,
// opening and closing a fresh connection for it. Used to seed and mutate
// the small fixture databases these tests build by hand (conversation
// summaries index, per-conversation steps tables).
func mustExecSQLite(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db := mustOpenSQLite(t, path)
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q on %s: %v", query, path, err)
	}
}

// buildProtobufPromptBlob encodes text as a single length-delimited
// protobuf field, matching the shape antigravity.ExtractStrings recovers
// text from (see internal/adapter/antigravity/reader_test.go).
func buildProtobufPromptBlob(text string) []byte {
	var buf []byte
	buf = binary.AppendUvarint(buf, (1<<3)|2)
	buf = binary.AppendUvarint(buf, uint64(len(text)))
	return append(buf, text...)
}

// createSummariesDBWithConversation builds a conversation_summaries.db with
// one row. last_modified_time is declared TEXT (matching
// internal/adapter/antigravity/reader_test.go's fixture) rather than the
// DATETIME type createEmptySummariesDB above uses: modernc.org/sqlite
// auto-reformats DATETIME-affinity columns to RFC3339 ("...T...Z") on
// read, which antigravity.parseSQLiteDatetime's space-separated layout
// can't parse. createEmptySummariesDB never surfaces this because its
// existing callers never insert a row into it; these tests need a real
// row read back correctly, so they sidestep the quirk rather than fix
// it -- that's a pre-existing antigravity-package concern out of scope
// here (see task-10-report.md's Fix round 1 notes).
func createSummariesDBWithConversation(t *testing.T, path, conversationID, workspaceURIs, lastModified string) {
	t.Helper()
	db := mustOpenSQLite(t, path)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id text, workspace_uris text NOT NULL DEFAULT "",
		last_modified_time text NOT NULL, PRIMARY KEY (conversation_id)
	)`)
	if err != nil {
		t.Fatalf("create conversation_summaries: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO conversation_summaries (conversation_id, workspace_uris, last_modified_time) VALUES (?, ?, ?)`,
		conversationID, workspaceURIs, lastModified,
	)
	if err != nil {
		t.Fatalf("insert conversation summary: %v", err)
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

// TestPollOnceCapturesVerbatimFromAntigravityConversation exercises
// pollAntigravity's success path end to end: a real conversation_summaries.db
// row pointing at a real per-conversation steps db, read through
// antigravity.ReadObservations, landing as a verbatim drawer under the
// right wing/session -- the Antigravity-side mirror of
// TestPollOnceCapturesVerbatimFromClaudeCodeTranscript above, which only
// covered the Claude Code path.
func TestPollOnceCapturesVerbatimFromAntigravityConversation(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects") // no Claude Code transcripts in this test

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	antigravityDir := filepath.Join(dir, "antigravity")
	if err := os.MkdirAll(filepath.Join(antigravityDir, "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summariesDB := filepath.Join(antigravityDir, "conversation_summaries.db")
	createSummariesDBWithConversation(t, summariesDB, "conv-1", "/tmp/agy-project", "2026-08-11 10:00:00.000000000+00:00")

	conversationDB := filepath.Join(antigravityDir, "conversations", "conv-1.db")
	mustExecSQLite(t, conversationDB, `CREATE TABLE steps (idx integer, step_payload blob)`)
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (0, ?)`,
		buildProtobufPromptBlob("investigated the failing build"))

	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	wingID, err := store.GetOrCreateWing("/tmp/agy-project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	verbatim, err := store.VerbatimSince(wingID, "conv-1", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(verbatim) != 1 {
		t.Fatalf("expected 1 verbatim drawer from antigravity path, got %d", len(verbatim))
	}
	if verbatim[0].Content != "investigated the failing build" {
		t.Fatalf("unexpected verbatim content: %q", verbatim[0].Content)
	}
}

// TestPollOnceAntigravityErrorDoesNotAdvanceWatermark proves the guard
// flagged by Task 8's code review: antigravity.ReadObservations can return
// a non-nil error alongside a maxIdx that's already advanced past rows it
// didn't return (a mid-scan failure after some rows were already
// processed). The steps table below seeds two good rows (idx 0, 1) plus a
// row whose idx is TEXT rather than INTEGER, which sorts after the
// integers and makes rows.Scan fail once the query reaches it -- so
// ReadObservations returns (nil, 1, err), exactly the shape the guard in
// pollAntigravity has to handle correctly.
func TestPollOnceAntigravityErrorDoesNotAdvanceWatermark(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects")

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	antigravityDir := filepath.Join(dir, "antigravity")
	if err := os.MkdirAll(filepath.Join(antigravityDir, "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summariesDB := filepath.Join(antigravityDir, "conversation_summaries.db")
	createSummariesDBWithConversation(t, summariesDB, "conv-err", "/tmp/agy-broken", "2026-08-11 10:00:00.000000000+00:00")

	conversationDB := filepath.Join(antigravityDir, "conversations", "conv-err.db")
	mustExecSQLite(t, conversationDB, `CREATE TABLE steps (idx integer, step_payload blob)`)
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (0, ?)`,
		buildProtobufPromptBlob("obs zero"))
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (1, ?)`,
		buildProtobufPromptBlob("obs one"))
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES ('bad-idx', ?)`,
		buildProtobufPromptBlob("obs bad"))

	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})

	if err := d.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("first PollOnce: %v", err)
	}

	if idx, ok := d.antigravityLastIdx["conv-err"]; ok {
		t.Fatalf("expected no watermark stored after a failing poll, got %d", idx)
	}

	wingID, err := store.GetOrCreateWing("/tmp/agy-broken")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	verbatim, err := store.VerbatimSince(wingID, "conv-err", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(verbatim) != 0 {
		t.Fatalf("expected 0 verbatim drawers from the failing poll, got %d", len(verbatim))
	}

	// The underlying condition resolves (e.g. a later write finishes
	// cleanly): drop the bad row so the next poll's query succeeds.
	mustExecSQLite(t, conversationDB, `DELETE FROM steps WHERE typeof(idx) = 'text'`)

	if err := d.PollOnce(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}

	if idx, ok := d.antigravityLastIdx["conv-err"]; !ok || idx != 1 {
		t.Fatalf("expected watermark 1 after the recovered poll, got %d (ok=%v)", idx, ok)
	}

	verbatim, err = store.VerbatimSince(wingID, "conv-err", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince after recovery: %v", err)
	}
	if len(verbatim) != 2 {
		t.Fatalf("expected 2 verbatim drawers after the retried poll (no rows skipped), got %d", len(verbatim))
	}
}

// TestClaudeCodeOffsetSurvivesDaemonRestart proves Bug 2 is fixed for the
// Claude Code path: a fresh Daemon/Tailer sharing the same *storage.Store
// (simulating a process restart) must restore the persisted byte offset
// instead of re-reading the transcript from byte 0, which would duplicate
// every historical observation as a new verbatim drawer.
func TestClaudeCodeOffsetSurvivesDaemonRestart(t *testing.T) {
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

	// First daemon lifetime: processes the whole transcript once.
	d1 := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d1.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("first daemon PollOnce: %v", err)
	}

	// Simulate a restart: a brand-new Daemon (fresh in-memory Tailer/parser
	// state) sharing the same *storage.Store, with no new lines appended.
	d2 := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d2.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("second daemon PollOnce: %v", err)
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
		t.Fatalf("expected 1 verbatim drawer after restart (no re-processing of old lines), got %d", len(verbatim))
	}
}

// TestAntigravityWatermarkSurvivesDaemonRestart is the Antigravity-side
// mirror of TestClaudeCodeOffsetSurvivesDaemonRestart above: a fresh Daemon
// sharing the same *storage.Store must restore the persisted idx watermark
// instead of defaulting back to -1 and re-reading every step as new.
func TestAntigravityWatermarkSurvivesDaemonRestart(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects") // no Claude Code transcripts in this test

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	antigravityDir := filepath.Join(dir, "antigravity")
	if err := os.MkdirAll(filepath.Join(antigravityDir, "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summariesDB := filepath.Join(antigravityDir, "conversation_summaries.db")
	createSummariesDBWithConversation(t, summariesDB, "conv-restart", "/tmp/agy-restart", "2026-08-11 10:00:00.000000000+00:00")

	conversationDB := filepath.Join(antigravityDir, "conversations", "conv-restart.db")
	mustExecSQLite(t, conversationDB, `CREATE TABLE steps (idx integer, step_payload blob)`)
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (0, ?)`,
		buildProtobufPromptBlob("step zero"))
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (1, ?)`,
		buildProtobufPromptBlob("step one"))

	// First daemon lifetime: processes both steps.
	d1 := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d1.PollOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("first daemon PollOnce: %v", err)
	}

	// Simulate a restart: a brand-new Daemon (fresh in-memory
	// antigravityLastIdx map) sharing the same *storage.Store. A new step
	// arrives after the restart to confirm forward progress still works.
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (2, ?)`,
		buildProtobufPromptBlob("step two"))

	d2 := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})
	if err := d2.PollOnce(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("second daemon PollOnce: %v", err)
	}

	wingID, err := store.GetOrCreateWing("/tmp/agy-restart")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	verbatim, err := store.VerbatimSince(wingID, "conv-restart", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(verbatim) != 3 {
		t.Fatalf("expected 3 verbatim drawers (steps 0,1 from before the restart plus step 2 after), got %d", len(verbatim))
	}
}

// TestPollAntigravityPersistsWatermarkAfterWritingObservations is a
// regression test for a Critical bug introduced in the first pass of this
// fix: pollAntigravity's SetPollState call for the watermark ran BEFORE the
// loop that writes the batch's observations via recordObservation. A
// process crash in the window between "watermark persisted" and
// "observations actually written" would make the next restart's sinceIdx
// skip exactly the rows that were never durably recorded -- silent,
// permanent data loss, the same failure mode this whole fix wave exists to
// eliminate.
//
// A real crash mid-function can't be injected into pollAntigravity from a
// black-box test without adding a production-only fault-injection seam
// (recordObservation's writes have no natural per-row failure path to
// hook, and Store's single pooled connection means locking it externally
// blocks every write indiscriminately rather than isolating one). So this
// test instead pins the property directly at the source level: the
// SetPollState watermark persist must appear strictly after the
// recordObservation loop in daemon_antigravity.go, matching the ordering
// daemon_claudecode.go already used correctly. If this ordering regresses,
// this test fails.
func TestPollAntigravityPersistsWatermarkAfterWritingObservations(t *testing.T) {
	src, err := os.ReadFile("daemon_antigravity.go")
	if err != nil {
		t.Fatalf("read daemon_antigravity.go: %v", err)
	}
	text := string(src)

	loopIdx := strings.Index(text, "for _, o := range obs {")
	persistIdx := strings.Index(text, "d.Store.SetPollState(antigravityConvKey(conv.ID), maxIdx)")
	if loopIdx == -1 {
		t.Fatalf("could not find the recordObservation loop in daemon_antigravity.go")
	}
	if persistIdx == -1 {
		t.Fatalf("could not find the SetPollState watermark persist call in daemon_antigravity.go")
	}
	if persistIdx < loopIdx {
		t.Fatalf("SetPollState watermark persist (source offset %d) must come AFTER the recordObservation loop (source offset %d) -- persisting the watermark before the batch's observations are written reintroduces the crash-window data-loss bug", persistIdx, loopIdx)
	}
}

// TestPollOnceRetriesSummarizationAfterTransientFailure is the regression
// test for Critical 2: before the fix, Tracker.Due marked a session as fired
// as soon as it was returned, regardless of what the caller did with it
// afterward, so a summarizeSession failure (transient storage error, expired
// auth, network down, rate limiting) permanently dropped that session's
// pending summary once the session went quiet for good. The fix only
// consumes the session from the tracker once summarizeSession actually
// succeeds, so a failed attempt must be retried on the very next poll tick.
func TestPollOnceRetriesSummarizationAfterTransientFailure(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects")
	writeSampleTranscript(t, projectsRoot)

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	// Never created: pollAntigravity must tolerate this (Important 4).
	missingSummariesDB := filepath.Join(dir, "antigravity", "conversation_summaries.db")

	invoker := &flakyInvoker{failures: 1}
	d := New(store, projectsRoot, missingSummariesDB, invoker, stubInvoker{reply: "[]"})

	base := time.Now()
	if err := d.PollOnce(context.Background(), base); err != nil {
		t.Fatalf("first PollOnce (capture verbatim): %v", err)
	}

	wingID, err := store.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	// Session goes idle: this tick's summarization attempt fails.
	if err := d.PollOnce(context.Background(), base.Add(10*time.Second)); err != nil {
		t.Fatalf("second PollOnce (failing summarize): %v", err)
	}
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected no summary yet after a failed attempt, got %d", len(summaries))
	}

	// The very next poll tick (not a whole new idle window) must retry the
	// same session rather than having dropped it permanently.
	if err := d.PollOnce(context.Background(), base.Add(13*time.Second)); err != nil {
		t.Fatalf("third PollOnce (retry): %v", err)
	}
	summaries, err = store.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries after retry: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected the summary to land after the retry, got %d", len(summaries))
	}
	if summaries[0].Content != "listed the project files" {
		t.Fatalf("unexpected summary content: %q", summaries[0].Content)
	}
	if invoker.calls != 2 {
		t.Fatalf("expected exactly 2 invoker calls (1 failed + 1 retry), got %d", invoker.calls)
	}
}

// TestPollAntigravitySkipsWhenSummariesDBMissing is the regression test for
// Important 4: pollAntigravity must treat a missing
// antigravitySummariesDB (any machine that's never used Antigravity CLI) as
// "no conversations, nothing to do" -- not an error logged on every single
// poll tick.
func TestPollAntigravitySkipsWhenSummariesDBMissing(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	missingDB := filepath.Join(dir, "antigravity", "conversation_summaries.db") // never created
	d := New(store, filepath.Join(dir, "claude-projects"), missingDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	if err := d.pollAntigravity(time.Now()); err != nil {
		t.Fatalf("pollAntigravity: expected no error when summaries db is missing, got %v", err)
	}
	if logBuf.Len() != 0 {
		t.Fatalf("expected no log output when summaries db is missing, got %q", logBuf.String())
	}
}

// TestDaemon_SkipsRedundantPollStateWrites verifies that polling unchanged
// Claude Code transcripts and Antigravity conversation databases does not
// trigger redundant SetPollState database writes.
func TestDaemon_SkipsRedundantPollStateWrites(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects")
	writeSampleTranscript(t, projectsRoot)

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	antigravityDir := filepath.Join(dir, "antigravity")
	if err := os.MkdirAll(filepath.Join(antigravityDir, "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summariesDB := filepath.Join(antigravityDir, "conversation_summaries.db")
	createSummariesDBWithConversation(t, summariesDB, "conv-skip", "/tmp/agy-skip", "2026-08-11 10:00:00.000000000+00:00")

	conversationDB := filepath.Join(antigravityDir, "conversations", "conv-skip.db")
	mustExecSQLite(t, conversationDB, `CREATE TABLE steps (idx integer, step_payload blob)`)
	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (0, ?)`,
		buildProtobufPromptBlob("agy step zero"))

	claudeFile := filepath.Join(projectsRoot, "-tmp-project", "sess-1.jsonl")

	d := New(store, projectsRoot, summariesDB, stubInvoker{reply: "[]"}, stubInvoker{reply: "[]"})

	// Pass 1: initial scan processes both transcripts and persists watermarks
	base := time.Now()
	if err := d.PollOnce(context.Background(), base); err != nil {
		t.Fatalf("first PollOnce: %v", err)
	}

	claudeKey := claudeFileKey(claudeFile)
	claudeOffset1, ok, err := store.GetPollState(claudeKey)
	if err != nil || !ok || claudeOffset1 == 0 {
		t.Fatalf("expected claude poll state > 0, got %d (ok=%v, err=%v)", claudeOffset1, ok, err)
	}

	agyKey := antigravityConvKey("conv-skip")
	agyIdx1, ok, err := store.GetPollState(agyKey)
	if err != nil || !ok || agyIdx1 != 0 {
		t.Fatalf("expected agy poll state 0, got %d (ok=%v, err=%v)", agyIdx1, ok, err)
	}

	// Set sentinel values in poll_state. If the next poll tick skips redundant writes,
	// these sentinel values will not be overwritten.
	const sentinel int64 = 99999
	if err := store.SetPollState(claudeKey, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPollState(agyKey, sentinel); err != nil {
		t.Fatal(err)
	}

	// Pass 2: files are unchanged, polling must skip redundant SetPollState writes
	if err := d.PollOnce(context.Background(), base.Add(time.Second)); err != nil {
		t.Fatalf("second PollOnce: %v", err)
	}

	if val, _, _ := store.GetPollState(claudeKey); val != sentinel {
		t.Fatalf("expected claude poll_state to remain sentinel %d, got %d (redundant write occurred)", sentinel, val)
	}
	if val, _, _ := store.GetPollState(agyKey); val != sentinel {
		t.Fatalf("expected agy poll_state to remain sentinel %d, got %d (redundant write occurred)", sentinel, val)
	}

	// Restore correct poll state values
	if err := store.SetPollState(claudeKey, claudeOffset1); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPollState(agyKey, agyIdx1); err != nil {
		t.Fatal(err)
	}

	// Append new data to both sources
	f, err := os.OpenFile(claudeFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	newLine := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp/project","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"pwd"}}]}}` + "\n"
	if _, err := f.WriteString(newLine); err != nil {
		t.Fatal(err)
	}
	f.Close()
	touchTime := base.Add(2 * time.Second)
	_ = os.Chtimes(claudeFile, touchTime, touchTime)

	mustExecSQLite(t, conversationDB, `INSERT INTO steps (idx, step_payload) VALUES (1, ?)`,
		buildProtobufPromptBlob("agy step one"))
	_ = os.Chtimes(conversationDB, touchTime, touchTime)

	// Pass 3: modified files should be read and poll_state updated
	if err := d.PollOnce(context.Background(), base.Add(3*time.Second)); err != nil {
		t.Fatalf("third PollOnce: %v", err)
	}

	claudeOffset2, _, _ := store.GetPollState(claudeKey)
	if claudeOffset2 <= claudeOffset1 {
		t.Fatalf("expected claude poll state to advance past %d, got %d", claudeOffset1, claudeOffset2)
	}
	agyIdx2, _, _ := store.GetPollState(agyKey)
	if agyIdx2 != 1 {
		t.Fatalf("expected agy poll state 1, got %d", agyIdx2)
	}

	// Pass 4: unchanged again -> sentinel check
	const sentinel2 int64 = 88888
	if err := store.SetPollState(claudeKey, sentinel2); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPollState(agyKey, sentinel2); err != nil {
		t.Fatal(err)
	}

	if err := d.PollOnce(context.Background(), base.Add(4*time.Second)); err != nil {
		t.Fatalf("fourth PollOnce: %v", err)
	}

	if val, _, _ := store.GetPollState(claudeKey); val != sentinel2 {
		t.Fatalf("expected claude poll_state to remain sentinel2 %d, got %d", sentinel2, val)
	}
	if val, _, _ := store.GetPollState(agyKey); val != sentinel2 {
		t.Fatalf("expected agy poll_state to remain sentinel2 %d, got %d", sentinel2, val)
	}
}

