# MemRemark MCP Server Design Specification

## 1. Problem Statement & Motivation

MemRemark currently operates as a background daemon (`memremarkd`) and passive hook system (`SessionStart` and `PreInvocation` hooks). While this automatically injects recent context into LLM prompts without token overhead, AI agents (Claude Code, Antigravity CLI, Cursor, etc.) currently lack an interactive mechanism to:
1. **Search deep memory**: Actively query past technical decisions (`fact`), previous bugs/workarounds (`discovery`), coding preferences (`preference`), and architectural recommendations (`advice`).
2. **Inspect raw verbatim logs**: Search raw tool calls and command outputs from previous sessions when debugging regressions.
3. **Remember explicitly**: Explicitly write a critical rule or decision into the Memory Palace immediately upon user instruction without waiting for idle daemon summarization.
4. **Invalidate outdated facts**: Delete or remove obsolete memories when architectures change.

The **Model Context Protocol (MCP)** provides an open standard for exposing tool interfaces to AI models via standard input/output (`stdio`). Adding a dedicated `memremark-mcp` binary enables active memory management across all MCP-compliant clients.

---

## 2. Goals & Non-Goals

### Goals
- **Core Lean Tool Suite**: Expose 4 focused, high-leverage MCP tools (`search_memory`, `remember`, `get_timeline`, `forget_memory`).
- **Stdio Transport (JSON-RPC 2.0)**: Pure Go standard library implementation of the MCP protocol over `stdin`/`stdout`.
- **Zero External Dependencies**: Use existing SQLite storage layer (`~/.memremark/memremark.db`) and Go standard library.
- **Fast & Lightweight**: Sub-millisecond tool execution directly querying SQLite.
- **Robust Edge-Case Resilience**: Guard against historical bugs in `memremark` (concurrency races, buffer overflows, large payloads, timestamp truncation, and framing issues).
- **Seamless Workspace Scoping**: Automatically normalize and resolve workspace (`wing`) from tool parameters or process working directory (`cwd`).
- **Automated Installation**: Integrate binary build and client MCP registration into `install.sh` and `Makefile`.

### Non-Goals
- Vector database / ChromaDB / heavy embedding models (SQLite exact/LIKE search is sufficient for v1; full-text search FTS5 can be added later if needed).
- Complex multi-agent consensus or room management (simple Wing -> Hall -> Drawer hierarchy per Core Engine v1).
- Cloud synchronization (deferred to Sync Layer).

---

## 3. System Architecture

```
   +-------------------------------------------------------------+
   |              MCP Client (Claude Code / Antigravity)         |
   +------------------------------+------------------------------+
                                  |
                   JSON-RPC 2.0   | (stdin / stdout via stdio)
                                  v
   +-------------------------------------------------------------+
   |                     cmd/memremark-mcp                       |
   |                                                             |
   |   +-----------------------------------------------------+   |
   |   |                  internal/mcp                       |   |
   |   |   - Protocol Server (initialize, tools/list, call)  |   |
   |   |   - Stream Framing & Large Payload Buffer Reader    |   |
   |   |   - Tool Handlers (search, remember, timeline, etc) |   |
   |   +--------------------------+--------------------------+   |
   +------------------------------|------------------------------+
                                  |
                                  v
   +-------------------------------------------------------------+
   |                    internal/storage                         |
   |   - SearchDrawers, GetTimeline, DeleteDrawer, InsertDrawer  |
   |   - Atomic GetOrCreateWing (ON CONFLICT DO NOTHING)         |
   |   - Single connection pool & PRAGMA busy_timeout = 5000     |
   +------------------------------+------------------------------+
                                  |
                                  v
   +-------------------------------------------------------------+
   |            SQLite: ~/.memremark/memremark.db                |
   +-------------------------------------------------------------+
```

---

## 4. MCP Tools Specification

### 4.1. `search_memory`
Search through distilled summaries and verbatim observations in the Memory Palace.

**Parameters:**
- `query` (string, optional): Search keyword to match in drawer content. If omitted, returns the most recent memories.
- `hall` (string, optional): Filter by hall (`fact`, `discovery`, `preference`, `advice`, `event`).
- `type` (string, optional): Filter by drawer type (`summary`, `verbatim`, or `all`). Default: `summary`.
- `wing_path` (string, optional): File path of the workspace. Defaults to the current working directory.
- `limit` (integer, optional): Maximum number of results to return (default: `10`, clamped between `1` and `50`).

**Response Format:**
Text output formatted with clear sections and IDs for easy referencing:
```markdown
Found 3 memories in wing '/home/minh/personal/memremark':
- [ID: 17215] [fact] Schema transcript Claude Code dùng cho parser memremark... (2026-08-18 08:41:27)
- [ID: 17208] [discovery] Hai nợ nhỏ đã ghi lại và hoãn (progress.md)... (2026-08-18 08:41:27)
- [ID: 17211] [advice] Khi daemon bắt đầu ghi đồng thời, thay SELECT-rồi-INSERT... (2026-08-18 08:41:27)
```

---

### 4.2. `remember`
Explicitly record a new distilled knowledge item into the Memory Palace immediately.

**Parameters:**
- `content` (string, required): The knowledge item or decision to record. Must not be empty.
- `hall` (string, required): Knowledge classification (`fact`, `discovery`, `preference`, `advice`).
- `wing_path` (string, optional): Target workspace path (defaults to current working directory).

**Response Format:**
```
Successfully recorded memory [ID: 17220] into hall 'fact' for wing '/home/minh/personal/memremark'.
```

---

### 4.3. `get_timeline`
Retrieve a chronological sequence of verbatim tool events and summaries for a session or time window to reconstruct diagnostic history.

**Parameters:**
- `session_id` (string, optional): Specific session ID to inspect.
- `wing_path` (string, optional): Target workspace path (defaults to current working directory).
- `since` (integer, optional): Unix timestamp in seconds to fetch events after.
- `limit` (integer, optional): Maximum entries to return (default: `20`, clamped between `1` and `100`).

**Response Format:**
```markdown
Timeline for session '2c26b2de-9d91-4e51-b559-e2604b60f71e' (15 events):
1. [08:39:10] [verbatim:Bash] go test ./...
2. [08:39:15] [verbatim:ViewFile] internal/summarizer/summarizer.go
3. [08:41:27] [summary:fact] Task 2 and Task 3 completed with clean review.
```

---

### 4.4. `forget_memory`
Delete an outdated or incorrect memory drawer by its ID.

**Parameters:**
- `id` (integer, required): ID of the drawer to delete (must be > 0).

**Response Format:**
```
Memory drawer [ID: 17215] has been deleted.
```

---

## 5. Storage Layer Extensions (`internal/storage/`)

Extend `internal/storage` with the following queries:

### 5.1. `SearchDrawers`
```go
func (s *Store) SearchDrawers(wingID int64, query, hall, drawerType string, limit int) ([]Drawer, error)
```
- If `wingID > 0`, filters by `wing_id = ?`.
- If `query != ""`, uses parameterized `content LIKE ?` (with `fmt.Sprintf("%%%s%%", query)`). Handles special characters (`%`, `_`, quotes, unicode) safely without SQL injection.
- If `hall != ""`, adds `hall = ?`.
- If `drawerType == "summary"` or `"verbatim"`, adds `type = ?`. If `"all"` or empty, queries all types.
- Clamps `limit` (`limit <= 0` defaults to 10; `limit > 50` clamped to 50).
- Orders by `created_at DESC, id DESC LIMIT ?` (deterministic tie-breaker).

### 5.2. `InsertManualSummary`
```go
func (s *Store) InsertManualSummary(wingID int64, hall, content string, createdAt time.Time) (int64, error)
```
- Validates `hall` against `isValidHall(hall)` (`fact`, `discovery`, `preference`, `advice`).
- Inserts a summary drawer with `session_id = "manual"`, `covers_from = createdAt`, `covers_to = createdAt`.
- Uses `createdAt.Truncate(time.Second)` to match SQLite Unix second timestamp convention.
- Returns the generated drawer ID via `res.LastInsertId()`.

### 5.3. `GetTimeline`
```go
func (s *Store) GetTimeline(wingID int64, sessionID string, since time.Time, limit int) ([]Drawer, error)
```
- Queries drawers matching `wing_id`, optional `session_id`, and `created_at >= since.Unix()`.
- Clamps `limit` (`limit <= 0` defaults to 20; `limit > 100` clamped to 100).
- Orders by `created_at ASC, id ASC LIMIT ?` (chronological sequence).

### 5.4. `DeleteDrawer`
```go
func (s *Store) DeleteDrawer(id int64) (bool, error)
```
- Executes `DELETE FROM drawers WHERE id = ?`.
- Returns `true` if `res.RowsAffected() > 0`, `false` if not found.

---

## 6. MCP Protocol Implementation & Stream Safety (`internal/mcp/`)

The `internal/mcp` package implements standard MCP JSON-RPC 2.0 over `io.Reader` and `io.Writer`:

```go
type Server struct {
	store *storage.Store
	in    *bufio.Reader
	out   io.Writer
	mu    sync.Mutex
}
```

### 6.1. Stdio Framing & Transport Safety (Addressing Historical Bugs)
- **Large Payload Streaming**: Does NOT use `bufio.Scanner` with default 64KB token limit. Uses `bufio.Reader.ReadBytes('\n')` to handle arbitrarily large payloads (>500KB markdown, code files, stack traces).
- **Line Ending Tolerance**: Handles `\r\n` (Windows/terminal emulators) and `\n` seamlessly by trimming trailing carriage return and whitespace.
- **Stdout Contamination Prevention**: Only JSON-RPC message responses are written to `os.Stdout`. All logging, debugging, and warning messages must be directed strictly to `os.Stderr`.
- **Polymorphic `id` Support**: Uses `json.RawMessage` for request/response `id` so `int`, `string`, and `null` IDs are preserved verbatim in response envelopes.
- **Notification Handling**: Methods starting with `notifications/` (e.g. `notifications/initialized`, `notifications/cancelled`) or requests without an `id` are processed silently with **zero** output written to `stdout`.

### 6.2. Supported RPC Methods
1. `initialize`:
   - Returns protocol version `"2024-11-05"`, server info (`name: "memremark-mcp"`, `version: "0.1.0"`), and capabilities (`tools: {}`).
2. `notifications/initialized`:
   - Acknowledgement, no response emitted.
3. `tools/list`:
   - Returns list of 4 tools with standard JSON Schema `inputSchema`.
4. `tools/call`:
   - Validates arguments:
     - Normalizes `wing_path` with `filepath.Clean` and `filepath.Abs` (falling back to `os.Getwd()`).
     - Validates `hall` enum and non-empty `content` for `remember`.
     - Validates `id > 0` for `forget_memory`.
   - Returns standard `{content: [{type: "text", text: result}]}` or `{isError: true, content: [{type: "text", text: errMsg}]}`.
5. `ping`:
   - Returns empty object `{}`.

### 6.3. Standard JSON-RPC Error Codes
- **Parse Error (`-32700`)**: For malformed JSON input.
- **Method Not Found (`-32601`)**: For unrecognized RPC methods.
- **Invalid Params (`-32602`)**: For missing required parameters or invalid types.

---

## 7. Build, Packaging & Auto-Installation

### 7.1. Makefile
Update `Makefile`:
```makefile
build:
	go build -o bin/memremarkd ./cmd/memremarkd
	go build -o bin/memremark-hook-claude-sessionstart ./cmd/memremark-hook-claude-sessionstart
	go build -o bin/memremark-hook-antigravity-preinvocation ./cmd/memremark-hook-antigravity-preinvocation
	go build -o bin/memremark-mcp ./cmd/memremark-mcp
```

### 7.2. Installation Script (`install.sh`)
Update `install.sh` to:
1. Compile and install `memremark-mcp` to `~/.local/bin/memremark-mcp`.
2. Configure Claude Code MCP:
   - Patch `~/.claude/mcp.json` or `.mcp.json` with:
     ```json
     {
       "mcpServers": {
         "memremark": {
           "command": "/home/minh/.local/bin/memremark-mcp"
         }
       }
     }
     ```
3. Support `--cli=all`, `--cli=claude-code`, `--cli=antigravity-cli`, and `--uninstall`.

---

## 8. Comprehensive Test Suite Matrix

| Nhóm Test | Test Case Cụ Thể | Mục Tiêu & Assertions |
|---|---|---|
| **Storage Queries** | `TestStore_SearchDrawers_Filters` | Test search with query, hall, type filters, and `limit` boundary clamping. |
| | `TestStore_SearchDrawers_SpecialChars` | Test search with `%`, `_`, `'`, `"`, Vietnamese unicode strings without SQL errors. |
| | `TestStore_InsertManualSummary_Validation` | Test inserting fact/discovery/preference/advice and assert returned ID and truncation. |
| | `TestStore_GetTimeline_Ordering` | Test timeline retrieval ordering (`created_at ASC, id ASC`) and `since` filter. |
| | `TestStore_DeleteDrawer_SuccessAndNotFound` | Test deleting existing ID (returns true) vs non-existing ID (returns false). |
| | `TestStore_GetOrCreateWing_Concurrency` | Run 20 goroutines creating the same workspace path concurrently under `-race`. |
| **Protocol Handshake** | `TestMCP_Initialize` | Verify protocol version `"2024-11-05"`, `capabilities.tools`, and `serverInfo`. |
| | `TestMCP_NotificationInitialized` | Verify no response is written to stdout for notifications. |
| | `TestMCP_Ping` | Verify `{}` response preserving request ID. |
| **Tool Discovery** | `TestMCP_ToolsList` | Verify returns 4 tools with correct JSON Schema parameters. |
| **Tool Execution** | `TestMCP_Remember_Valid` | Verify `remember` saves to DB, returns created ID and wing path. |
| | `TestMCP_Remember_InvalidHall` | Verify `remember` with invalid hall returns `isError: true` with error message. |
| | `TestMCP_Remember_LargePayload` | Send 200KB content; verify stream handles buffer without truncation. |
| | `TestMCP_SearchMemory_Execution` | Test search tool returns formatted markdown with IDs and timestamps. |
| | `TestMCP_GetTimeline_Execution` | Test timeline returns chronological list of events. |
| | `TestMCP_ForgetMemory_Execution` | Test forget tool removes drawer and handles non-existent ID. |
| **Transport & Framing** | `TestMCP_PolymorphicIDs` | Test integer, string, and null request IDs are preserved in responses. |
| | `TestMCP_LineEndings_CRLF` | Test parsing lines with `\r\n` and trailing whitespace. |
| | `TestMCP_MalformedJSON` | Send invalid JSON, verify `-32700` parse error response. |
| | `TestMCP_MethodNotFound` | Send unknown method, verify `-32601` error response. |
| | `TestMCP_PathNormalization` | Test `wing_path` with `.`, `./foo/..`, and trailing slashes maps to clean path. |
| | `TestMCP_ConcurrentRequests` | Execute 20 concurrent tool requests over server instance under `go test -race`. |
