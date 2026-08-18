package hookctx

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
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

func TestFormatSummariesExactOutput_Localized(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: "fact", Content: "Go 1.26 minimum"},
		{Hall: "advice", Content: "Always run tests before commit"},
	}

	// 1. Vietnamese (vi)
	gotVi := FormatSummaries(summaries, locale.TargetLanguage{Code: "vi", Name: "Vietnamese"})
	expectedVi := "Bối cảnh từ các phiên làm việc trước (memremark):\n- [fact] Go 1.26 minimum\n- [advice] Always run tests before commit\n"
	if gotVi != expectedVi {
		t.Fatalf("Vietnamese FormatSummaries mismatch:\ngot:  %q\nwant: %q", gotVi, expectedVi)
	}

	// 2. Japanese (ja)
	gotJa := FormatSummaries(summaries, locale.TargetLanguage{Code: "ja", Name: "Japanese"})
	expectedJa := "過去のセッションからのコンテキスト (memremark):\n- [fact] Go 1.26 minimum\n- [advice] Always run tests before commit\n"
	if gotJa != expectedJa {
		t.Fatalf("Japanese FormatSummaries mismatch:\ngot:  %q\nwant: %q", gotJa, expectedJa)
	}

	// 3. Chinese (zh)
	gotZh := FormatSummaries(summaries, locale.TargetLanguage{Code: "zh", Name: "Chinese"})
	expectedZh := "来自先前会话的上下文 (memremark):\n- [fact] Go 1.26 minimum\n- [advice] Always run tests before commit\n"
	if gotZh != expectedZh {
		t.Fatalf("Chinese FormatSummaries mismatch:\ngot:  %q\nwant: %q", gotZh, expectedZh)
	}

	// 4. Default / English (en)
	gotEn := FormatSummaries(summaries, locale.TargetLanguage{Code: "en", Name: "English"})
	expectedEn := "Context from prior sessions (memremark):\n- [fact] Go 1.26 minimum\n- [advice] Always run tests before commit\n"
	if gotEn != expectedEn {
		t.Fatalf("English FormatSummaries mismatch:\ngot:  %q\nwant: %q", gotEn, expectedEn)
	}
}
