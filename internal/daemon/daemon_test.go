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
