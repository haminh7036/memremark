package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func setupTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	return store
}

func TestAPI_Wings_Empty(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	req := httptest.NewRequest(http.MethodGet, "/api/wings", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("expected application/json; charset=utf-8, got %q", ct)
	}

	var wings []storage.WingStats
	if err := json.NewDecoder(rec.Body).Decode(&wings); err != nil {
		t.Fatalf("decode wings: %v", err)
	}
	if wings == nil || len(wings) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v", wings)
	}
}

func TestAPI_Wings_WithData(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	w1, err := store.GetOrCreateWing("/projects/proj-a")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	w2, err := store.GetOrCreateWing("/projects/proj-b")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now()
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallFact, "Fact A", now, now, now)
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallDiscovery, "Discovery A", now, now, now)
	_ = store.InsertVerbatimDrawer(w1, "s1", "tool_read", "read something", now)
	_ = store.InsertSummaryDrawer(w2, "s2", storage.HallAdvice, "Advice B", now, now, now)

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	req := httptest.NewRequest(http.MethodGet, "/api/wings", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var wings []storage.WingStats
	if err := json.NewDecoder(rec.Body).Decode(&wings); err != nil {
		t.Fatalf("decode wings: %v", err)
	}
	if len(wings) != 2 {
		t.Fatalf("expected 2 wings, got %d", len(wings))
	}
	if wings[0].SummaryCount+wings[1].SummaryCount != 3 {
		t.Fatalf("expected total 3 summaries, got %d and %d", wings[0].SummaryCount, wings[1].SummaryCount)
	}
}

func TestAPI_Timeline_Empty(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	req := httptest.NewRequest(http.MethodGet, "/api/timeline", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var items []DrawerItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("expected empty non-nil slice, got %+v", items)
	}
}

func TestAPI_Timeline_Filters(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	w1, _ := store.GetOrCreateWing("/projects/proj-a")
	w2, _ := store.GetOrCreateWing("/projects/proj-b")

	now := time.Now()
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallFact, "Database password is xyz", now, now, now)
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallDiscovery, "Found performance bottleneck in query", now, now, now)
	_ = store.InsertVerbatimDrawer(w1, "s1", "exec_cmd", "running tests", now)
	_ = store.InsertSummaryDrawer(w2, "s2", storage.HallDiscovery, "Found redis memory leak", now, now, now)

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	// Filter by hall=discovery and q=bottleneck with wing_id
	req := httptest.NewRequest(http.MethodGet, "/api/timeline?wing_id=1&hall=discovery&q=bottleneck", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var items []DrawerItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(items), items)
	}
	if items[0].Hall != "discovery" || items[0].WingID != 1 {
		t.Fatalf("unexpected item: %+v", items[0])
	}

	// Filter by type=verbatim
	reqVerbatim := httptest.NewRequest(http.MethodGet, "/api/timeline?type=verbatim", nil)
	recVerbatim := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recVerbatim, reqVerbatim)

	if recVerbatim.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recVerbatim.Code)
	}

	var verbatimItems []DrawerItem
	if err := json.NewDecoder(recVerbatim.Body).Decode(&verbatimItems); err != nil {
		t.Fatalf("decode verbatim items: %v", err)
	}
	if len(verbatimItems) != 1 || verbatimItems[0].ToolName != "exec_cmd" || verbatimItems[0].WingID != w1 || verbatimItems[0].SessionID != "s1" {
		t.Fatalf("unexpected verbatim items: %+v", verbatimItems)
	}
}

func TestAPI_Timeline_AllWorkspacesPreservesWingID(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	w1, _ := store.GetOrCreateWing("/projects/proj-a")
	w2, _ := store.GetOrCreateWing("/projects/proj-b")

	now := time.Now().Truncate(time.Second)
	coversFrom := now.Add(-time.Hour).Truncate(time.Second)
	coversTo := now.Truncate(time.Second)

	_ = store.InsertSummaryDrawer(w1, "session-1", storage.HallFact, "Fact on proj A", coversFrom, coversTo, now)
	_ = store.InsertVerbatimDrawer(w2, "session-2", "Bash", "ls -la on proj B", now.Add(time.Second))

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	// Query across all workspaces (wing_id=0 or omitted)
	req := httptest.NewRequest(http.MethodGet, "/api/timeline?wing_id=0", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var items []DrawerItem
	if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}

	// Results are ordered newest first (created_at DESC): item 0 is w2 verbatim, item 1 is w1 summary
	if items[0].WingID != w2 || items[0].Type != "verbatim" || items[0].SessionID != "session-2" {
		t.Fatalf("expected item 0 to have WingID=%d, Type=verbatim, SessionID=session-2, got %+v", w2, items[0])
	}
	if items[1].WingID != w1 || items[1].Type != "summary" || items[1].SessionID != "session-1" || items[1].CoversFrom != coversFrom.Unix() || items[1].CoversTo != coversTo.Unix() {
		t.Fatalf("expected item 1 to have WingID=%d, Type=summary, SessionID=session-1, CoversFrom/To set, got %+v", w1, items[1])
	}
}

func TestAPI_Stats(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	w1, _ := store.GetOrCreateWing("/projects/proj-a")
	now := time.Now()
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallFact, "Fact 1", now, now, now)
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallDiscovery, "Discovery 1", now, now, now)
	_ = store.InsertVerbatimDrawer(w1, "s1", "tool_read", "content 1", now)

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var stats storage.GlobalStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.TotalWings != 1 || stats.TotalSummaries != 2 || stats.TotalVerbatim != 1 {
		t.Fatalf("unexpected global stats: %+v", stats)
	}
	if stats.HallCounts[storage.HallFact] != 1 || stats.HallCounts[storage.HallDiscovery] != 1 {
		t.Fatalf("unexpected hall counts: %+v", stats.HallCounts)
	}
}
