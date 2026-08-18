package storage

import (
	"path/filepath"
	"sync"
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

func TestStore_SearchDrawers_Filters(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws1")
	now := time.Now().Truncate(time.Second)
	coversFrom := now.Add(-time.Hour).Truncate(time.Second)
	coversTo := now.Truncate(time.Second)

	_ = store.InsertSummaryDrawer(wingID, "s1", HallFact, "Fact about golang compiler", coversFrom, coversTo, now)
	_ = store.InsertSummaryDrawer(wingID, "s1", HallAdvice, "Advice on memory management", coversFrom, coversTo, now.Add(time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "s2", "Bash", "go build -o test", now.Add(2*time.Second))

	// Search by query and verify fields
	res, err := store.SearchDrawers(wingID, "golang", "", "", 10)
	if err != nil || len(res) != 1 || res[0].Hall != HallFact {
		t.Fatalf("search query failed: got %v, err: %v", res, err)
	}
	if res[0].WingID != wingID {
		t.Fatalf("expected WingID %d, got %d", wingID, res[0].WingID)
	}
	if res[0].SessionID != "s1" {
		t.Fatalf("expected SessionID 's1', got %q", res[0].SessionID)
	}
	if res[0].CoversFrom != coversFrom.Unix() {
		t.Fatalf("expected CoversFrom %d, got %d", coversFrom.Unix(), res[0].CoversFrom)
	}
	if res[0].CoversTo != coversTo.Unix() {
		t.Fatalf("expected CoversTo %d, got %d", coversTo.Unix(), res[0].CoversTo)
	}
	if res[0].Type != "summary" {
		t.Fatalf("expected Type 'summary', got %q", res[0].Type)
	}

	// Search by hall
	res, err = store.SearchDrawers(wingID, "", HallAdvice, "", 10)
	if err != nil || len(res) != 1 || res[0].Hall != HallAdvice {
		t.Fatalf("search hall failed: got %v, err: %v", res, err)
	}

	// Search by type=verbatim and verify fields
	res, err = store.SearchDrawers(wingID, "", "", "verbatim", 10)
	if err != nil || len(res) != 1 || res[0].ToolName != "Bash" {
		t.Fatalf("search verbatim failed: got %v, err: %v", res, err)
	}
	if res[0].WingID != wingID {
		t.Fatalf("expected WingID %d, got %d", wingID, res[0].WingID)
	}
	if res[0].SessionID != "s2" {
		t.Fatalf("expected SessionID 's2', got %q", res[0].SessionID)
	}
	if res[0].CoversFrom != 0 || res[0].CoversTo != 0 {
		t.Fatalf("expected CoversFrom/To=0 for verbatim, got %d/%d", res[0].CoversFrom, res[0].CoversTo)
	}
	if res[0].Type != "verbatim" {
		t.Fatalf("expected Type 'verbatim', got %q", res[0].Type)
	}

	// Limit clamping (limit=0 defaults to 10, limit > 200 clamps to 200)
	res, err = store.SearchDrawers(wingID, "", "", "all", 0)
	if err != nil || len(res) != 3 {
		t.Fatalf("search limit clamping failed: got %d items", len(res))
	}
	res, err = store.SearchDrawers(wingID, "", "", "all", 250)
	if err != nil || len(res) != 3 {
		t.Fatalf("search limit clamping >200 failed: got %d items", len(res))
	}
}

func TestStore_SearchDrawers_SpecialChars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_special")
	now := time.Now().Truncate(time.Second)

	_ = store.InsertSummaryDrawer(wingID, "s1", HallDiscovery, "Tiết kiệm 50% CPU với 'O'Reilly' optimization_v2", now, now, now)

	// Search with %, _, and quotes
	for _, q := range []string{"50%", "O'Reilly", "optimization_v2", "Tiết kiệm"} {
		res, err := store.SearchDrawers(wingID, q, "", "all", 10)
		if err != nil {
			t.Fatalf("SearchDrawers failed for %q: %v", q, err)
		}
		if len(res) != 1 {
			t.Fatalf("expected 1 match for %q, got %d", q, len(res))
		}
	}
}

func TestStore_InsertManualSummary_Validation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_manual")
	now := time.Now()

	// Invalid hall
	_, err = store.InsertManualSummary(wingID, "invalid_hall", "test", now)
	if err == nil {
		t.Fatalf("expected error for invalid hall, got nil")
	}

	// Valid hall
	id, err := store.InsertManualSummary(wingID, HallPreference, "Use atomic commits", now)
	if err != nil || id <= 0 {
		t.Fatalf("InsertManualSummary failed: id=%d, err=%v", id, err)
	}

	summaries, err := store.RecentSummaries(wingID, 5)
	if err != nil || len(summaries) != 1 || summaries[0].ID != id || summaries[0].Content != "Use atomic commits" {
		t.Fatalf("expected inserted summary in RecentSummaries: %v, err: %v", summaries, err)
	}
}

func TestStore_GetTimeline_Ordering(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_timeline")
	baseTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second)

	_ = store.InsertVerbatimDrawer(wingID, "session-1", "Bash", "event 1", baseTime.Add(10*time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "session-1", "ViewFile", "event 2", baseTime.Add(20*time.Second))
	_ = store.InsertSummaryDrawer(wingID, "session-1", HallFact, "summary 1", baseTime.Add(10*time.Second), baseTime.Add(20*time.Second), baseTime.Add(30*time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "session-2", "Bash", "other session event", baseTime.Add(15*time.Second))

	// Get timeline for session-1
	timeline, err := store.GetTimeline(wingID, "session-1", baseTime, 50)
	if err != nil {
		t.Fatalf("GetTimeline failed: %v", err)
	}
	if len(timeline) != 3 {
		t.Fatalf("expected 3 events for session-1, got %d", len(timeline))
	}
	if timeline[0].Content != "event 1" || timeline[1].Content != "event 2" || timeline[2].Content != "summary 1" {
		t.Fatalf("unexpected chronological order: %+v", timeline)
	}
}

func TestStore_DeleteDrawer_SuccessAndNotFound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	wingID, _ := store.GetOrCreateWing("/test/ws_delete")
	id, _ := store.InsertManualSummary(wingID, HallAdvice, "Temporary advice", time.Now())

	deleted, err := store.DeleteDrawer(id)
	if err != nil || !deleted {
		t.Fatalf("expected drawer to be deleted: %v, err: %v", deleted, err)
	}

	// Delete again (non-existent)
	deletedAgain, err := store.DeleteDrawer(id)
	if err != nil || deletedAgain {
		t.Fatalf("expected deletedAgain to be false, got: %v, err: %v", deletedAgain, err)
	}
}

func TestStore_GetOrCreateWing_Concurrency(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	ids := make([]int64, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := store.GetOrCreateWing("/concurrent/project/path")
			if err != nil {
				errCh <- err
				return
			}
			ids[idx] = id
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrency error: %v", err)
	}

	for i := 1; i < 20; i++ {
		if ids[i] != ids[0] || ids[i] <= 0 {
			t.Fatalf("expected same wing ID for all concurrent calls, got: %v", ids)
		}
	}
}

func TestStore_ListWingsWithStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// 1. Empty database test
	emptyWings, err := store.ListWingsWithStats()
	if err != nil {
		t.Fatalf("ListWingsWithStats on empty db failed: %v", err)
	}
	if len(emptyWings) != 0 {
		t.Fatalf("expected 0 wings on empty db, got %d", len(emptyWings))
	}

	// 2. Populated wings test with sorting and mixed counts
	wB, err := store.GetOrCreateWing("/path/to/b-project")
	if err != nil {
		t.Fatalf("create wing B: %v", err)
	}
	wA, err := store.GetOrCreateWing("/path/to/a-project")
	if err != nil {
		t.Fatalf("create wing A: %v", err)
	}
	wC, err := store.GetOrCreateWing("/path/to/c-empty-project")
	if err != nil {
		t.Fatalf("create wing C: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	// Wing A: 2 summaries, 1 verbatim
	_ = store.InsertSummaryDrawer(wA, "s1", HallFact, "Fact A1", now, now, now)
	_ = store.InsertSummaryDrawer(wA, "s1", HallDiscovery, "Discovery A1", now, now, now)
	_ = store.InsertVerbatimDrawer(wA, "s1", "Read", "cat foo.go", now)

	// Wing B: 1 summary, 2 verbatims
	_ = store.InsertSummaryDrawer(wB, "s2", HallAdvice, "Advice B1", now, now, now)
	_ = store.InsertVerbatimDrawer(wB, "s2", "Bash", "go build", now)
	_ = store.InsertVerbatimDrawer(wB, "s2", "Edit", "edit bar.go", now)

	// Wing C: 0 drawers

	wings, err := store.ListWingsWithStats()
	if err != nil {
		t.Fatalf("ListWingsWithStats failed: %v", err)
	}
	if len(wings) != 3 {
		t.Fatalf("expected 3 wings, got %d", len(wings))
	}

	// Verify sorting by name ASC: a-project, b-project, c-empty-project
	if wings[0].Name != "a-project" || wings[1].Name != "b-project" || wings[2].Name != "c-empty-project" {
		t.Fatalf("unexpected wing sort order: %s, %s, %s", wings[0].Name, wings[1].Name, wings[2].Name)
	}

	// Verify wing A stats
	if wings[0].ID != wA || wings[0].Path != "/path/to/a-project" || wings[0].SummaryCount != 2 || wings[0].VerbatimCount != 1 {
		t.Fatalf("unexpected wing A stats: %+v", wings[0])
	}
	if wings[0].CreatedAt.IsZero() {
		t.Fatalf("expected non-zero CreatedAt for wing A")
	}

	// Verify wing B stats
	if wings[1].ID != wB || wings[1].Path != "/path/to/b-project" || wings[1].SummaryCount != 1 || wings[1].VerbatimCount != 2 {
		t.Fatalf("unexpected wing B stats: %+v", wings[1])
	}

	// Verify wing C stats (empty drawers)
	if wings[2].ID != wC || wings[2].Path != "/path/to/c-empty-project" || wings[2].SummaryCount != 0 || wings[2].VerbatimCount != 0 {
		t.Fatalf("unexpected wing C stats: %+v", wings[2])
	}
}

func TestStore_GetGlobalStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// 1. Empty database test
	emptyStats, err := store.GetGlobalStats()
	if err != nil {
		t.Fatalf("GetGlobalStats on empty db failed: %v", err)
	}
	if emptyStats.TotalWings != 0 || emptyStats.TotalSummaries != 0 || emptyStats.TotalVerbatim != 0 {
		t.Fatalf("expected 0 for all totals on empty db, got %+v", emptyStats)
	}
	if emptyStats.HallCounts[HallFact] != 0 || emptyStats.HallCounts[HallDiscovery] != 0 ||
		emptyStats.HallCounts[HallPreference] != 0 || emptyStats.HallCounts[HallAdvice] != 0 {
		t.Fatalf("expected all hall counts to be 0 on empty db, got %+v", emptyStats.HallCounts)
	}

	// 2. Populated database test
	w1, _ := store.GetOrCreateWing("/path/to/proj1")
	w2, _ := store.GetOrCreateWing("/path/to/proj2")
	now := time.Now().Truncate(time.Second)

	_ = store.InsertSummaryDrawer(w1, "s1", HallFact, "Fact 1", now, now, now)
	_ = store.InsertSummaryDrawer(w1, "s1", HallDiscovery, "Discovery 1", now, now, now)
	_ = store.InsertVerbatimDrawer(w1, "s1", "Read", "cat file", now)

	_ = store.InsertSummaryDrawer(w2, "s2", HallAdvice, "Advice 1", now, now, now)
	_ = store.InsertVerbatimDrawer(w2, "s2", "Bash", "make test", now)

	stats, err := store.GetGlobalStats()
	if err != nil {
		t.Fatalf("GetGlobalStats failed: %v", err)
	}

	if stats.TotalWings != 2 {
		t.Fatalf("expected 2 wings, got %d", stats.TotalWings)
	}
	if stats.TotalSummaries != 3 {
		t.Fatalf("expected 3 summaries, got %d", stats.TotalSummaries)
	}
	if stats.TotalVerbatim != 2 {
		t.Fatalf("expected 2 verbatim, got %d", stats.TotalVerbatim)
	}

	if stats.HallCounts[HallFact] != 1 {
		t.Fatalf("expected 1 fact, got %d", stats.HallCounts[HallFact])
	}
	if stats.HallCounts[HallDiscovery] != 1 {
		t.Fatalf("expected 1 discovery, got %d", stats.HallCounts[HallDiscovery])
	}
	if stats.HallCounts[HallAdvice] != 1 {
		t.Fatalf("expected 1 advice, got %d", stats.HallCounts[HallAdvice])
	}
	if stats.HallCounts[HallPreference] != 0 {
		t.Fatalf("expected 0 preference, got %d", stats.HallCounts[HallPreference])
	}
}


