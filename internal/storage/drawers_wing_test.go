package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUnsummarizedVerbatimByWing(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/path/to/project")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	now := time.Now()
	// Insert verbatim across 2 different sessions in the same wing
	_ = store.InsertVerbatimDrawer(wingID, "sess-1", "bash", "ls -la", now)
	_ = store.InsertVerbatimDrawer(wingID, "sess-2", "edit", "main.go", now.Add(time.Second))
	_ = store.InsertVerbatimDrawer(wingID, "sess-1", "read", "go.mod", now.Add(2*time.Second))

	// Retrieve all unsummarized verbatim for the wing
	rows, err := store.UnsummarizedVerbatimByWing(wingID, 100)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing failed: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows across sessions, got %d", len(rows))
	}
	if rows[0].SessionID != "sess-1" || rows[1].SessionID != "sess-2" || rows[2].SessionID != "sess-1" {
		t.Fatalf("unexpected ordering: %+v", rows)
	}

	// Test OrphanedVerbatimWings
	wings, err := store.OrphanedVerbatimWings()
	if err != nil {
		t.Fatalf("OrphanedVerbatimWings failed: %v", err)
	}
	if len(wings) != 1 || wings[0] != wingID {
		t.Fatalf("expected wingID %d, got %v", wingID, wings)
	}

	// Once deleted, wing is no longer orphaned
	_ = store.DeleteDrawers([]int64{rows[0].ID, rows[1].ID, rows[2].ID})
	wingsAfter, err := store.OrphanedVerbatimWings()
	if err != nil {
		t.Fatalf("OrphanedVerbatimWings failed: %v", err)
	}
	if len(wingsAfter) != 0 {
		t.Fatalf("expected 0 orphaned wings, got %d", len(wingsAfter))
	}
}

func TestUnsummarizedVerbatimByWing_DefaultLimitAndIsolation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	wing1, err := store.GetOrCreateWing("/path/to/proj1")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}
	wing2, err := store.GetOrCreateWing("/path/to/proj2")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	now := time.Now()
	_ = store.InsertVerbatimDrawer(wing1, "sess-a", "bash", "cmd 1", now)
	_ = store.InsertVerbatimDrawer(wing2, "sess-b", "bash", "cmd 2", now)

	// limit <= 0 should default to 500
	rows, err := store.UnsummarizedVerbatimByWing(wing1, 0)
	if err != nil {
		t.Fatalf("UnsummarizedVerbatimByWing failed: %v", err)
	}
	if len(rows) != 1 || rows[0].Content != "cmd 1" {
		t.Fatalf("expected 1 row for wing1, got %+v", rows)
	}

	wings, err := store.OrphanedVerbatimWings()
	if err != nil {
		t.Fatalf("OrphanedVerbatimWings failed: %v", err)
	}
	if len(wings) != 2 || wings[0] != wing1 || wings[1] != wing2 {
		t.Fatalf("expected wings [%d, %d], got %v", wing1, wing2, wings)
	}
}
