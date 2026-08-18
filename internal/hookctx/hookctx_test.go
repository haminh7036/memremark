package hookctx

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestGetSummaries(t *testing.T) {
	fakeHome := t.TempDir()
	dbPath := filepath.Join(fakeHome, ".memremark", "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/workspace/project")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	coversFrom := now.Add(-time.Hour)
	coversTo := now.Add(-time.Minute)

	if err := store.InsertSummaryDrawer(wingID, "session-1", "fact", "Go 1.26 is the minimum version", coversFrom, coversTo, now); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSummaryDrawer(wingID, "session-1", "discovery", "SQLite WAL mode is needed for concurrent reads", coversFrom, coversTo, now); err != nil {
		t.Fatal(err)
	}

	summaries, err := GetSummaries("/workspace/project", fakeHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
}

func TestGetSummaries_NoSummaries(t *testing.T) {
	fakeHome := t.TempDir()
	dbPath := filepath.Join(fakeHome, ".memremark", "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	summaries, err := GetSummaries("/workspace/project", fakeHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestGetSummariesReturnsErrorOnStorageOpenFailure(t *testing.T) {
	summaries, err := GetSummaries(".", "/nonexistent/1234567890/memremark.db")
	if err == nil {
		t.Fatalf("expected error when storage path is broken, got nil")
	}
	if summaries != nil {
		t.Fatalf("expected nil summaries on error, got %v", summaries)
	}
}

func TestFormatSummariesIncludesHallAndContent(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: storage.HallFact, Content: "chose SQLite for v1"},
		{Hall: storage.HallDiscovery, Content: "agy hooks don't execute"},
	}
	out := FormatSummaries(summaries)
	if !strings.Contains(out, "chose SQLite for v1") {
		t.Fatalf("expected output to include first summary, got %q", out)
	}
	if !strings.Contains(out, "agy hooks don't execute") {
		t.Fatalf("expected output to include second summary, got %q", out)
	}
}

func TestFormatSummariesExactOutput(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: "fact", Content: "Go 1.26 minimum"},
		{Hall: "advice", Content: "Always run tests before commit"},
	}
	got := FormatSummaries(summaries)
	expected := "Bối cảnh từ các phiên làm việc trước (memremark):\n- [fact] Go 1.26 minimum\n- [advice] Always run tests before commit\n"
	if got != expected {
		t.Fatalf("FormatSummaries mismatch:\ngot:  %q\nwant: %q", got, expected)
	}
}
