# Antigravity Real-Time Live Session Discovery Design

- **Issue:** [#2 - [RFC] Real-time Antigravity live session discovery and direct API invoker to reduce peak memory](https://github.com/haminh7036/memremark/issues/2)
- **Author:** Antigravity & User Pair Programming
- **Date:** 2026-08-26
- **Status:** Approved / In Review

---

## 1. Overview & Problem Statement

Currently, `memremarkd` discovers Antigravity CLI conversations exclusively through `~/.gemini/antigravity-cli/conversation_summaries.db` (`antigravity.ListConversations`).

### Problem with Exclusively Index-Based Discovery
1. **Delayed Capture:** Antigravity CLI (`agy`) only writes/updates `conversation_summaries.db` when a session is closed or exited. While a session is actively running, `agy` writes steps directly to `~/.gemini/antigravity-cli/conversations/<id>.db` (with SQLite WAL mode: `<id>.db-wal`).
2. **Batch Lag:** Live interactions in Antigravity CLI are not captured in real time; observations accumulate as an unindexed backlog and are processed only after the user terminates the session.

### Goals
- **Real-Time Discovery:** Directly scan `~/.gemini/antigravity-cli/conversations/*.db` so newly opened and active Antigravity conversations are detected on the very next poll tick (~3s).
- **Hybrid Workspace Path Resolution:** Automatically resolve the canonical workspace path (`wing.Path`) for active sessions before they appear in `conversation_summaries.db` by extracting URI metadata from initial step payloads and matching against known registered wings.
- **Incremental Live Distillation:** Tail observations from active sessions step-by-step and trigger debounced summarization while the session remains open.
- **Concurrency & Lock Safety:** Open all conversation databases in read-only mode (`mode=ro`) with SQLite busy timeout to avoid write lock contention with active `agy` CLI processes.

---

## 2. Architecture & Components

### 2.1. File-Based Conversation Discovery (`internal/adapter/antigravity/reader.go`)

Add `DiscoverConversationDBs`:
```go
// DiscoverConversationDBs scans the conversations directory for all *.db files
// and returns a slice of conversation IDs (extracted from <id>.db filename).
func DiscoverConversationDBs(conversationsDir string) ([]string, error)
```

- Tolerates missing directory (returns `nil, nil`).
- Filters out non-`.db` files (e.g. `.db-wal`, `.db-shm`, `.tmp`).
- Extracts conversation ID from `strings.TrimSuffix(entry.Name(), ".db")`.

### 2.2. Workspace Path Extraction from Live DBs (`internal/adapter/antigravity/reader.go`)

Add `ExtractWorkspacePathFromDB`:
```go
// ExtractWorkspacePathFromDB inspects early step payloads in a conversation SQLite database
// to find a file:// URI or matches against a slice of known registered wing paths.
func ExtractWorkspacePathFromDB(conversationDBPath string, knownWings []string) string
```

**Algorithm:**
1. Open `conversationDBPath` in read-only mode (`file:<path>?mode=ro`).
2. Query early steps: `SELECT step_payload FROM steps ORDER BY idx ASC LIMIT 5`.
3. Extract embedded strings using `ExtractStrings(payload)` (from `protoscan.go`).
4. Look for strings starting with `file://` or containing `["file://..."]` and run `ExtractWorkspacePath`.
5. If no explicit `file://` URI is found, check if any extracted string contains an exact match or prefix match with `knownWings`, picking the longest match.
6. Return normalized canonical path (or empty string if not yet resolvable).

### 2.3. Daemon Polling Updates (`internal/daemon/daemon_antigravity.go`)

Update `pollAntigravity(now time.Time)`:
1. **Discover all conversation IDs on disk:**
   - Scan `conversationsDir := filepath.Join(filepath.Dir(d.antigravitySummariesDB), "conversations")` using `DiscoverConversationDBs`.
2. **Merge with `conversation_summaries.db`:**
   - If `conversation_summaries.db` exists, load index rows for metadata (known `WorkspaceURIs` and `LastModified`).
   - For conversation IDs present on disk but not yet in `conversation_summaries.db` (live active sessions):
     - Skip if `d.isSummarySession(convID)`.
     - Check cache `d.antigravityConvWing[convID]`. If missing, resolve via `ExtractWorkspacePathFromDB(dbPath, knownWings)`.
     - If resolved, cache path and use `os.Stat(dbPath).ModTime()` as `LastModified`.
3. **Stat-Based Change Detection:**
   - Check `os.Stat(dbPath)` (ModTime and Size) against `d.antigravityDBMeta[convID]`.
   - If changed or new:
     - Read new steps since last watermark: `ReadObservations(dbPath, cleanWingPath, convID, lastModified, sinceIdx)`.
     - Record observations into storage with `d.recordObservation(obs, d.antigravityInvoker, now)`.
     - Update watermark in memory and `poll_state` table (`antigravity:conv:<convID>`).

---

## 3. Storage Layer Additions

Add helper to `internal/storage/wings.go` (if not already present):
```go
// ListWingPaths returns the canonical paths of all registered wings.
func (s *Store) ListWingPaths() ([]string, error)
```

---

## 4. Error Handling & Invariants

1. **Read-Only SQLite Access:** Every database connection uses `mode=ro` and `PRAGMA busy_timeout = 5000;`. It cannot modify or lock Antigravity CLI's live SQLite databases.
2. **Crash & Restart Recovery:**
   - Watermarks stored in `poll_state` (`antigravity:conv:<id>`) ensure that restarting the daemon never re-reads already processed steps.
   - `d.Warmup()` ensures that any active session whose verbatim observations were written before daemon restart will still be debounced and summarized.
3. **Dedicated Summary Session Filtering:**
   - The daemon checks `d.isSummarySession(convID)` to ensure its own summarization requests are never ingested as observations.

---

## 5. Testing & Verification

1. **`DiscoverConversationDBs` Unit Tests (`internal/adapter/antigravity/reader_test.go`):**
   - Test scanning directory with multiple `.db`, `.db-wal`, `.db-shm`, and non-db files.
   - Test missing directory tolerance.
2. **`ExtractWorkspacePathFromDB` Unit Tests (`internal/adapter/antigravity/reader_test.go`):**
   - Test extracting `file:///path/to/project` from protobuf step payloads.
   - Test matching against `knownWings`.
3. **Daemon Live Session Discovery Integration Test (`internal/daemon/daemon_antigravity_test.go`):**
   - Simulate a newly created live session `.db` without modifying `conversation_summaries.db`.
   - Verify daemon discovers the live `.db`, extracts workspace path, records verbatim observations, and triggers debounced summarization.
