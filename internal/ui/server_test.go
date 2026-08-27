package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestServer_SPAFallback(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	// Root path should serve index.html
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	recRoot := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recRoot, reqRoot)

	if recRoot.Code != http.StatusOK {
		t.Fatalf("expected status 200 on /, got %d", recRoot.Code)
	}
	body, _ := io.ReadAll(recRoot.Body)
	if !strings.Contains(string(body), "MemRemark Dashboard") {
		t.Fatalf("expected index.html body on /, got %q", string(body))
	}

	// Unknown client route should fallback to index.html with 200 OK
	reqClient := httptest.NewRequest(http.MethodGet, "/wings/42/timeline", nil)
	recClient := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recClient, reqClient)

	if recClient.Code != http.StatusOK {
		t.Fatalf("expected status 200 on SPA fallback, got %d", recClient.Code)
	}
	bodyClient, _ := io.ReadAll(recClient.Body)
	if !strings.Contains(string(bodyClient), "MemRemark Dashboard") {
		t.Fatalf("expected index.html body on SPA fallback, got %q", string(bodyClient))
	}

	// Unknown API route should return 404 Not Found (not SPA fallback)
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/nonexistent", nil)
	recAPI := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recAPI, reqAPI)

	if recAPI.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 on unknown /api/* route, got %d", recAPI.Code)
	}
}

func TestDigestsAPI(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	w1, err := store.GetOrCreateWing("/projects/proj-a")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}
	_, err = store.GetOrCreateWing("/projects/proj-b")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	digest1 := storage.SessionDigest{
		WingID:       w1,
		SessionID:    "sess-123",
		Request:      "Implement SQLite caching",
		Investigated: "Looked into memory vs disk trade-offs",
		Learned:      "Busy timeout avoids locking errors",
		Completed:    "Added timeout and WAL mode",
		NextSteps:    "Write benchmark tests",
		Notes:        "Ensure clean shutdown",
		CreatedAt:    now.Add(-time.Hour),
	}
	if err := store.UpsertSessionDigest(digest1); err != nil {
		t.Fatalf("UpsertSessionDigest: %v", err)
	}

	digest2 := storage.SessionDigest{
		WingID:       w1,
		SessionID:    "sess-456",
		Request:      "Refactor UI API",
		Investigated: "Checked HTTP mux routing",
		Learned:      "Go 1.22+ supports path params",
		Completed:    "Added digest endpoints",
		NextSteps:    "Connect frontend",
		Notes:        "Keep zero-overhead invariant",
		CreatedAt:    now,
	}
	if err := store.UpsertSessionDigest(digest2); err != nil {
		t.Fatalf("UpsertSessionDigest: %v", err)
	}

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	t.Run("GET /api/wings/{id}/digests returns 200 OK and list of digests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/wings/1/digests", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("expected application/json; charset=utf-8, got %q", ct)
		}

		var digests []storage.SessionDigest
		if err := json.NewDecoder(rec.Body).Decode(&digests); err != nil {
			t.Fatalf("decode digests: %v", err)
		}
		if len(digests) != 2 {
			t.Fatalf("expected 2 digests, got %d", len(digests))
		}
		// Order is newest first
		if digests[0].SessionID != "sess-456" || digests[1].SessionID != "sess-123" {
			t.Errorf("unexpected digest ordering or IDs: %+v", digests)
		}
		if digests[1].Request != "Implement SQLite caching" || digests[1].Completed != "Added timeout and WAL mode" {
			t.Errorf("unexpected digest[1] content: %+v", digests[1])
		}
	})

	t.Run("GET /api/wings/{id}/digests empty for wing without digests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/wings/2/digests", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var digests []storage.SessionDigest
		if err := json.NewDecoder(rec.Body).Decode(&digests); err != nil {
			t.Fatalf("decode digests: %v", err)
		}
		if digests == nil || len(digests) != 0 {
			t.Fatalf("expected empty non-nil slice, got %+v", digests)
		}
	})

	t.Run("GET /api/wings/{id}/digests invalid wing id returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/wings/invalid/digests", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("GET /api/sessions/{sessionId}/digest returns 200 OK and digest details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions/sess-123/digest", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("expected application/json; charset=utf-8, got %q", ct)
		}

		var digest storage.SessionDigest
		if err := json.NewDecoder(rec.Body).Decode(&digest); err != nil {
			t.Fatalf("decode digest: %v", err)
		}
		if digest.SessionID != "sess-123" {
			t.Errorf("expected session_id 'sess-123', got %q", digest.SessionID)
		}
		if digest.Request != "Implement SQLite caching" {
			t.Errorf("expected request 'Implement SQLite caching', got %q", digest.Request)
		}
		if digest.Investigated != "Looked into memory vs disk trade-offs" {
			t.Errorf("expected investigated 'Looked into memory vs disk trade-offs', got %q", digest.Investigated)
		}
		if digest.Learned != "Busy timeout avoids locking errors" {
			t.Errorf("expected learned 'Busy timeout avoids locking errors', got %q", digest.Learned)
		}
		if digest.Completed != "Added timeout and WAL mode" {
			t.Errorf("expected completed 'Added timeout and WAL mode', got %q", digest.Completed)
		}
		if digest.NextSteps != "Write benchmark tests" {
			t.Errorf("expected next_steps 'Write benchmark tests', got %q", digest.NextSteps)
		}
		if digest.Notes != "Ensure clean shutdown" {
			t.Errorf("expected notes 'Ensure clean shutdown', got %q", digest.Notes)
		}
		if digest.WingID != w1 {
			t.Errorf("expected wing_id %d, got %d", w1, digest.WingID)
		}
	})

	t.Run("GET /api/sessions/{sessionId}/digest not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent-session/digest", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})
}

func TestAPI_Timeline_IncludesNarrative(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	w1, _ := store.GetOrCreateWing("/projects/proj-a")
	now := time.Now()
	_ = store.InsertSummaryDrawer(w1, "s1", storage.HallFact, "Fact with narrative", "This is the detailed context narrative.", now, now, now)

	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	srv := NewServer(store, assets)

	req := httptest.NewRequest(http.MethodGet, "/api/timeline?wing_id=1", nil)
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
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Narrative != "This is the detailed context narrative." {
		t.Errorf("expected narrative 'This is the detailed context narrative.', got %q", items[0].Narrative)
	}
}

