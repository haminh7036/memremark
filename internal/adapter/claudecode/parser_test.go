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
		obs, err := p.Feed(scanner.Bytes())
		if err != nil {
			t.Fatalf("Feed returned error: %v", err)
		}
		for _, o := range obs {
			count++
			toolName, sessionID, wingPath, content = o.ToolName, o.SessionID, o.WingPath, o.Content
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
	obs, err := p.Feed([]byte(`{"type":"user","uuid":"a5","message":{`))
	if err != nil {
		t.Fatalf("expected no error for a malformed line, got %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("expected no observations for a malformed line, got %d", len(obs))
	}
}

func TestParserOnEmptyLineReturnsNothing(t *testing.T) {
	p := NewParser()
	obs, err := p.Feed([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("expected no observations for an empty line, got %d", len(obs))
	}
}

func TestParserIgnoresPlainTextTurns(t *testing.T) {
	p := NewParser()
	line := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","message":{"content":"hello"}}`
	obs, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("expected no observations for a plain text turn, got %d", len(obs))
	}
}

func TestParserLeavesUnmatchedToolUsePending(t *testing.T) {
	p := NewParser()
	line := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp","message":{"content":[{"type":"tool_use","id":"toolu_x","name":"Read","input":{}}]}}`
	obs, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("expected no observations, a lone tool_use should not emit one, got %d", len(obs))
	}
	if len(p.pending) != 1 {
		t.Fatalf("expected 1 pending tool_use, got %d", len(p.pending))
	}
}

// TestParserReturnsAllObservationsFromMultiResultLine proves Bug 1 is
// fixed: when a single line completes multiple tool_results, every one of
// them comes back from that same Feed call. Before the fix, only the
// first was returned and the rest were buffered to drain on a future
// call -- so if this were the transcript's last line, the extras would
// never be written to storage.
func TestParserReturnsAllObservationsFromMultiResultLine(t *testing.T) {
	p := NewParser()

	// Line 1: assistant with two parallel tool_uses.
	line1 := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp","timestamp":"2026-08-10T10:00:00.000Z","message":{"content":[{"type":"tool_use","id":"id1","name":"Read","input":{"file":"a.txt"}},{"type":"tool_use","id":"id2","name":"Write","input":{"file":"b.txt"}}]}}`
	obs, err := p.Feed([]byte(line1))
	if err != nil {
		t.Fatalf("Feed line 1: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("expected no observations for the assistant line, got %d", len(obs))
	}

	// Line 2 is the session's LAST line: both matching tool_results arrive
	// together, with no future line to trigger a drain.
	line2 := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","timestamp":"2026-08-10T10:00:01.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"id1","content":"file a","is_error":false},{"type":"tool_result","tool_use_id":"id2","content":"file b","is_error":false}]}}`
	obs, err = p.Feed([]byte(line2))
	if err != nil {
		t.Fatalf("Feed line 2: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("expected both observations from this single call, got %d", len(obs))
	}
	names := map[string]bool{obs[0].ToolName: true, obs[1].ToolName: true}
	if !names["Read"] || !names["Write"] {
		t.Fatalf("expected observations for both Read and Write, got %+v", obs)
	}
	if len(p.pending) != 0 {
		t.Fatalf("expected both tool_uses resolved, got %d still pending", len(p.pending))
	}
}

func TestParserIgnoresOrphanToolResult(t *testing.T) {
	p := NewParser()
	// A tool_result with no matching pending tool_use should be silently ignored
	line := `{"type":"user","sessionId":"sess-1","cwd":"/tmp","message":{"content":[{"type":"tool_result","tool_use_id":"orphan_id","content":"result","is_error":false}]}}`
	obs, err := p.Feed([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs) != 0 {
		t.Fatalf("expected no observations for orphan tool_result, got %d", len(obs))
	}
	if len(p.pending) != 0 {
		t.Fatalf("expected 0 pending entries, got %d", len(p.pending))
	}
}
