package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
