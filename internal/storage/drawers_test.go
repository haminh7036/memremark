package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInsertVerbatimAndSummaryDrawers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, err := s.GetOrCreateWing("/tmp/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now()
	if err := s.InsertVerbatimDrawer(wingID, "session-1", "Read", "read foo.txt", now); err != nil {
		t.Fatalf("InsertVerbatimDrawer: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "session-1", HallFact, "decided to use SQLite", now.Add(-time.Hour), now, now); err != nil {
		t.Fatalf("InsertSummaryDrawer: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "session-1", "not-a-real-hall", "x", now, now, now); err == nil {
		t.Fatalf("expected error for invalid hall, got nil")
	}

	summaries, err := s.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary drawer, got %d", len(summaries))
	}
	if summaries[0].Hall != HallFact || summaries[0].Content != "decided to use SQLite" {
		t.Fatalf("unexpected summary: %+v", summaries[0])
	}
}

func TestRecentSummariesOrdersNewestFirst(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now()

	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "older fact", older, older, older); err != nil {
		t.Fatalf("insert older: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "newer fact", newer, newer, newer); err != nil {
		t.Fatalf("insert newer: %v", err)
	}

	summaries, err := s.RecentSummaries(wingID, 10)
	if err != nil {
		t.Fatalf("RecentSummaries: %v", err)
	}
	if len(summaries) != 2 || summaries[0].Content != "newer fact" {
		t.Fatalf("expected newest first, got %+v", summaries)
	}
}

// TestRecentSummariesOrdersByIDWhenCreatedAtTies covers the case the daemon
// hits routinely: a batch of summary drawers inserted with the identical
// created_at value (one summarization pass, one "now"). Without an id
// tiebreaker, SQLite's tie order is unspecified and a batch bigger than the
// requested limit could be truncated non-deterministically.
func TestRecentSummariesOrdersByIDWhenCreatedAtTies(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	sameInstant := time.Now().Truncate(time.Second)

	contents := []string{"first", "second", "third", "fourth"}
	for _, c := range contents {
		if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, c, sameInstant, sameInstant, sameInstant); err != nil {
			t.Fatalf("insert %q: %v", c, err)
		}
	}

	wantOrder := []string{"fourth", "third", "second", "first"}
	for attempt := 0; attempt < 3; attempt++ {
		summaries, err := s.RecentSummaries(wingID, 10)
		if err != nil {
			t.Fatalf("RecentSummaries: %v", err)
		}
		if len(summaries) != len(wantOrder) {
			t.Fatalf("expected %d summaries, got %d", len(wantOrder), len(summaries))
		}
		for i, want := range wantOrder {
			if summaries[i].Content != want {
				t.Fatalf("attempt %d: expected order %v, got %+v", attempt, wantOrder, summaries)
			}
		}
	}
}

func TestVerbatimSinceReturnsOnlyNewerRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	if err := s.InsertVerbatimDrawer(wingID, "s1", "Bash", "old command", older); err != nil {
		t.Fatalf("insert older: %v", err)
	}
	if err := s.InsertVerbatimDrawer(wingID, "s1", "Read", "new command", newer); err != nil {
		t.Fatalf("insert newer: %v", err)
	}

	got, err := s.VerbatimSince(wingID, "s1", older)
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(got) != 1 || got[0].Content != "new command" || got[0].ToolName != "Read" {
		t.Fatalf("expected only the newer row, got %+v", got)
	}
}

func TestLastSummaryTimeReturnsFalseWhenNoneExists(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	_, ok, err := s.LastSummaryTime(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryTime: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when no summary exists")
	}
}

func TestLastSummaryTimeReturnsMostRecent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	first := time.Now().Add(-time.Hour).Truncate(time.Second)
	second := time.Now().Truncate(time.Second)
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "a", first, first, first); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "b", second, second, second); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	got, ok, err := s.LastSummaryTime(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryTime: %v", err)
	}
	if !ok || !got.Equal(second) {
		t.Fatalf("expected %v (ok=true), got %v (ok=%v)", second, got, ok)
	}
}
