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
	"github.com/haminh7036/memremark/internal/version"
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
	serverInfo, ok := resMap["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing or invalid serverInfo: %v", resMap["serverInfo"])
	}
	if serverInfo["name"] != "memremark-mcp" || serverInfo["version"] != version.Version {
		t.Fatalf("unexpected serverInfo: %v", serverInfo)
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
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got: %d", len(tools))
	}

	hasGetSessionDigest := false
	for _, toolObj := range tools {
		tMap := toolObj.(map[string]interface{})
		if tMap["name"] == "get_session_digest" {
			hasGetSessionDigest = true
			schema, ok := tMap["inputSchema"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing inputSchema for get_session_digest")
			}
			props, ok := schema["properties"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing properties for get_session_digest")
			}
			if _, ok := props["session_id"]; !ok {
				t.Errorf("expected session_id in properties")
			}
			if _, ok := props["wing_path"]; !ok {
				t.Errorf("expected wing_path in properties")
			}
			if _, ok := props["regenerate"]; !ok {
				t.Errorf("expected regenerate in properties")
			}
		}
	}
	if !hasGetSessionDigest {
		t.Fatalf("tools/list missing get_session_digest tool")
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

	// Empty wing_path defaults to current working directory
	_ = storeRemember(s, in, out, "Cwd Fact", "fact", "")
	searchEmptyReq := `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"search_memory","arguments":{"wing_path":""}}}`
	resp, err = sendRequest(s, in, out, searchEmptyReq)
	if err != nil || resp == nil {
		t.Fatalf("search with empty wing_path failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr = resMap["content"].([]interface{})
	firstItem = contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "Cwd Fact") {
		t.Fatalf("expected empty wing_path to match cwd: %v", firstItem["text"])
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

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			inBuf := &bytes.Buffer{}
			outBuf := &bytes.Buffer{}
			s := NewServer(store, inBuf, outBuf)
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

func TestMCP_Serve_Lifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// 1. Reader is nil
	sNil := NewServer(store, nil, nil)
	if err := sNil.Serve(context.Background()); err == nil {
		t.Fatalf("expected error for nil reader")
	}

	// 2. Process stream until EOF
	in := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n\n")
	out := &bytes.Buffer{}
	s := NewServer(store, in, out)
	if err := s.Serve(context.Background()); err != nil {
		t.Fatalf("expected nil error on EOF, got: %v", err)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("expected ping response, got: %s", out.String())
	}

	// 3. Canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sCanceled := NewServer(store, strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n"), out)
	if err := sCanceled.Serve(ctx); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestMCP_UnknownTool(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"unknown_tool","arguments":{}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["isError"] != true {
		t.Fatalf("expected isError=true for unknown tool, got: %+v", resMap)
	}
}

func TestMCP_InvalidParams(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":100,"method":"tools/call","params":"not-an-object"}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidParams {
		t.Fatalf("expected CodeInvalidParams, got: %+v", resp.Error)
	}
}

func TestMCP_Remember_EmptyContentOrHall(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":101,"method":"tools/call","params":{"name":"remember","arguments":{"content":"","hall":"fact"}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["isError"] != true {
		t.Fatalf("expected isError=true for empty content, got: %+v", resMap)
	}

	resp, err = sendRequest(s, in, out, `{"jsonrpc":"2.0","id":102,"method":"tools/call","params":{"name":"remember","arguments":{"content":"Something","hall":""}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	if resMap["isError"] != true {
		t.Fatalf("expected isError=true for empty hall, got: %+v", resMap)
	}
}

func TestMCP_ForgetMemory_EdgeCases(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	// Invalid ID
	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":103,"method":"tools/call","params":{"name":"forget_memory","arguments":{"id":-5}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["isError"] != true {
		t.Fatalf("expected isError=true for negative ID, got: %+v", resMap)
	}

	// Not found ID
	resp, err = sendRequest(s, in, out, `{"jsonrpc":"2.0","id":104,"method":"tools/call","params":{"name":"forget_memory","arguments":{"id":999999}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "was not found") {
		t.Fatalf("expected not found text, got: %v", firstItem["text"])
	}
}

func TestMCP_SearchMemory_And_Timeline_Empty(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	// Search empty wing
	resp, err := sendRequest(s, in, out, `{"jsonrpc":"2.0","id":105,"method":"tools/call","params":{"name":"search_memory","arguments":{"wing_path":"/test/empty"}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "No memories found") {
		t.Fatalf("expected No memories found, got: %v", firstItem["text"])
	}

	// Timeline empty
	resp, err = sendRequest(s, in, out, `{"jsonrpc":"2.0","id":106,"method":"tools/call","params":{"name":"get_timeline","arguments":{"wing_path":"/test/empty"}}}`)
	if err != nil || resp == nil {
		t.Fatalf("request failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr = resMap["content"].([]interface{})
	firstItem = contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "No events found") {
		t.Fatalf("expected No events found, got: %v", firstItem["text"])
	}
}

func TestMCP_EmptyLine(t *testing.T) {
	s, _, out := setupTestServer(t)
	in := &bytes.Buffer{}

	resp, err := sendRequest(s, in, out, "   ")
	if err != nil {
		t.Fatalf("expected no error on empty line, got: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected no response on empty line, got: %+v", resp)
	}
}

func TestMCP_GetSessionDigest(t *testing.T) {
	s, store, out := setupTestServer(t)
	in := &bytes.Buffer{}

	wingPath := "/test/digest-project"
	wingID, err := store.GetOrCreateWing(wingPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	digest := storage.SessionDigest{
		WingID:       wingID,
		SessionID:    "sess-xyz",
		Request:      "Refactor auth logic",
		Investigated: "Checked OAuth2 tokens and session storage",
		Learned:      "Refresh token rotation prevents stale auth tokens",
		Completed:    "Implemented token rotation",
		NextSteps:    "Write integration tests",
		Notes:        "Security audit passed",
		CreatedAt:    time.Now().Truncate(time.Second),
	}
	if err := store.UpsertSessionDigest(digest); err != nil {
		t.Fatalf("UpsertSessionDigest failed: %v", err)
	}

	// 1. Fetch by session_id
	callReq := `{"jsonrpc":"2.0","id":201,"method":"tools/call","params":{"name":"get_session_digest","arguments":{"session_id":"sess-xyz","wing_path":"/test/digest-project"}}}`
	resp, err := sendRequest(s, in, out, callReq)
	if err != nil || resp == nil {
		t.Fatalf("get_session_digest failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	if resMap["isError"] == true {
		t.Fatalf("expected success, got error: %+v", resMap)
	}
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	text := firstItem["text"].(string)

	if !strings.Contains(text, `sess-xyz`) ||
		!strings.Contains(text, `Refactor auth logic`) ||
		!strings.Contains(text, `Checked OAuth2 tokens`) ||
		!strings.Contains(text, `Refresh token rotation prevents stale auth tokens`) ||
		!strings.Contains(text, `Implemented token rotation`) ||
		!strings.Contains(text, `Write integration tests`) ||
		!strings.Contains(text, `Security audit passed`) {
		t.Fatalf("digest output missing expected fields, got: %s", text)
	}

	// 2. Fetch nonexistent session_id
	callReq2 := `{"jsonrpc":"2.0","id":202,"method":"tools/call","params":{"name":"get_session_digest","arguments":{"session_id":"sess-nonexistent"}}}`
	resp, err = sendRequest(s, in, out, callReq2)
	if err != nil || resp == nil {
		t.Fatalf("get_session_digest nonexistent failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr = resMap["content"].([]interface{})
	firstItem = contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "No session digest found") {
		t.Fatalf("expected not found message, got: %v", firstItem["text"])
	}

	// 3. List digests for wing without specifying session_id
	callReq3 := `{"jsonrpc":"2.0","id":203,"method":"tools/call","params":{"name":"get_session_digest","arguments":{"wing_path":"/test/digest-project"}}}`
	resp, err = sendRequest(s, in, out, callReq3)
	if err != nil || resp == nil {
		t.Fatalf("get_session_digest list failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr = resMap["content"].([]interface{})
	firstItem = contentArr[0].(map[string]interface{})
	listText := firstItem["text"].(string)
	if !strings.Contains(listText, "sess-xyz") || !strings.Contains(listText, "Refactor auth logic") {
		t.Fatalf("expected list of digests to contain session and request, got: %s", listText)
	}

	// 4. List digests for empty wing
	callReq4 := `{"jsonrpc":"2.0","id":204,"method":"tools/call","params":{"name":"get_session_digest","arguments":{"wing_path":"/test/empty-wing"}}}`
	resp, err = sendRequest(s, in, out, callReq4)
	if err != nil || resp == nil {
		t.Fatalf("get_session_digest empty list failed: %v", err)
	}
	resMap = resp.Result.(map[string]interface{})
	contentArr = resMap["content"].([]interface{})
	firstItem = contentArr[0].(map[string]interface{})
	if !strings.Contains(firstItem["text"].(string), "No session digests found") {
		t.Fatalf("expected No session digests found, got: %v", firstItem["text"])
	}
}

func TestMCP_SearchMemory_WithNarrative(t *testing.T) {
	s, store, out := setupTestServer(t)
	in := &bytes.Buffer{}

	wingPath := "/test/narrative-search"
	wingID, err := store.GetOrCreateWing(wingPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	err = store.InsertSummaryDrawer(
		wingID,
		"sess-narrative",
		storage.HallFact,
		"SQLite busy_timeout invariant",
		"Set busy_timeout to 5000ms to prevent lock contention under concurrent access.",
		now,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("InsertSummaryDrawer failed: %v", err)
	}

	searchReq := `{"jsonrpc":"2.0","id":301,"method":"tools/call","params":{"name":"search_memory","arguments":{"query":"busy_timeout","wing_path":"/test/narrative-search"}}}`
	resp, err := sendRequest(s, in, out, searchReq)
	if err != nil || resp == nil {
		t.Fatalf("search_memory call failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	text := firstItem["text"].(string)

	if !strings.Contains(text, "SQLite busy_timeout invariant") {
		t.Fatalf("expected content in search result, got: %s", text)
	}
	if !strings.Contains(text, "Narrative: Set busy_timeout to 5000ms to prevent lock contention under concurrent access.") {
		t.Fatalf("expected narrative in search result, got: %s", text)
	}
}

func TestMCP_GetTimeline_WithDigestAndNarrative(t *testing.T) {
	s, store, out := setupTestServer(t)
	in := &bytes.Buffer{}

	wingPath := "/test/timeline-digest"
	wingID, err := store.GetOrCreateWing(wingPath)
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	sessionID := "sess-tl-digest"

	digest := storage.SessionDigest{
		WingID:       wingID,
		SessionID:    sessionID,
		Request:      "Implement timeline digest metadata",
		Investigated: "Explored MCP timeline format",
		Learned:      "Showing digest at top improves context",
		Completed:    "Added digest header to timeline",
		NextSteps:    "Integrate UI timeline card",
		Notes:        "",
		CreatedAt:    now,
	}
	if err := store.UpsertSessionDigest(digest); err != nil {
		t.Fatalf("UpsertSessionDigest failed: %v", err)
	}

	err = store.InsertSummaryDrawer(
		wingID,
		sessionID,
		storage.HallDiscovery,
		"Timeline summary bullet",
		"Timeline detailed narrative explaining the discovery context.",
		now,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("InsertSummaryDrawer failed: %v", err)
	}

	timeReq := `{"jsonrpc":"2.0","id":401,"method":"tools/call","params":{"name":"get_timeline","arguments":{"session_id":"sess-tl-digest","wing_path":"/test/timeline-digest"}}}`
	resp, err := sendRequest(s, in, out, timeReq)
	if err != nil || resp == nil {
		t.Fatalf("get_timeline call failed: %v", err)
	}
	resMap := resp.Result.(map[string]interface{})
	contentArr := resMap["content"].([]interface{})
	firstItem := contentArr[0].(map[string]interface{})
	text := firstItem["text"].(string)

	if !strings.Contains(text, "Session Digest") ||
		!strings.Contains(text, "Implement timeline digest metadata") ||
		!strings.Contains(text, "Added digest header to timeline") {
		t.Fatalf("timeline missing session digest header, got: %s", text)
	}

	if !strings.Contains(text, "Timeline summary bullet") {
		t.Fatalf("timeline missing summary content, got: %s", text)
	}

	if !strings.Contains(text, "Narrative: Timeline detailed narrative explaining the discovery context.") {
		t.Fatalf("timeline missing narrative text, got: %s", text)
	}
}


