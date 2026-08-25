package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
