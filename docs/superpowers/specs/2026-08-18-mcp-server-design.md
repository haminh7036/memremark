# MemRemark MCP Server Design Specification

## 1. Problem Statement & Motivation

MemRemark currently operates as a background daemon (`memremarkd`) and passive hook system (`SessionStart` and `PreInvocation` hooks). While this automatically provides recent context into LLM prompts without token overhead, AI agents (Claude Code, Antigravity CLI, Cursor, etc.) currently lack an interactive mechanism to:
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
- **Seamless Workspace Scoping**: Automatically resolve current workspace (`wing`) from tool parameters or process working directory (`cwd`).
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
                   JSON-RPC 2.0   | (stdin / stdout)
                                  v
   +-------------------------------------------------------------+
   |                     cmd/memremark-mcp                       |
   |                                                             |
   |   +-----------------------------------------------------+   |
   |   |                  internal/mcp                       |   |
   |   |   - Protocol Server (initialize, tools/list, call)  |   |
   |   |   - Tool Handlers (search, remember, timeline, etc) |   |
   |   +--------------------------+--------------------------+   |
   +------------------------------|------------------------------+
                                  |
                                  v
   +-------------------------------------------------------------+
   |                    internal/storage                         |
   |   - SearchDrawers, GetTimeline, DeleteDrawer, InsertDrawer  |
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
- `limit` (integer, optional): Maximum number of results to return (default: `10`, max: `50`).

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
- `content` (string, required): The knowledge item or decision to record.
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
- `limit` (integer, optional): Maximum entries to return (default: `20`, max: `100`).

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
- `id` (integer, required): ID of the drawer to delete.

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
- If `query != ""`, adds `content LIKE ?` (with `%query%`).
- If `hall != ""`, adds `hall = ?`.
- If `drawerType == "summary"` or `"verbatim"`, adds `type = ?`. If `"all"` or empty, queries all types.
- Orders by `created_at DESC, id DESC LIMIT ?`.

### 5.2. `InsertManualSummary`
```go
func (s *Store) InsertManualSummary(wingID int64, hall, content string, createdAt time.Time) (int64, error)
```
- Inserts a summary drawer with `session_id = "manual"`, `covers_from = createdAt`, `covers_to = createdAt`.
- Returns the generated drawer ID.

### 5.3. `GetTimeline`
```go
func (s *Store) GetTimeline(wingID int64, sessionID string, since time.Time, limit int) ([]Drawer, error)
```
- Queries drawers ordered by `created_at ASC, id ASC LIMIT ?` matching `wing_id`, `session_id`, and `created_at >= ?`.

### 5.4. `DeleteDrawer`
```go
func (s *Store) DeleteDrawer(id int64) (bool, error)
```
- Executes `DELETE FROM drawers WHERE id = ?`.
- Returns `true` if a row was affected, `false` if not found.

---

## 6. MCP Protocol Implementation (`internal/mcp/`)

The `internal/mcp` package implements standard MCP JSON-RPC 2.0 over `io.Reader` and `io.Writer`:

```go
type Server struct {
	store *storage.Store
	in    io.Reader
	out   io.Writer
}
```

### Supported RPC Methods:
1. `initialize`:
   - Returns protocol version `"2024-11-05"`, server info (`name: "memremark-mcp"`, `version: "1.0.0"`), and capabilities (`tools: {}`).
2. `notifications/initialized`:
   - Acknowledgement, no response required.
3. `tools/list`:
   - Returns list of 4 tools with standard JSON Schema `inputSchema`.
4. `tools/call`:
   - Validates arguments, dispatches to tool implementation, returns `{content: [{type: "text", text: result}]}` or `{isError: true, content: [{type: "text", text: errMsg}]}`.
5. `ping`:
   - Returns empty object `{}`.

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
   - Check/update `~/.claude/mcp.json` or `.mcp.json` to register `"memremark": { "command": "~/.local/bin/memremark-mcp" }`.
3. Support `--cli=all`, `--cli=claude-code`, `--cli=antigravity-cli`, and `--uninstall`.

---

## 8. Testing Strategy

1. **Storage Unit Tests (`internal/storage/drawers_test.go`)**:
   - Test `SearchDrawers` with various query/hall/type filters.
   - Test `InsertManualSummary` and verify drawer fields and returned ID.
   - Test `GetTimeline` ordering and session filtering.
   - Test `DeleteDrawer` for existing and non-existent IDs.
2. **MCP Server Protocol Unit Tests (`internal/mcp/server_test.go`)**:
   - Test `initialize` request/response handshake.
   - Test `tools/list` schema validation.
   - Test `tools/call` for each of the 4 tools (`search_memory`, `remember`, `get_timeline`, `forget_memory`).
   - Test error handling for invalid tool names, malformed JSON, and missing required parameters.
3. **End-to-End Stdio Test (`cmd/memremark-mcp/main_test.go`)**:
   - Pipe JSON-RPC messages via stdin and assert stdout responses.
