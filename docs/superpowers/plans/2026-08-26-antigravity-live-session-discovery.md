# Antigravity Real-Time Live Session Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable real-time discovery and incremental observation tailing for active, ongoing Antigravity CLI sessions by directly scanning `conversations/*.db` and extracting workspace paths before sessions close.

**Architecture:** Extend `internal/adapter/antigravity` with filesystem discovery (`DiscoverConversationDBs`) and early-step payload inspection (`ExtractWorkspacePathFromDB`). Update `internal/daemon/daemon_antigravity.go` to merge filesystem conversations with `conversation_summaries.db` index and tail active `.db` files incrementally.

**Tech Stack:** Go 1.22+, `modernc.org/sqlite` (read-only mode), standard library `os`, `path/filepath`, `time`.

## Global Constraints

- Safe SQLite read-only access (`mode=ro` and `PRAGMA busy_timeout = 5000;`) on all live databases.
- Full backwards compatibility with `conversation_summaries.db` index.
- Filter out dedicated summary sessions (`isSummarySession`) to prevent recursive feedback loops.
- Standard library and existing dependencies only.

---

### Task 1: Implement `DiscoverConversationDBs` and `ExtractWorkspacePathFromDB` in `internal/adapter/antigravity`

**Files:**
- Modify: `internal/adapter/antigravity/reader.go`
- Test: `internal/adapter/antigravity/reader_test.go`

**Interfaces:**
- Produces:
  - `func DiscoverConversationDBs(conversationsDir string) ([]string, error)`
  - `func ExtractWorkspacePathFromDB(conversationDBPath string, knownWings []string) string`

- [ ] **Step 1: Write failing unit tests in `internal/adapter/antigravity/reader_test.go`**

```go
func TestDiscoverConversationDBs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create simulated conversation databases and auxiliary files
	_ = os.WriteFile(filepath.Join(tmpDir, "conv-1.db"), []byte("dummy"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "conv-2.db"), []byte("dummy"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "conv-1.db-wal"), []byte("wal"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "other.txt"), []byte("txt"), 0o644)

	ids, err := DiscoverConversationDBs(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sort.Strings(ids)
	expected := []string{"conv-1", "conv-2"}
	if len(ids) != len(expected) || ids[0] != expected[0] || ids[1] != expected[1] {
		t.Fatalf("expected %v, got %v", expected, ids)
	}

	// Missing directory should return empty slice and no error
	missingIDs, err := DiscoverConversationDBs(filepath.Join(tmpDir, "nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error on missing dir: %v", err)
	}
	if len(missingIDs) != 0 {
		t.Fatalf("expected 0 ids on missing dir, got %d", len(missingIDs))
	}
}

func TestExtractWorkspacePathFromDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-conv.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, _ = db.Exec("CREATE TABLE steps (idx integer, step_payload blob);")

	// Payload with encoded file:// URI
	payload := []byte("\x12\x20file:///home/user/myproject/code.go")
	_, err = db.Exec("INSERT INTO steps (idx, step_payload) VALUES (0, ?);", payload)
	db.Close()
	if err != nil {
		t.Fatalf("insert step: %v", err)
	}

	path := ExtractWorkspacePathFromDB(dbPath, []string{"/home/user/myproject"})
	if path != "/home/user/myproject/code.go" && path != "/home/user/myproject" {
		t.Errorf("unexpected extracted path: %q", path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/adapter/antigravity -run "TestDiscoverConversationDBs|TestExtractWorkspacePathFromDB"`
Expected: FAIL (functions undefined)

- [ ] **Step 3: Implement functions in `internal/adapter/antigravity/reader.go`**

```go
// DiscoverConversationDBs scans the conversations directory for all *.db files
// and returns a slice of conversation IDs (extracted from <id>.db filename).
func DiscoverConversationDBs(conversationsDir string) ([]string, error) {
	entries, err := os.ReadDir(conversationsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("antigravity: read conversations dir %s: %w", conversationsDir, err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".db") && !strings.HasSuffix(name, ".db-wal") && !strings.HasSuffix(name, ".db-shm") {
			convID := strings.TrimSuffix(name, ".db")
			if convID != "" {
				ids = append(ids, convID)
			}
		}
	}
	return ids, nil
}

// ExtractWorkspacePathFromDB inspects early step payloads in a conversation SQLite database
// to find a file:// URI or matches against a slice of known registered wing paths.
func ExtractWorkspacePathFromDB(conversationDBPath string, knownWings []string) string {
	db, err := openReadOnly(conversationDBPath)
	if err != nil {
		return ""
	}
	defer db.Close()

	rows, err := db.Query(`SELECT step_payload FROM steps ORDER BY idx ASC LIMIT 10`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var candidateStrings []string
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err == nil && len(payload) > 0 {
			strs := ExtractStrings(payload)
			candidateStrings = append(candidateStrings, strs...)
		}
	}

	// 1. First priority: look for explicit file:// URIs or ["file://..."]
	for _, s := range candidateStrings {
		if strings.Contains(s, "file://") {
			if idx := strings.Index(s, "file://"); idx >= 0 {
				rawURI := s[idx:]
				if endIdx := strings.IndexAny(rawURI, " \t\r\n\"'"); endIdx > 0 {
					rawURI = rawURI[:endIdx]
				}
				if cleaned := ExtractWorkspacePath(rawURI); cleaned != "" {
					return cleaned
				}
			}
		}
	}

	// 2. Second priority: match against known registered wings
	var longestMatch string
	for _, s := range candidateStrings {
		for _, wing := range knownWings {
			cleanWing := filepath.Clean(wing)
			if cleanWing != "" && strings.Contains(s, cleanWing) {
				if len(cleanWing) > len(longestMatch) {
					longestMatch = cleanWing
				}
			}
		}
	}
	if longestMatch != "" {
		return longestMatch
	}

	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/adapter/antigravity/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/antigravity/reader.go internal/adapter/antigravity/reader_test.go
git commit -m "feat(adapter/antigravity): add DiscoverConversationDBs and ExtractWorkspacePathFromDB"
```

---

### Task 2: Implement `Store.ListWingPaths` in `internal/storage/wings.go`

**Files:**
- Modify: `internal/storage/wings.go`
- Test: `internal/storage/wings_test.go`

**Interfaces:**
- Produces: `func (s *Store) ListWingPaths() ([]string, error)`

- [ ] **Step 1: Write unit test in `internal/storage/wings_test.go`**

```go
func TestListWingPaths(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	_, _ = store.GetOrCreateWing("/home/user/projectA", "projectA")
	_, _ = store.GetOrCreateWing("/home/user/projectB", "projectB")

	paths, err := store.ListWingPaths()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/storage -run TestListWingPaths`
Expected: FAIL (method undefined)

- [ ] **Step 3: Implement `ListWingPaths` in `internal/storage/wings.go`**

```go
// ListWingPaths returns the canonical paths of all registered wings in the store.
func (s *Store) ListWingPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM wings ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list wing paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("storage: scan wing path: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/storage/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/wings.go internal/storage/wings_test.go
git commit -m "feat(storage): add ListWingPaths to retrieve all registered workspace paths"
```

---

### Task 3: Update `pollAntigravity` in `internal/daemon/daemon_antigravity.go`

**Files:**
- Modify: `internal/daemon/daemon_antigravity.go`
- Modify: `internal/daemon/daemon.go` (add caching field `antigravityConvWing map[string]string` if needed)
- Test: `internal/daemon/daemon_antigravity_test.go` or `internal/daemon/daemon_test.go`

- [ ] **Step 1: Write test for live session discovery in `internal/daemon/daemon_antigravity_test.go`**

```go
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
	_, _ = store.GetOrCreateWing("/home/user/active-project", "active-project")

	invoker := &stubInvoker{}
	d := New(store, t.TempDir(), summariesDB, invoker, invoker, locale.TargetLanguage{Code: "en", Name: "English"})

	now := time.Now()
	if err := d.pollAntigravity(now); err != nil {
		t.Fatalf("pollAntigravity error: %v", err)
	}

	// Verify observations were recorded for live session
	count, err := store.CountVerbatimDrawers()
	if err != nil {
		t.Fatalf("count verbatim error: %v", err)
	}
	if count == 0 {
		t.Fatalf("expected verbatim observations to be recorded for live session, got 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/daemon -run TestPollAntigravity_LiveSessionDiscovery`
Expected: FAIL (live session not picked up without summaries.db)

- [ ] **Step 3: Update `internal/daemon/daemon_antigravity.go` and `internal/daemon/daemon.go`**

Add `antigravityConvWing map[string]string` in `Daemon` struct.
In `pollAntigravity`:
- Scan `DiscoverConversationDBs(conversationsDir)`.
- Merge with `antigravity.ListConversations(summariesDB)`.
- For live sessions without indexed `WorkspaceURIs`, resolve via `ExtractWorkspacePathFromDB(dbPath, knownWings)`.
- Record observations and update watermarks.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/daemon/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/daemon_antigravity.go internal/daemon/daemon_antigravity_test.go
git commit -m "feat(daemon): enable real-time active Antigravity session discovery and observation tailing"
```

---

### Task 4: End-to-End Verification & Issue #2 Closure

**Files:**
- Verify: Full repo build and test suite
- Update: GitHub Issue #2

- [ ] **Step 1: Run full test suite & race detector**

```bash
go build ./...
go test -v -race ./...
```

- [ ] **Step 2: Close GitHub Issue #2**

```bash
gh issue close 2 --comment "Completed: Real-time Antigravity live session discovery and direct API invokers are fully implemented, tested, and released."
```
