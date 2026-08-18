package claudecode

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTailer_DirtyCheckingAndTruncation(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.jsonl")

	if err := os.WriteFile(filePath, []byte("line 1\nline 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer()

	// 1. Initial read
	lines, changed, err := tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("first read error: %v", err)
	}
	if !changed || len(lines) != 2 {
		t.Fatalf("expected 2 lines with changed=true, got %d lines, changed=%v", len(lines), changed)
	}

	// 2. Second read without modifications -> should be dirty-checked (changed=false, no lines, no file open)
	lines, changed, err = tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("second read error: %v", err)
	}
	if changed || len(lines) != 0 {
		t.Fatalf("expected 0 lines with changed=false on unchanged file, got %d lines, changed=%v", len(lines), changed)
	}

	// 3. Append a new line
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line 3\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Force mtime change if filesystem has coarse resolution
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(filePath, now, now)

	lines, changed, err = tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("third read error: %v", err)
	}
	if !changed || len(lines) != 1 || string(lines[0]) != "line 3" {
		t.Fatalf("expected line 3 with changed=true, got %v, changed=%v", lines, changed)
	}

	// 4. Truncation / File Rewrite
	if err := os.WriteFile(filePath, []byte("new line 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filePath, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))

	lines, changed, err = tailer.ReadNewLines(filePath)
	if err != nil {
		t.Fatalf("read after truncation error: %v", err)
	}
	if !changed || len(lines) != 1 || string(lines[0]) != "new line 1" {
		t.Fatalf("expected reset and read 'new line 1', got %v", lines)
	}
}

func linesToStrings(lines [][]byte) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}

func TestReadNewLinesOnlyReturnsAppendedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tailer := NewTailer()
	lines, _, err := tailer.ReadNewLines(path)
	if err != nil {
		t.Fatalf("ReadNewLines: %v", err)
	}
	if len(lines) != 2 || string(lines[0]) != "line1" || string(lines[1]) != "line2" {
		t.Fatalf("unexpected first read: %v", linesToStrings(lines))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("line3\npartial-lin"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	lines2, _, err := tailer.ReadNewLines(path)
	if err != nil {
		t.Fatalf("ReadNewLines (2nd): %v", err)
	}
	if len(lines2) != 1 || string(lines2[0]) != "line3" {
		t.Fatalf("expected only the complete new line, got %v", linesToStrings(lines2))
	}

	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("e4\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	lines3, _, err := tailer.ReadNewLines(path)
	if err != nil {
		t.Fatalf("ReadNewLines (3rd): %v", err)
	}
	if len(lines3) != 1 || string(lines3[0]) != "partial-line4" {
		t.Fatalf("expected the completed partial line, got %v", linesToStrings(lines3))
	}
}

func TestDiscoverTranscriptFilesFindsOnlyJSONL(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mustWrite("proj-a/session1.jsonl", "{}")
	mustWrite("proj-a/session2.jsonl", "{}")
	mustWrite("proj-b/session3.jsonl", "{}")
	mustWrite("proj-b/notes.md", "ignore me")

	files, err := DiscoverTranscriptFiles(root)
	if err != nil {
		t.Fatalf("DiscoverTranscriptFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 .jsonl files, got %d: %v", len(files), files)
	}
}

func TestDiscoverTranscriptFilesOnMissingRootReturnsEmptyNoError(t *testing.T) {
	files, err := DiscoverTranscriptFiles(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error for a missing root, got %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
}

// sequentialReader returns specific data and errors in sequence, useful for
// testing error handling without relying on actual file system I/O.
type sequentialReader struct {
	calls     []struct {
		data []byte
		err  error
	}
	callIndex int
}

func (s *sequentialReader) Read(p []byte) (int, error) {
	if s.callIndex >= len(s.calls) {
		return 0, io.EOF
	}
	call := s.calls[s.callIndex]
	s.callIndex++
	n := copy(p, call.data)
	return n, call.err
}

func TestReadNewLinesReturnsNonEOFErrors(t *testing.T) {
	tailer := NewTailer()

	// Create a reader that returns:
	// - First read: "line1\n" with no error
	// - Second read: "line2" with a non-EOF error
	// This simulates an I/O error occurring mid-stream.
	reader := &sequentialReader{
		calls: []struct {
			data []byte
			err  error
		}{
			{[]byte("line1\n"), nil},
			{[]byte("line2"), fmt.Errorf("simulated I/O error")},
		},
	}

	lines, err := tailer.readNewLinesFrom("test.jsonl", reader, 0)

	// Should have gotten the first complete line
	if len(lines) != 1 || string(lines[0]) != "line1" {
		t.Fatalf("expected [line1], got %v", linesToStrings(lines))
	}

	// Should have gotten a non-EOF error
	if err == nil {
		t.Fatalf("expected a non-EOF error, got nil")
	}
	if err == io.EOF {
		t.Fatalf("expected non-EOF error, got io.EOF")
	}
}

func TestReadNewLinesPersistsOffsetOnError(t *testing.T) {
	tailer := NewTailer()

	// First call: reads line1 successfully, then hits an error
	reader1 := &sequentialReader{
		calls: []struct {
			data []byte
			err  error
		}{
			{[]byte("line1\n"), nil},
			{[]byte(""), fmt.Errorf("simulated I/O error")},
		},
	}

	lines1, err1 := tailer.readNewLinesFrom("test.jsonl", reader1, 0)
	if len(lines1) != 1 || string(lines1[0]) != "line1" {
		t.Fatalf("first call: expected [line1], got %v", linesToStrings(lines1))
	}
	if err1 == nil {
		t.Fatalf("first call: expected error, got nil")
	}

	// Check that the offset was persisted even though an error occurred
	// "line1\n" is 6 bytes, so the offset should be 6
	expectedOffset := int64(6)
	actualOffset := tailer.Offset("test.jsonl")
	if actualOffset != expectedOffset {
		t.Fatalf("offset not persisted after error: got %d, expected %d", actualOffset, expectedOffset)
	}

	// Second call: should start from offset 6, not re-read line1
	// This simulates the daemon calling ReadNewLines again after the error
	reader2 := &sequentialReader{
		calls: []struct {
			data []byte
			err  error
		}{
			{[]byte("line2\n"), nil},
		},
	}

	lines2, err2 := tailer.readNewLinesFrom("test.jsonl", reader2, actualOffset)
	if len(lines2) != 1 || string(lines2[0]) != "line2" {
		t.Fatalf("second call: expected [line2], got %v", linesToStrings(lines2))
	}
	if err2 != nil {
		t.Fatalf("second call: expected no error, got %v", err2)
	}

	// Verify offset was updated correctly after successful read
	expectedOffset2 := int64(12) // 6 (from before) + 6 ("line2\n")
	actualOffset2 := tailer.Offset("test.jsonl")
	if actualOffset2 != expectedOffset2 {
		t.Fatalf("offset not updated correctly: got %d, expected %d", actualOffset2, expectedOffset2)
	}
}
