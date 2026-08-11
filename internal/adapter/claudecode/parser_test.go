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
