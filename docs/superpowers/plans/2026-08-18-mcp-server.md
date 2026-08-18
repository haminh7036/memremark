# MemRemark MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `memremark-mcp` Model Context Protocol (MCP) server over stdio with 4 core tools (`search_memory`, `remember`, `get_timeline`, `forget_memory`), comprehensive edge-case protection against historical bugs, and automated installation.

**Architecture:** A lightweight JSON-RPC 2.0 stdio server (`internal/mcp` and `cmd/memremark-mcp`) connecting directly to SQLite (`~/.memremark/memremark.db`) via `internal/storage`. Uses `bufio.Reader` for large payload streams, `json.RawMessage` for polymorphic IDs, and strict `os.Stderr` logging to prevent stdout framing corruption.

**Tech Stack:** Go (Standard Library: `context`, `bufio`, `database/sql`, `encoding/json`, `fmt`, `io`, `os`, `path/filepath`, `strings`, `sync`, `time`, `testing`).

## Global Constraints

- Zero external dependencies outside the Go standard library.
- Thread-safe / stateless tool execution under `-race`.
- `os.Stdout` MUST ONLY contain valid JSON-RPC 2.0 messages; all logs/warnings go strictly to `os.Stderr`.
- Stream reader MUST use `bufio.Reader` (not `bufio.Scanner`) to support payloads >64KB safely.
- 100% test coverage for all edge cases with `go test -race ./...`.

---

### Task 1: Storage Layer Extensions (`internal/storage`)

**Files:**
- Modify: `internal/storage/drawers.go`
- Modify: `internal/storage/drawers_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s *Store) SearchDrawers(wingID int64, query, hall, drawerType string, limit int) ([]Drawer, error)
  func (s *Store) InsertManualSummary(wingID int64, hall, content string, createdAt time.Time) (int64, error)
  func (s *Store) GetTimeline(wingID int64, sessionID string, since time.Time, limit int) ([]Drawer, error)
  func (s *Store) DeleteDrawer(id int64) (bool, error)
  ```

- [ ] **Step 1: Write failing storage tests in `internal/storage/drawers_test.go`**

Add the test suite to `internal/storage/drawers_test.go`:

```go
func TestStore_SearchDrawers_Filters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws1")
	now := time.Now().Truncate(time.Second)

	_ = store.InsertSummaryDrawer(wingID, "s1", HallFact, "Fact about golang compiler", now, now, now)
	_ = store.InsertSummaryDrawer(wingID, "s1", HallAdvice, "Advice on memory management", now, now, now.Add(time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "s1", "Bash", "go build -o test", now.Add(2*time.Second))

	// Search by query
	res, err := store.SearchDrawers(wingID, "golang", "", "", 10)
	if err != nil || len(res) != 1 || res[0].Hall != HallFact {
		t.Fatalf("search query failed: got %v, err: %v", res, err)
	}

	// Search by hall
	res, err = store.SearchDrawers(wingID, "", HallAdvice, "", 10)
	if err != nil || len(res) != 1 || res[0].Hall != HallAdvice {
		t.Fatalf("search hall failed: got %v, err: %v", res, err)
	}

	// Search by type=verbatim
	res, err = store.SearchDrawers(wingID, "", "", "verbatim", 10)
	if err != nil || len(res) != 1 || res[0].ToolName != "Bash" {
		t.Fatalf("search verbatim failed: got %v, err: %v", res, err)
	}

	// Limit clamping
	res, err = store.SearchDrawers(wingID, "", "", "all", 0)
	if err != nil || len(res) != 3 {
		t.Fatalf("search limit clamping failed: got %d items", len(res))
	}
}

func TestStore_SearchDrawers_SpecialChars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_special")
	now := time.Now().Truncate(time.Second)

	_ = store.InsertSummaryDrawer(wingID, "s1", HallDiscovery, "Tiết kiệm 50% CPU với 'O'Reilly' optimization_v2", now, now, now)

	// Search with %, _, and quotes
	for _, q := range []string{"50%", "O'Reilly", "optimization_v2", "Tiết kiệm"} {
		res, err := store.SearchDrawers(wingID, q, "", "all", 10)
		if err != nil {
			t.Fatalf("SearchDrawers failed for %q: %v", q, err)
		}
		if len(res) != 1 {
			t.Fatalf("expected 1 match for %q, got %d", q, len(res))
		}
	}
}

func TestStore_InsertManualSummary_Validation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_manual")
	now := time.Now()

	// Invalid hall
	_, err = store.InsertManualSummary(wingID, "invalid_hall", "test", now)
	if err == nil {
		t.Fatalf("expected error for invalid hall, got nil")
	}

	// Valid hall
	id, err := store.InsertManualSummary(wingID, HallPreference, "Use atomic commits", now)
	if err != nil || id <= 0 {
		t.Fatalf("InsertManualSummary failed: id=%d, err=%v", id, err)
	}

	summaries, err := store.RecentSummaries(wingID, 5)
	if err != nil || len(summaries) != 1 || summaries[0].ID != id || summaries[0].Content != "Use atomic commits" {
		t.Fatalf("expected inserted summary in RecentSummaries: %v, err: %v", summaries, err)
	}
}

func TestStore_GetTimeline_Ordering(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_timeline")
	baseTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second)

	_ = store.InsertVerbatimDrawer(wingID, "session-1", "Bash", "event 1", baseTime.Add(10*time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "session-1", "ViewFile", "event 2", baseTime.Add(20*time.Second))
	_ = store.InsertSummaryDrawer(wingID, "session-1", HallFact, "summary 1", baseTime.Add(10*time.Second), baseTime.Add(20*time.Second), baseTime.Add(30*time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "session-2", "Bash", "other session event", baseTime.Add(15*time.Second))

	// Get timeline for session-1
	timeline, err := store.GetTimeline(wingID, "session-1", baseTime, 50)
	if err != nil {
		t.Fatalf("GetTimeline failed: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("expected 3 events for session-1, got %d", len(timeline))
	}
	if timeline[0].Content != "event 1" || timeline[1].Content != "event 2" || timeline[2].Content != "summary 1" {
		t.Fatalf("unexpected chronological order: %+v", timeline)
	}
}

func TestStore_DeleteDrawer_SuccessAndNotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_delete")
	id, _ := store.InsertManualSummary(wingID, HallAdvice, "Temporary advice", time.Now())

	deleted, err := store.DeleteDrawer(id)
	if err != nil || !deleted {
		t.Fatalf("expected drawer to be deleted: %v, err: %v", deleted, err)
	}

	// Delete again (non-existent)
	deletedAgain, err := store.DeleteDrawer(id)
	if err != nil || deletedAgain {
		t.Fatalf("expected deletedAgain to be false, got: %v, err: %v", deletedAgain, err)
	}
}

func TestStore_GetOrCreateWing_Concurrency(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	ids := make([]int64, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := store.GetOrCreateWing("/concurrent/project/path")
			if err != nil {
				errCh <- err
				return
			}
			ids[idx] = id
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrency error: %v", err)
	}

	for i := 1; i < 20; i++ {
		if ids[i] != ids[0] || ids[i] <= 0 {
			t.Fatalf("expected same wing ID for all concurrent calls, got: %v", ids)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/storage/...`
Expected: FAIL (compilation error: `SearchDrawers`, `InsertManualSummary`, `GetTimeline`, `DeleteDrawer` undefined)

- [ ] **Step 3: Implement storage methods in `internal/storage/drawers.go`**

Implement:
```go
// SearchDrawers queries drawers matching the filters, ordered newest first.
func (s *Store) SearchDrawers(wingID int64, query, hall, drawerType string, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 10
	} else if limit > 50 {
		limit = 50
	}

	var conditions []string
	var args []interface{}

	if wingID > 0 {
		conditions = append(conditions, "wing_id = ?")
		args = append(args, wingID)
	}
	if query != "" {
		conditions = append(conditions, "content LIKE ?")
		args = append(args, fmt.Sprintf("%%%s%%", query))
	}
	if hall != "" {
		conditions = append(conditions, "hall = ?")
		args = append(args, hall)
	}
	if drawerType == "summary" || drawerType == "verbatim" {
		conditions = append(conditions, "type = ?")
		args = append(args, drawerType)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	querySQL := fmt.Sprintf(
		`SELECT id, type, hall, tool_name, content, created_at FROM drawers
		 %s
		 ORDER BY created_at DESC, id DESC LIMIT ?`,
		whereClause,
	)
	args = append(args, limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: search drawers: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var drawerType string
		var toolName sql.NullString
		var createdAt int64
		if err := rows.Scan(&d.ID, &drawerType, &d.Hall, &toolName, &d.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan search row: %w", err)
		}
		d.ToolName = toolName.String
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// InsertManualSummary records a user or AI explicit summary drawer.
func (s *Store) InsertManualSummary(wingID int64, hall, content string, createdAt time.Time) (int64, error) {
	if !isValidHall(hall) {
		return 0, fmt.Errorf("storage: invalid hall %q", hall)
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	unixTime := createdAt.Truncate(time.Second).Unix()
	res, err := s.db.Exec(
		`INSERT INTO drawers (wing_id, type, hall, content, session_id, covers_from, covers_to, created_at)
		 VALUES (?, 'summary', ?, ?, 'manual', ?, ?, ?)`,
		wingID, hall, content, unixTime, unixTime, unixTime,
	)
	if err != nil {
		return 0, fmt.Errorf("storage: insert manual summary: %w", err)
	}
	return res.LastInsertId()
}

// GetTimeline retrieves chronological sequence of events for a wing/session.
func (s *Store) GetTimeline(wingID int64, sessionID string, since time.Time, limit int) ([]Drawer, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}

	var conditions []string
	var args []interface{}

	if wingID > 0 {
		conditions = append(conditions, "wing_id = ?")
		args = append(args, wingID)
	}
	if sessionID != "" {
		conditions = append(conditions, "session_id = ?")
		args = append(args, sessionID)
	}
	if !since.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, since.Unix())
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	querySQL := fmt.Sprintf(
		`SELECT id, type, hall, tool_name, content, created_at FROM drawers
		 %s
		 ORDER BY created_at ASC, id ASC LIMIT ?`,
		whereClause,
	)
	args = append(args, limit)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: get timeline: %w", err)
	}
	defer rows.Close()

	var out []Drawer
	for rows.Next() {
		var d Drawer
		var drawerType string
		var toolName sql.NullString
		var createdAt int64
		if err := rows.Scan(&d.ID, &drawerType, &d.Hall, &toolName, &d.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("storage: scan timeline row: %w", err)
		}
		d.ToolName = toolName.String
		d.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDrawer deletes a drawer by ID, returning true if deleted.
func (s *Store) DeleteDrawer(id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM drawers WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("storage: delete drawer: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: rows affected: %w", err)
	}
	return rowsAffected > 0, nil
}
```

- [ ] **Step 4: Run tests to verify PASS**

Run: `go test -race ./internal/storage/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/drawers.go internal/storage/drawers_test.go
git commit -m "feat(storage): add SearchDrawers, InsertManualSummary, GetTimeline and DeleteDrawer"
```

---

### Task 2: Implement MCP Protocol Server & Stream Safety (`internal/mcp`)

**Files:**
- Create: `internal/mcp/types.go`
- Create: `internal/mcp/server.go`
- Create: `internal/mcp/server_test.go`

**Interfaces:**
- Produces:
  ```go
  type Server struct
  func NewServer(store *storage.Store, in io.Reader, out io.Writer) *Server
  func (s *Server) Serve(ctx context.Context) error
  ```

- [ ] **Step 1: Write `internal/mcp/types.go`**

Define standard JSON-RPC 2.0 and MCP types:

```go
package mcp

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required,omitempty"`
	AdditionalProperties bool                `json:"additionalProperties"`
}

type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type ToolCallContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolCallResult struct {
	Content []ToolCallContent `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}
```

- [ ] **Step 2: Write tests in `internal/mcp/server_test.go`**

Write test suite covering all historical and MCP protocol edge cases:

```go
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func setupTestServer(t *testing.T) (*Server, *storage.Store, *bytes.Buffer) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	out := &bytes.Buffer{}
	in := &bytes.Buffer{}
	s := NewServer(store, in, out)
	return s, store, out
}

func sendRequest(s *Server, inBuf *bytes.Buffer, outBuf *bytes.Buffer, reqStr string) (*Response, error) {
	outBuf.Reset()
	inBuf.Reset()
	inBuf.WriteString(reqStr + "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.HandleLine(ctx, inBuf.Bytes()); err != nil {
		return nil, err
	}

	outBytes := bytes.TrimSpace(outBuf.Bytes())
	if len(outBytes) == 0 {
		return nil, nil // notification (no output)
	}

	var resp Response
	if err := json.Unmarshal(outBytes, &resp); err != nil {
		return nil, fmt.Errorf("parse response %q: %w", string(outBytes), err)
	}
	return &resp, nil
}

func TestMCP_Initialize(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	if err != nil || resp == nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if string(resp.ID) != "1" {
		t.Fatalf("expected id=1, got: %s", string(resp.ID))
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["protocolVersion"] != "2024-11-05" {
		t.Fatalf("unexpected protocolVersion: %v", resMap["protocolVersion"])
	}
}

func TestMCP_NotificationInitialized(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if err != nil {
		t.Fatalf("notification error: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected no response for notification, got: %+v", resp)
	}
}

func TestMCP_Ping(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":"ping-1","method":"ping"}`)
	if err != nil || resp == nil {
		t.Fatalf("ping failed: %v", err)
	}
	if string(resp.ID) != `"ping-1"` {
		t.Fatalf("expected id='ping-1', got: %s", string(resp.ID))
	}
}

func TestMCP_ToolsList(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if err != nil || resp == nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	tools := resMap["tools"].([]interface{})
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got: %d", len(tools))
	}
}

func TestMCP_Remember_And_Search(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	// Call remember
	remReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remember","arguments":{"content":"Use atomic commits","hall":"preference","wing_path":"/test/project"}}}`
	resp, err := sendRequest(s, in, out, remReq)
	if err != nil || resp == nil {
		t.Fatalf("remember call failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "Successfully recorded memory") {
		t.Fatalf("unexpected remember response: %v", firstItem["text"])
	}

	// Call search_memory
	searchReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_memory","arguments":{"query":"atomic","wing_path":"/test/project"}}}`
	resp, err = sendRequest(s, in, out, searchReq)
	if err != nil || resp == nil {
		t.Fatalf("search_memory call failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr = resMap["content"].([]interface{})
	firstItem = contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "Use atomic commits") {
		t.Fatalf("expected search match, got: %v", firstItem["text"])
	}
}

func TestMCP_Remember_InvalidHall(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	remReq := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"remember","arguments":{"content":"Test","hall":"invalid_hall"}}}`
	resp, err := sendRequest(s, in, out, remReq)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["isError"] != true {
		t.Fatalf("expected isError=true for invalid hall, got: %+v", resMap)
	}
}

func TestMCP_Remember_LargePayload(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	largeContent := strings.Repeat("A", 150000) // 150KB
	argsJSON, _ := json.Marshal(map[string]interface{}{
		"name": "remember",
		"arguments": map[string]interface{}{
			"content":   largeContent,
			"hall":      "fact",
			"wing_path": "/test/large",
		},
	})
	remReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":%s}`, string(argsJSON))

	resp, err := sendRequest(s, in, out, remReq)
	if err != nil || resp == nil {
		t.Fatalf("large payload remember failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["isError"] == true {
		t.Fatalf("unexpected error response for large payload: %+v", resMap)
	}
}

func TestMCP_GetTimeline_And_Forget(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	// Remember item
	remReq := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"remember","arguments":{"content":"Timeline fact","hall":"fact","wing_path":"/test/timeline"}}}`
	_, _ = sendRequest(s, in, out, remReq)

	// Timeline
	timeReq := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get_timeline","arguments":{"wing_path":"/test/timeline"}}}`
	resp, err := sendRequest(s, in, out, timeReq)
	if err != nil || resp == nil {
		t.Fatalf("get_timeline failed: %v", err)
	}

	// Forget ID 1
	forgetReq := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"forget_memory","arguments":{"id":1}}}`
	resp, err = sendRequest(s, in, out, forgetReq)
	if err != nil || resp == nil {
		t.Fatalf("forget_memory failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "has been deleted") {
		t.Fatalf("unexpected forget result: %v", firstItem["text"])
	}
}

func TestMCP_PolymorphicIDs(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	for _, idVal := range []string{`123`, `"str-id-456"`, `null`} {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"ping"}`, idVal)
		resp, err := sendRequest(s, in, out, req)
		if err != nil || resp == nil {
			t.Fatalf("ping with id %s failed: %v", idVal, err)
		}
		if string(resp.ID) != idVal {
			t.Fatalf("expected response id %s, got: %s", idVal, string(resp.ID))
		}
	}
}

func TestMCP_MalformedJSON(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{malformed json line`)
	if err != nil || resp == nil {
		t.Fatalf("expected error response, got err=%v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("expected parse error code %d, got: %+v", CodeParseError, resp.Error)
	}
}

func TestMCP_MethodNotFound(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":10,"method":"unknown/method"}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected method not found code %d, got: %+v", CodeMethodNotFound, resp.Error)
	}
}

func TestMCP_PathNormalization(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	// Insert into /test/norm
	_ = storeRemember(s, in, out, "Normalized Fact", "fact", "/test/norm")

	// Search with trailing slash /test/norm/
	searchReq := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"search_memory","arguments":{"wing_path":"/test/norm/"}}}`
	resp, err := sendRequest(s, in, out, searchReq)
	if err != nil || resp == nil {
		t.Fatalf("search failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "Normalized Fact") {
		t.Fatalf("expected normalized path to match: %v", firstItem["text"])
	}
}

func storeRemember(s *Server, in, out *bytes.Buffer, content, hall, path string) *Response {
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remember","arguments":{"content":%q,"hall":%q,"wing_path":%q}}}`, content, hall, path)
	resp, _ := sendRequest(s, in, out, req)
	return resp
}

func TestMCP_ConcurrentRequests(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	s := NewServer(store, nil, nil)
	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			inBuf := &bytes.Buffer{}
			outBuf := &bytes.Buffer{}
			req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"remember","arguments":{"content":"Concurrent %d","hall":"fact","wing_path":"/test/conc"}}}`, idx, idx)
			resp, err := sendRequest(s, inBuf, outBuf, req)
			if err != nil {
				errCh <- err
				return
			}
			if resp == nil || resp.Error != nil {
				errCh <- fmt.Errorf("unexpected resp for %d: %+v", idx, resp)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent error: %v", err)
	}
}
```

- [ ] **Step 3: Implement `internal/mcp/server.go`**

Write `internal/mcp/server.go` implementing `Server`, `HandleLine`, stream reader, and 4 tool handlers:

```go
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

type Server struct {
	store *storage.Store
	in    *bufio.Reader
	out   io.Writer
	mu    sync.Mutex
}

func NewServer(store *storage.Store, in io.Reader, out io.Writer) *Server {
	var reader *bufio.Reader
	if in != nil {
		reader = bufio.NewReader(in)
	}
	return &Server{
		store: store,
		in:    reader,
		out:   out,
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if s.in == nil {
		return fmt.Errorf("mcp: reader is nil")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := s.in.ReadBytes('\n')
		if len(line) > 0 {
			if handleErr := s.HandleLine(ctx, line); handleErr != nil {
				fmt.Fprintf(os.Stderr, "mcp: handle error: %v\n", handleErr)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) HandleLine(ctx context.Context, line []byte) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return s.sendResponse(&Response{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error: &RPCError{
				Code:    CodeParseError,
				Message: fmt.Sprintf("Parse error: %v", err),
			},
		})
	}

	// Notifications
	if len(req.ID) == 0 || string(req.ID) == "null" && strings.HasPrefix(req.Method, "notifications/") {
		return nil // No response for notifications
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(&req)
	case "notifications/initialized":
		return nil
	case "ping":
		return s.handlePing(&req)
	case "tools/list":
		return s.handleToolsList(&req)
	case "tools/call":
		return s.handleToolsCall(ctx, &req)
	default:
		return s.sendResponse(&Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeMethodNotFound,
				Message: fmt.Sprintf("Method %q not found", req.Method),
			},
		})
	}
}

func (s *Server) sendResponse(resp *Response) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.out == nil {
		return nil
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.out, "%s\n", data)
	return err
}

func (s *Server) handleInitialize(req *Request) error {
	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]interface{}{
				"name":    "memremark-mcp",
				"version": "0.1.0",
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
		},
	})
}

func (s *Server) handlePing(req *Request) error {
	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{},
	})
}

func (s *Server) handleToolsList(req *Request) error {
	tools := []Tool{
		{
			Name:        "search_memory",
			Description: "Search through distilled summaries and verbatim observations in the Memory Palace.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query":     {Type: "string", Description: "Keyword to match in memory content. If omitted, returns recent memories."},
					"hall":      {Type: "string", Description: "Filter by hall classification.", Enum: []string{"fact", "discovery", "preference", "advice", "event"}},
					"type":      {Type: "string", Description: "Filter by drawer type.", Enum: []string{"summary", "verbatim", "all"}},
					"wing_path": {Type: "string", Description: "Workspace path. Defaults to current directory."},
					"limit":     {Type: "integer", Description: "Max results to return (default: 10, max: 50)."},
				},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "remember",
			Description: "Explicitly record a new distilled knowledge item into the Memory Palace immediately.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"content":   {Type: "string", Description: "The knowledge item or decision to record."},
					"hall":      {Type: "string", Description: "Knowledge classification.", Enum: []string{"fact", "discovery", "preference", "advice"}},
					"wing_path": {Type: "string", Description: "Workspace path. Defaults to current directory."},
				},
				Required:             []string{"content", "hall"},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "get_timeline",
			Description: "Retrieve chronological sequence of events and summaries for a session or time window.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"session_id": {Type: "string", Description: "Specific session ID to inspect."},
					"wing_path":  {Type: "string", Description: "Workspace path. Defaults to current directory."},
					"since":      {Type: "integer", Description: "Unix timestamp in seconds to fetch events after."},
					"limit":      {Type: "integer", Description: "Max entries to return (default: 20, max: 100)."},
				},
				AdditionalProperties: false,
			},
		},
		{
			Name:        "forget_memory",
			Description: "Delete an outdated or incorrect memory drawer by its ID.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"id": {Type: "integer", Description: "ID of the drawer to delete."},
				},
				Required:             []string{"id"},
				AdditionalProperties: false,
			},
		},
	}

	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	})
}

func (s *Server) handleToolsCall(ctx context.Context, req *Request) error {
	var callParams struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &callParams); err != nil {
		return s.sendResponse(&Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
		})
	}

	var result ToolCallResult
	switch callParams.Name {
	case "search_memory":
		result = s.callSearchMemory(callParams.Arguments)
	case "remember":
		result = s.callRemember(callParams.Arguments)
	case "get_timeline":
		result = s.callGetTimeline(callParams.Arguments)
	case "forget_memory":
		result = s.callForgetMemory(callParams.Arguments)
	default:
		result = ToolCallResult{
			IsError: true,
			Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Unknown tool %q", callParams.Name)}},
		}
	}

	return s.sendResponse(&Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	})
}

func normalizePath(pathStr string) string {
	if pathStr == "" {
		cwd, err := os.Getwd()
		if err == nil {
			pathStr = cwd
		}
	}
	abs, err := filepath.Abs(pathStr)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(pathStr)
}

func (s *Server) callSearchMemory(args map[string]interface{}) ToolCallResult {
	query, _ := args["query"].(string)
	hall, _ := args["hall"].(string)
	drawerType, _ := args["type"].(string)
	wingPath, _ := args["wing_path"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}

	cleanPath := normalizePath(wingPath)
	wingID, err := s.store.GetOrCreateWing(cleanPath)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to resolve wing: %v", err)}}}
	}

	drawers, err := s.store.SearchDrawers(wingID, query, hall, drawerType, limit)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Search error: %v", err)}}}
	}

	if len(drawers) == 0 {
		return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("No memories found for wing %q.", cleanPath)}}}
	}

	var sb bytes.Buffer
	fmt.Fprintf(&sb, "Found %d memories in wing %q:\n", len(drawers), cleanPath)
	for _, d := range drawers {
		if d.ToolName != "" {
			fmt.Fprintf(&sb, "- [ID: %d] [%s:%s] %s (%s)\n", d.ID, d.Hall, d.ToolName, d.Content, d.CreatedAt.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Fprintf(&sb, "- [ID: %d] [%s] %s (%s)\n", d.ID, d.Hall, d.Content, d.CreatedAt.Format("2006-01-02 15:04:05"))
		}
	}

	return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: sb.String()}}}
}

func (s *Server) callRemember(args map[string]interface{}) ToolCallResult {
	content, _ := args["content"].(string)
	hall, _ := args["hall"].(string)
	wingPath, _ := args["wing_path"].(string)

	if strings.TrimSpace(content) == "" {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: "Missing required argument 'content'"}}}
	}
	if strings.TrimSpace(hall) == "" {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: "Missing required argument 'hall'"}}}
	}

	cleanPath := normalizePath(wingPath)
	wingID, err := s.store.GetOrCreateWing(cleanPath)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to resolve wing: %v", err)}}}
	}

	id, err := s.store.InsertManualSummary(wingID, hall, content, time.Now())
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to store memory: %v", err)}}}
	}

	return ToolCallResult{
		Content: []ToolCallContent{{
			Type: "text",
			Text: fmt.Sprintf("Successfully recorded memory [ID: %d] into hall %q for wing %q.", id, hall, cleanPath),
		}},
	}
}

func (s *Server) callGetTimeline(args map[string]interface{}) ToolCallResult {
	sessionID, _ := args["session_id"].(string)
	wingPath, _ := args["wing_path"].(string)
	limit := 20
	if l, ok := args["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}
	var since time.Time
	if sVal, ok := args["since"].(float64); ok && int64(sVal) > 0 {
		since = time.Unix(int64(sVal), 0)
	}

	cleanPath := normalizePath(wingPath)
	wingID, err := s.store.GetOrCreateWing(cleanPath)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to resolve wing: %v", err)}}}
	}

	timeline, err := s.store.GetTimeline(wingID, sessionID, since, limit)
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to get timeline: %v", err)}}}
	}

	if len(timeline) == 0 {
		return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: "No events found for the specified timeline criteria."}}}
	}

	var sb bytes.Buffer
	fmt.Fprintf(&sb, "Timeline for wing %q (%d events):\n", cleanPath, len(timeline))
	for i, d := range timeline {
		timeStr := d.CreatedAt.Format("15:04:05")
		if d.ToolName != "" {
			fmt.Fprintf(&sb, "%d. [%s] [verbatim:%s] %s\n", i+1, timeStr, d.ToolName, d.Content)
		} else {
			fmt.Fprintf(&sb, "%d. [%s] [summary:%s] %s\n", i+1, timeStr, d.Hall, d.Content)
		}
	}

	return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: sb.String()}}}
}

func (s *Server) callForgetMemory(args map[string]interface{}) ToolCallResult {
	idVal, ok := args["id"].(float64)
	if !ok || int64(idVal) <= 0 {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: "Argument 'id' must be a positive integer"}}}
	}

	deleted, err := s.store.DeleteDrawer(int64(idVal))
	if err != nil {
		return ToolCallResult{IsError: true, Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Failed to delete memory: %v", err)}}}
	}
	if !deleted {
		return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Memory drawer [ID: %d] was not found.", int64(idVal))}}}
	}

	return ToolCallResult{Content: []ToolCallContent{{Type: "text", Text: fmt.Sprintf("Memory drawer [ID: %d] has been deleted.", int64(idVal))}}}
}
```

- [ ] **Step 4: Run tests to verify PASS**

Run: `go test -race ./internal/mcp/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/types.go internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): implement MCP stdio server with comprehensive edge case test suite"
```

---

### Task 3: MCP Binary Entrypoint (`cmd/memremark-mcp`)

**Files:**
- Create: `cmd/memremark-mcp/main.go`
- Create: `cmd/memremark-mcp/main_test.go`

**Interfaces:**
- Produces: `memremark-mcp` binary

- [ ] **Step 1: Write `cmd/memremark-mcp/main.go`**

Implement binary entrypoint:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/haminh7036/memremark/internal/mcp"
	"github.com/haminh7036/memremark/internal/storage"
)

func main() {
	// Direct all log output to stderr so stdout remains pure JSON-RPC
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memremark-mcp: resolve home dir: %v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(home, ".memremark", "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memremark-mcp: open storage: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server := mcp.NewServer(store, os.Stdin, os.Stdout)
	if err := server.Serve(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "memremark-mcp: serve error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Write end-to-end test in `cmd/memremark-mcp/main_test.go`**

Test binary compilation and execution:

```go
package main

import (
	"os/exec"
	"testing"
)

func TestBinaryBuild(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build memremark-mcp binary: %s", string(out))
	}
}
```

- [ ] **Step 3: Run test to verify PASS**

Run: `go test -v ./cmd/memremark-mcp/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/memremark-mcp/main.go cmd/memremark-mcp/main_test.go
git commit -m "feat(cmd): add memremark-mcp binary entrypoint"
```

---

### Task 4: Build & Auto-Installation Integration (`Makefile`, `install.sh`)

**Files:**
- Modify: `Makefile`
- Modify: `install.sh`
- Modify: `README.md`

- [ ] **Step 1: Update `Makefile`**

Add `bin/memremark-mcp` to build target:

```makefile
build:
	go build -o bin/memremarkd ./cmd/memremarkd
	go build -o bin/memremark-hook-claude-sessionstart ./cmd/memremark-hook-claude-sessionstart
	go build -o bin/memremark-hook-antigravity-preinvocation ./cmd/memremark-hook-antigravity-preinvocation
	go build -o bin/memremark-mcp ./cmd/memremark-mcp
```

- [ ] **Step 2: Update `install.sh`**

Update `install.sh` to compile `memremark-mcp`, copy it to `~/.local/bin/`, and patch MCP configuration in `~/.claude/mcp.json` or `.mcp.json`.

- [ ] **Step 3: Run full verification suite with race detector**

Run: `go test -race ./...`
Expected: PASS across all packages.

Run: `make build`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add Makefile install.sh README.md
git commit -m "feat(install): add memremark-mcp to build targets, install.sh and docs"
```
