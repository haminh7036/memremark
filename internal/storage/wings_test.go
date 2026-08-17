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
