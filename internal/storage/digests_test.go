package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open test store: %v", err)
	}
	return s, func() {
		_ = s.Close()
	}
}

func TestSessionDigest_UpsertAndGet(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, err := store.GetOrCreateWing("/home/user/proj")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	digest := SessionDigest{
		WingID:       wingID,
		SessionID:    "sess-123",
		Request:      "Add search feature",
		Investigated: "Checked SQLite FTS5",
		Learned:      "FTS5 is fast",
		Completed:    "Added search query",
		NextSteps:    "Add benchmarks",
		Notes:        "Keep schema clean",
		CreatedAt:    time.Now().Truncate(time.Second),
	}

	if err := store.UpsertSessionDigest(digest); err != nil {
		t.Fatalf("UpsertSessionDigest failed: %v", err)
	}

	got, err := store.GetSessionDigest("sess-123")
	if err != nil {
		t.Fatalf("GetSessionDigest failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected digest, got nil")
	}
	if got.Request != digest.Request || got.Completed != digest.Completed {
		t.Errorf("mismatched digest fields: %+v vs %+v", got, digest)
	}

	// Update existing digest
	digest.Completed = "Added search query and benchmarks"
	if err := store.UpsertSessionDigest(digest); err != nil {
		t.Fatalf("UpsertSessionDigest update failed: %v", err)
	}
	got2, err := store.GetSessionDigest("sess-123")
	if err != nil || got2.Completed != "Added search query and benchmarks" {
		t.Fatalf("failed to update digest: %v", got2)
	}
}

func TestSessionDigest_GetNonExistent(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	got, err := store.GetSessionDigest("non-existent-session")
	if err != nil {
		t.Fatalf("GetSessionDigest unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for non-existent session, got %+v", got)
	}
}

func TestSessionDigest_List(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	wingID, err := store.GetOrCreateWing("/home/user/proj")
	if err != nil {
		t.Fatalf("GetOrCreateWing failed: %v", err)
	}

	now := time.Now().Truncate(time.Second)

	for i := 1; i <= 5; i++ {
		digest := SessionDigest{
			WingID:       wingID,
			SessionID:    fmt.Sprintf("sess-%d", i),
			Request:      fmt.Sprintf("Request %d", i),
			Investigated: fmt.Sprintf("Investigated %d", i),
			Learned:      fmt.Sprintf("Learned %d", i),
			Completed:    fmt.Sprintf("Completed %d", i),
			NextSteps:    fmt.Sprintf("Next %d", i),
			Notes:        fmt.Sprintf("Notes %d", i),
			CreatedAt:    now.Add(time.Duration(i) * time.Minute),
		}
		if err := store.UpsertSessionDigest(digest); err != nil {
			t.Fatalf("UpsertSessionDigest %d failed: %v", i, err)
		}
	}

	// List with limit 3 (should return newest first: sess-5, sess-4, sess-3)
	list, err := store.ListSessionDigests(wingID, 3)
	if err != nil {
		t.Fatalf("ListSessionDigests failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 digests, got %d", len(list))
	}
	if list[0].SessionID != "sess-5" || list[1].SessionID != "sess-4" || list[2].SessionID != "sess-3" {
		t.Errorf("unexpected digest order: %v, %v, %v", list[0].SessionID, list[1].SessionID, list[2].SessionID)
	}
}
