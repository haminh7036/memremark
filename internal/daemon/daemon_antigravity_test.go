package daemon

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
)

func setupTestStore(t *testing.T) (*storage.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memremark_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	return store, func() {
		_ = store.Close()
	}
}

func TestPollAntigravity_LiveSessionDiscovery(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	tmpDir := t.TempDir()
	convDir := filepath.Join(tmpDir, "conversations")
	_ = os.MkdirAll(convDir, 0o755)

	summariesDB := filepath.Join(tmpDir, "conversation_summaries.db")
	// summaries.db is completely empty / non-existent!

	// Create active live session DB
	convID := "live-session-123"
	convDBPath := filepath.Join(convDir, convID+".db")
	db, err := sql.Open("sqlite", convDBPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, _ = db.Exec("CREATE TABLE steps (idx integer, step_payload blob);")
	payload := []byte("\x12\x20file:///home/user/active-project/main.go")
	_, _ = db.Exec("INSERT INTO steps (idx, step_payload) VALUES (0, ?);", payload)
	_ = db.Close()

	// Seed registered wing
	wingID, err := store.GetOrCreateWing("/home/user/active-project")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	invoker := &stubInvoker{}
	d := New(store, t.TempDir(), summariesDB, invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})

	now := time.Now()
	if err := d.pollAntigravity(now); err != nil {
		t.Fatalf("pollAntigravity error: %v", err)
	}

	// Verify observations were recorded for live session
	verbatim, err := store.VerbatimSince(wingID, convID, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince error: %v", err)
	}
	if len(verbatim) == 0 {
		t.Fatalf("expected verbatim observations to be recorded for live session, got 0")
	}
}

func TestPollAntigravity_MergedDiscovery(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	tmpDir := t.TempDir()
	convDir := filepath.Join(tmpDir, "conversations")
	_ = os.MkdirAll(convDir, 0o755)

	summariesDB := filepath.Join(tmpDir, "conversation_summaries.db")
	createSummariesDBWithConversation(t, summariesDB, "indexed-conv", "/home/user/indexed-proj", "2026-08-26 10:00:00.000000000+00:00")

	// Indexed session DB
	indexedDBPath := filepath.Join(convDir, "indexed-conv.db")
	mustExecSQLite(t, indexedDBPath, "CREATE TABLE steps (idx integer, step_payload blob);")
	mustExecSQLite(t, indexedDBPath, "INSERT INTO steps (idx, step_payload) VALUES (0, ?);",
		buildProtobufPromptBlob("indexed observation"))

	// Live active session DB (not in summaries.db)
	liveDBPath := filepath.Join(convDir, "live-conv.db")
	mustExecSQLite(t, liveDBPath, "CREATE TABLE steps (idx integer, step_payload blob);")
	mustExecSQLite(t, liveDBPath, "INSERT INTO steps (idx, step_payload) VALUES (0, ?);",
		buildProtobufPromptBlob("file:///home/user/live-proj"))

	// Register wings
	indexedWingID, _ := store.GetOrCreateWing("/home/user/indexed-proj")
	liveWingID, _ := store.GetOrCreateWing("/home/user/live-proj")

	invoker := &stubInvoker{}
	d := New(store, t.TempDir(), summariesDB, invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})

	now := time.Now()
	if err := d.pollAntigravity(now); err != nil {
		t.Fatalf("pollAntigravity error: %v", err)
	}

	// Verify indexed conversation was polled
	indexedVerbatim, err := store.VerbatimSince(indexedWingID, "indexed-conv", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince indexed error: %v", err)
	}
	if len(indexedVerbatim) != 1 {
		t.Fatalf("expected 1 verbatim for indexed session, got %d", len(indexedVerbatim))
	}

	// Verify live active conversation was polled
	liveVerbatim, err := store.VerbatimSince(liveWingID, "live-conv", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince live error: %v", err)
	}
	if len(liveVerbatim) != 1 {
		t.Fatalf("expected 1 verbatim for live session, got %d", len(liveVerbatim))
	}

	// Verify caching
	if d.antigravityConvWing["live-conv"] != "/home/user/live-proj" {
		t.Errorf("expected cached wing path for live-conv, got %q", d.antigravityConvWing["live-conv"])
	}
}

func TestPollAntigravity_KnownWingsFallback(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	tmpDir := t.TempDir()
	convDir := filepath.Join(tmpDir, "conversations")
	_ = os.MkdirAll(convDir, 0o755)

	summariesDB := filepath.Join(tmpDir, "conversation_summaries.db")

	// Live DB without file:// URI, but with text matching a known wing path
	liveDBPath := filepath.Join(convDir, "fallback-conv.db")
	mustExecSQLite(t, liveDBPath, "CREATE TABLE steps (idx integer, step_payload blob);")
	mustExecSQLite(t, liveDBPath, "INSERT INTO steps (idx, step_payload) VALUES (0, ?);",
		buildProtobufPromptBlob("working on /home/user/matched-project/pkg/util.go"))

	wingID, _ := store.GetOrCreateWing("/home/user/matched-project")

	invoker := &stubInvoker{}
	d := New(store, t.TempDir(), summariesDB, invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})

	now := time.Now()
	if err := d.pollAntigravity(now); err != nil {
		t.Fatalf("pollAntigravity error: %v", err)
	}

	verbatim, err := store.VerbatimSince(wingID, "fallback-conv", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("VerbatimSince fallback error: %v", err)
	}
	if len(verbatim) != 1 {
		t.Fatalf("expected 1 verbatim for fallback matched wing, got %d", len(verbatim))
	}
}

