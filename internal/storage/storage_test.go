package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesSchemaAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "memremark.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
}

func TestOpenIsIdempotentOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memremark.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open on existing file: %v", err)
	}
	defer s2.Close()
}

func TestGetOrCreateWingIsIdempotentPerPath(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id1, err := s.GetOrCreateWing("/home/minh/personal/memremark")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	if id1 == 0 {
		t.Fatalf("expected non-zero wing id")
	}

	id2, err := s.GetOrCreateWing("/home/minh/personal/memremark")
	if err != nil {
		t.Fatalf("GetOrCreateWing (2nd call): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected same wing id, got %d and %d", id1, id2)
	}

	id3, err := s.GetOrCreateWing("/home/minh/personal/other-project")
	if err != nil {
		t.Fatalf("GetOrCreateWing (other path): %v", err)
	}
	if id3 == id1 {
		t.Fatalf("expected a different wing id for a different path")
	}
}

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

// TestGetOrCreateWingConcurrentCallsReturnSameID forces the check-then-insert
// race directly: two separate connections to the same database file (as the
// long-lived daemon connection and a per-invocation hook connection would
// be) both call GetOrCreateWing for a brand-new path at the same time. Before
// the fix, the loser of the race hit the UNIQUE(path) constraint and
// returned an error instead of the winner's id.
func TestGetOrCreateWingConcurrentCallsReturnSameID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memremark.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open s1: %v", err)
	}
	defer s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open s2: %v", err)
	}
	defer s2.Close()

	const wingPath = "/tmp/race-project"
	const attempts = 20

	var wg sync.WaitGroup
	ids := make([]int64, 2*attempts)
	errs := make([]error, 2*attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			ids[idx], errs[idx] = s1.GetOrCreateWing(wingPath)
		}(2 * i)
		go func(idx int) {
			defer wg.Done()
			ids[idx], errs[idx] = s2.GetOrCreateWing(wingPath)
		}(2*i + 1)
	}
	wg.Wait()

	first := ids[0]
	for i, err := range errs {
		if err != nil {
			t.Fatalf("GetOrCreateWing call %d: %v", i, err)
		}
		if ids[i] != first {
			t.Fatalf("expected every concurrent call to return the same wing id %d, call %d got %d", first, i, ids[i])
		}
	}
}

// TestOpenSetsBusyTimeout confirms Open configures a non-zero busy_timeout
// so a concurrent writer waits briefly instead of failing immediately with
// "database is locked".
func TestOpenSetsBusyTimeout(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var got int
	if err := s.db.QueryRow("PRAGMA busy_timeout;").Scan(&got); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if got != 5000 {
		t.Fatalf("expected busy_timeout 5000, got %d", got)
	}
}

// TestOpenSetsRestrictivePermissions asserts the private-activity-log
// database ends up with a private directory (0700) and a private file
// (0600), not the driver/OS defaults.
func TestOpenSetsRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits don't apply on windows")
	}

	dir := filepath.Join(t.TempDir(), "sub")
	path := filepath.Join(dir, "memremark.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("expected dir mode 0700, got %o", perm)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", perm)
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
