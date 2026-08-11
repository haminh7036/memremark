package claudecode

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestParserPairsToolUseWithToolResult(t *testing.T) {
	f, err := os.Open("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	p := NewParser()
	var count int
	var toolName, sessionID, wingPath, content string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		obs, ok, err := p.Feed(scanner.Bytes())
		if err != nil {
			t.Fatalf("Feed returned error: %v", err)
		}
		if ok {
			count++
			toolName, sessionID, wingPath, content = obs.ToolName, obs.SessionID, obs.WingPath, obs.Content
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly 1 completed observation (the 2nd tool_use has no matching result), got %d", count)
	}
	if toolName != "Bash" {
		t.Fatalf("expected tool name Bash, got %q", toolName)
	}
	if sessionID != "sess-1" {
		t.Fatalf("expected session sess-1, got %q", sessionID)
	}
	if wingPath != "/home/minh/personal/memremark" {
		t.Fatalf("unexpected wing path %q", wingPath)
	}
	if !strings.Contains(content, "README.md") {
		t.Fatalf("expected result content to include README.md, got %q", content)
	}
}

func TestParserSkipsMalformedLinesWithoutError(t *testing.T) {
	p := NewParser()
	_, ok, err := p.Feed([]byte(`{"type":"user","uuid":"a5","message":{`))
	if err != nil {
		t.Fatalf("expected no error for a malformed line, got %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for a malformed line")
	}
}

func TestParserOnEmptyLineReturnsNothing(t *testing.T) {
	p := NewParser()
	_, ok, err := p.Feed([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for an empty line")
	}
}

func TestParserIgnoresPlainTextTurns(t *testing.T) {
	p := NewParser()
	line := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","message":{"content":"hello"}}`
	_, ok, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for a plain text turn")
	}
}

func TestParserLeavesUnmatchedToolUsePending(t *testing.T) {
	p := NewParser()
	line := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp","message":{"content":[{"type":"tool_use","id":"toolu_x","name":"Read","input":{}}]}}`
	_, ok, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false, a lone tool_use should not emit an observation")
	}
	if len(p.pending) != 1 {
		t.Fatalf("expected 1 pending tool_use, got %d", len(p.pending))
	}
}

func TestParserHandlesMultipleToolResultsInOneLine(t *testing.T) {
	p := NewParser()

	// Line 1: assistant with two tool_uses
	line1 := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp","timestamp":"2026-08-10T10:00:00.000Z","message":{"content":[{"type":"tool_use","id":"id1","name":"Read","input":{"file":"a.txt"}},{"type":"tool_use","id":"id2","name":"Write","input":{"file":"b.txt"}}]}}`
	obs, ok, err := p.Feed([]byte(line1))
	if err != nil {
		t.Fatalf("Feed line 1: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for assistant line, got true")
	}

	// Line 2: user with two matching tool_results
	line2 := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","timestamp":"2026-08-10T10:00:01.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"id1","content":"file a","is_error":false},{"type":"tool_result","tool_use_id":"id2","content":"file b","is_error":false}]}}`
	obs, ok, err = p.Feed([]byte(line2))
	if err != nil {
		t.Fatalf("Feed line 2 first call: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true for first tool_result")
	}
	if obs.ToolName != "Read" {
		t.Fatalf("expected first observation ToolName=Read, got %q", obs.ToolName)
	}

	// Line 3: next Feed call should drain the buffered second observation
	line3 := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","timestamp":"2026-08-10T10:00:02.000Z","message":{"content":"plain text"}}`
	obs2, ok, err := p.Feed([]byte(line3))
	if err != nil {
		t.Fatalf("Feed line 3: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true for buffered second tool_result")
	}
	if obs2.ToolName != "Write" {
		t.Fatalf("expected second observation ToolName=Write, got %q", obs2.ToolName)
	}

	// Now feeding line4 should return ok=false (no more buffered, and plain text doesn't produce observation)
	_, ok, err = p.Feed([]byte(`{"type":"user","sessionId":"sess-1","cwd":"/tmp","message":{"content":"another plain"}}`))
	if err != nil {
		t.Fatalf("Feed line 4: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false, no more buffered observations or matches")
	}
}

func TestParserIgnoresOrphanToolResult(t *testing.T) {
	p := NewParser()
	// A tool_result with no matching pending tool_use should be silently ignored
	line := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","message":{"content":[{"type":"tool_result","tool_use_id":"orphan_id","content":"result","is_error":false}]}}`
	_, ok, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for orphan tool_result")
	}
	if len(p.pending) != 0 {
		t.Fatalf("expected 0 pending entries, got %d", len(p.pending))
	}
}
