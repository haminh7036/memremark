package storage

import (
	"path/filepath"
	"sync"
	"testing"
)

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

func TestGetOrCreateWingNormalizesURIPaths(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id1, err := s.GetOrCreateWing(`["file:///home/minh/personal/memremark"]`)
	if err != nil {
		t.Fatalf("GetOrCreateWing with JSON array URI: %v", err)
	}

	id2, err := s.GetOrCreateWing("/home/minh/personal/memremark")
	if err != nil {
		t.Fatalf("GetOrCreateWing with clean path: %v", err)
	}

	if id1 != id2 {
		t.Fatalf("expected URI and clean path to map to identical wing id, got %d and %d", id1, id2)
	}

	var name, path string
	err = s.db.QueryRow(`SELECT name, path FROM wings WHERE id = ?`, id1).Scan(&name, &path)
	if err != nil {
		t.Fatalf("query wing: %v", err)
	}
	if name != "memremark" {
		t.Fatalf("expected name 'memremark', got %q", name)
	}
	if path != "/home/minh/personal/memremark" {
		t.Fatalf("expected path '/home/minh/personal/memremark', got %q", path)
	}
}

func TestMigrateLegacyWings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Insert legacy unnormalized rows directly
	_, err = s.db.Exec(`INSERT INTO wings (id, path, name, created_at) VALUES (1, '["file:///home/minh/personal/legacy1"]', 'legacy1"]', 100)`)
	if err != nil {
		t.Fatalf("insert legacy wing 1: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO wings (id, path, name, created_at) VALUES (2, '/home/minh/personal/legacy2', 'legacy2', 100)`)
	if err != nil {
		t.Fatalf("insert wing 2: %v", err)
	}
	_, err = s.db.Exec(`INSERT INTO wings (id, path, name, created_at) VALUES (3, '["file:///home/minh/personal/legacy2"]', 'legacy2"]', 100)`)
	if err != nil {
		t.Fatalf("insert duplicate legacy wing 3: %v", err)
	}
	// Insert drawers pointing to legacy wing 3
	_, err = s.db.Exec(`INSERT INTO drawers (id, wing_id, type, hall, content, session_id, created_at) VALUES (1, 3, 'summary', 'fact', 'legacy fact', 's1', 100)`)
	if err != nil {
		t.Fatalf("insert drawer for wing 3: %v", err)
	}
	s.Close()

	// Reopen store to trigger migration
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open s2 (migrate): %v", err)
	}
	defer s2.Close()

	// Check legacy 1 updated in-place
	var name1, path1 string
	err = s2.db.QueryRow(`SELECT name, path FROM wings WHERE id = 1`).Scan(&name1, &path1)
	if err != nil {
		t.Fatalf("query wing 1: %v", err)
	}
	if name1 != "legacy1" || path1 != "/home/minh/personal/legacy1" {
		t.Fatalf("unexpected wing 1 after migration: name=%q path=%q", name1, path1)
	}

	// Check legacy 3 merged into 2 and deleted
	var count3 int
	s2.db.QueryRow(`SELECT count(*) FROM wings WHERE id = 3`).Scan(&count3)
	if count3 != 0 {
		t.Fatalf("expected duplicate legacy wing 3 to be deleted, count=%d", count3)
	}

	// Check drawer re-pointed to wing 2
	var drawerWingID int64
	err = s2.db.QueryRow(`SELECT wing_id FROM drawers WHERE id = 1`).Scan(&drawerWingID)
	if err != nil {
		t.Fatalf("query drawer: %v", err)
	}
	if drawerWingID != 2 {
		t.Fatalf("expected drawer wing_id to be re-pointed to 2, got %d", drawerWingID)
	}
}
