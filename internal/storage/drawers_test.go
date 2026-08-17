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

func TestLastSummaryCoversToReturnsFalseWhenNoneExists(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")
	_, ok, err := s.LastSummaryCoversTo(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryCoversTo: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when no summary exists")
	}
}

func TestLastSummaryCoversToReturnsMostRecentCoversTo(t *testing.T) {
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

	got, ok, err := s.LastSummaryCoversTo(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryCoversTo: %v", err)
	}
	if !ok || !got.Equal(second) {
		t.Fatalf("expected %v (ok=true), got %v (ok=%v)", second, got, ok)
	}
}

// TestLastSummaryCoversToUsesEventTimeNotInsertionWallClock reproduces the
// clock-mismatch bug from Important 3: a summary drawer's own created_at is
// the daemon's poll-time wall-clock when it was inserted, which can be much
// later than covers_to (the real verbatim event-time it distilled up to) --
// e.g. during backlog catch-up after downtime. A verbatim row whose event-
// time falls between covers_to and that insertion wall-clock must still be
// picked up by the next VerbatimSince call. Before the fix (reading
// created_at instead of covers_to), this row would be silently and
// permanently skipped.
func TestLastSummaryCoversToUsesEventTimeNotInsertionWallClock(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	wingID, _ := s.GetOrCreateWing("/tmp/project")

	coversTo := time.Now().Add(-time.Hour).Truncate(time.Second)  // last real event-time distilled
	insertedAt := time.Now().Truncate(time.Second)                // daemon's wall clock when it wrote the summary, much later
	if err := s.InsertSummaryDrawer(wingID, "s1", HallFact, "a", coversTo, coversTo, insertedAt); err != nil {
		t.Fatalf("insert summary: %v", err)
	}

	// Newer than what's already been summarized (covers_to), but older than
	// the summary row's own insertion wall-clock (created_at).
	lateArrivingEventTime := coversTo.Add(30 * time.Minute)
	if err := s.InsertVerbatimDrawer(wingID, "s1", "Bash", "late-discovered command", lateArrivingEventTime); err != nil {
		t.Fatalf("insert verbatim: %v", err)
	}

	since, ok, err := s.LastSummaryCoversTo(wingID, "s1")
	if err != nil {
		t.Fatalf("LastSummaryCoversTo: %v", err)
	}
	if !ok || !since.Equal(coversTo) {
		t.Fatalf("expected since=%v (ok=true), got %v (ok=%v)", coversTo, since, ok)
	}

	got, err := s.VerbatimSince(wingID, "s1", since)
	if err != nil {
		t.Fatalf("VerbatimSince: %v", err)
	}
	if len(got) != 1 || got[0].Content != "late-discovered command" {
		t.Fatalf("expected the late-arriving verbatim row to still be picked up, got %+v", got)
	}
}
