package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

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
	lines, err := tailer.ReadNewLines(path)
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

	lines2, err := tailer.ReadNewLines(path)
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

	lines3, err := tailer.ReadNewLines(path)
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
