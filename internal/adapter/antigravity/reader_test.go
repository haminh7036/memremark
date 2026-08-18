package antigravity

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func createTestConversationDB(t *testing.T, path string, payloads [][]byte) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE steps (
		idx integer, step_type integer NOT NULL DEFAULT 0, status integer NOT NULL DEFAULT 0,
		has_subtrajectory numeric NOT NULL DEFAULT false, metadata blob, error_details blob,
		permissions blob, task_details blob, render_info blob, step_payload blob,
		step_format integer NOT NULL DEFAULT 0, PRIMARY KEY (idx)
	)`)
	if err != nil {
		t.Fatalf("create steps table: %v", err)
	}
	for i, p := range payloads {
		if _, err := db.Exec(`INSERT INTO steps (idx, step_payload) VALUES (?, ?)`, i, p); err != nil {
			t.Fatalf("insert step %d: %v", i, err)
		}
	}
}

func createTestSummariesDB(t *testing.T, path, conversationID, workspaceURIs, lastModified string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id text, workspace_uris text NOT NULL DEFAULT "",
		last_modified_time text NOT NULL, PRIMARY KEY (conversation_id)
	)`)
	if err != nil {
		t.Fatalf("create conversation_summaries table: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO conversation_summaries (conversation_id, workspace_uris, last_modified_time) VALUES (?, ?, ?)`,
		conversationID, workspaceURIs, lastModified,
	)
	if err != nil {
		t.Fatalf("insert conversation summary: %v", err)
	}
}

// createTestSummariesDBDatetimeColumn mirrors createTestSummariesDB but
// declares last_modified_time with SQLite type affinity `datetime`, matching
// the real Antigravity CLI schema. modernc.org/sqlite reformats values read
// back from a datetime-affinity column into RFC3339, unlike a text-affinity
// column which round-trips the string as-is.
func createTestSummariesDBDatetimeColumn(t *testing.T, path, conversationID, workspaceURIs, lastModified string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id text, workspace_uris text NOT NULL DEFAULT "",
		last_modified_time datetime NOT NULL, PRIMARY KEY (conversation_id)
	)`)
	if err != nil {
		t.Fatalf("create conversation_summaries table: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO conversation_summaries (conversation_id, workspace_uris, last_modified_time) VALUES (?, ?, ?)`,
		conversationID, workspaceURIs, lastModified,
	)
	if err != nil {
		t.Fatalf("insert conversation summary: %v", err)
	}
}

func buildProtobufPromptBlob(text string) []byte {
	var buf []byte
	buf = appendTag(buf, 1, wireBytes)
	buf = appendString(buf, text)
	return buf
}

func TestReadObservationsExtractsTextFromStepPayloads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation.db")
	createTestConversationDB(t, dbPath, [][]byte{
		buildProtobufPromptBlob("Append a line 'foo' to sample.txt"),
		nil, // a step with no payload must just be skipped
		buildProtobufPromptBlob("second step content"),
	})

	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	obs, maxIdx, err := ReadObservations(dbPath, "/tmp/project", "conv-1", at, -1)
	if err != nil {
		t.Fatalf("ReadObservations: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations (nil payload skipped), got %d", len(obs))
	}
	if maxIdx != 2 {
		t.Fatalf("expected maxIdx 2, got %d", maxIdx)
	}
	if obs[0].Content != "Append a line 'foo' to sample.txt" {
		t.Fatalf("unexpected first observation content: %q", obs[0].Content)
	}
	if obs[0].WingPath != "/tmp/project" || obs[0].SessionID != "conv-1" {
		t.Fatalf("unexpected wing/session on observation: %+v", obs[0])
	}
	if !obs[0].Timestamp.Equal(at) {
		t.Fatalf("expected timestamp %v, got %v", at, obs[0].Timestamp)
	}
}

func TestReadObservationsOnlyReturnsStepsAfterSinceIdx(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation.db")
	createTestConversationDB(t, dbPath, [][]byte{
		buildProtobufPromptBlob("first step"),
		buildProtobufPromptBlob("second step"),
	})

	at := time.Now()
	obs1, maxIdx1, err := ReadObservations(dbPath, "/tmp/project", "conv-1", at, -1)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(obs1) != 2 {
		t.Fatalf("expected 2 observations on first read, got %d", len(obs1))
	}
	if maxIdx1 != 1 {
		t.Fatalf("expected maxIdx 1, got %d", maxIdx1)
	}

	obs2, maxIdx2, err := ReadObservations(dbPath, "/tmp/project", "conv-1", at, maxIdx1)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(obs2) != 0 {
		t.Fatalf("expected no new observations on second read, got %d", len(obs2))
	}
	if maxIdx2 != maxIdx1 {
		t.Fatalf("expected maxIdx unchanged, got %d", maxIdx2)
	}
}

func TestListConversationsParsesRealTimestampFormat(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation_summaries.db")
	createTestSummariesDB(t, dbPath, "conv-1", "/home/minh/personal/memremark", "2026-07-17 03:45:48.875161831+00:00")

	convs, err := ListConversations(dbPath)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].WorkspaceURIs != "/home/minh/personal/memremark" {
		t.Fatalf("unexpected workspace: %q", convs[0].WorkspaceURIs)
	}
	want := time.Date(2026, 7, 17, 3, 45, 48, 875161831, time.UTC)
	if !convs[0].LastModified.Equal(want) {
		t.Fatalf("expected LastModified %v, got %v", want, convs[0].LastModified)
	}
}

// TestListConversationsParsesDatetimeAffinityColumn reproduces the real
// Antigravity CLI schema, where conversation_summaries.last_modified_time
// has SQLite type affinity `datetime` (not `text`). modernc.org/sqlite
// reformats values it reads back from such a column into RFC3339, so
// parseSQLiteDatetime must accept that shape too, not just the
// space-separated shape the text-affinity test above exercises.
func TestListConversationsParsesDatetimeAffinityColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation_summaries.db")
	createTestSummariesDBDatetimeColumn(t, dbPath, "conv-1", "/home/minh/personal/memremark", "2026-07-17 03:45:48.875161831+00:00")

	convs, err := ListConversations(dbPath)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	want := time.Date(2026, 7, 17, 3, 45, 48, 875161831, time.UTC)
	if !convs[0].LastModified.Equal(want) {
		t.Fatalf("expected LastModified %v, got %v", want, convs[0].LastModified)
	}
}

// TestOpenReadOnlyWorksAgainstExistingDatabase proves the switch to SQLite's
// mode=ro URI parameter didn't break the normal path: every existing query
// (ListConversations, ReadObservations) still works against a real,
// existing on-disk database opened through openReadOnly.
func TestOpenReadOnlyWorksAgainstExistingDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "conversation_summaries.db")
	createTestSummariesDB(t, dbPath, "conv-1", "/tmp/project", "2026-07-17 03:45:48.875161831+00:00")

	db, err := openReadOnly(dbPath)
	if err != nil {
		t.Fatalf("openReadOnly on existing db: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT count(*) FROM conversation_summaries").Scan(&count); err != nil {
		t.Fatalf("query through read-only handle: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}

	// Also exercise the real ListConversations call path, which routes
	// through openReadOnly.
	convs, err := ListConversations(dbPath)
	if err != nil {
		t.Fatalf("ListConversations through read-only open: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
}

// TestOpenReadOnlyFailsCleanlyOnMissingPath proves mode=ro closes the
// narrow write-on-delete race this fix targets: modernc.org/sqlite's
// default DSN silently creates a new empty file for a path that doesn't
// exist, which would be a real (if narrow-window) violation of "never
// write to files this package doesn't own" if the target .db is deleted
// between the daemon's os.Stat check and this open. With mode=ro, opening
// a missing path must fail with an error and must not create a file on
// disk.
func TestOpenReadOnlyFailsCleanlyOnMissingPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.db")

	db, err := openReadOnly(missing)
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("expected openReadOnly to fail for a missing path, got nil error")
	}

	if _, statErr := os.Stat(missing); statErr == nil {
		t.Fatalf("openReadOnly must not create a file for a missing path, but %s now exists", missing)
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("unexpected stat error checking for created file: %v", statErr)
	}
}
