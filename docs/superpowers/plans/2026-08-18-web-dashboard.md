# MemRemark Web Dashboard & Timeline Viewer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a zero-dependency local web dashboard (`memremark-ui`) with a Vue 3.5 + Tailwind v4 frontend embedded into a standalone Go binary, enabling visual inspection and filtering of distilled memory timelines across projects.

**Architecture:** A lightweight Go `net/http` backend serves REST API endpoints (`/api/wings`, `/api/timeline`, `/api/stats`) and embeds pre-compiled SPA assets via `embed.FS`. The frontend is a modern Vue 3 SPA with day-grouped timeline views, color-coded Hall badges, live keyword search, workspace filtering, and dark/light theme support.

**Tech Stack:** Go 1.26.5 (`net/http`, `embed.FS`, `database/sql`), Vue 3.5 (`vue@^3.5.41`), Vite (`vite@^8.2.1`, `@vitejs/plugin-vue@^6.0.8`), TailwindCSS v4 (`tailwindcss@^4.3.3`, `@tailwindcss/vite`), Lucide Icons (`lucide-vue-next@^1.0.0`), VueUse (`@vueuse/core@^14.4.0`).

## Global Constraints

- Go code must use standard library `net/http` and `embed.FS` with zero new external Go dependencies.
- Frontend must be completely self-contained in Go binary distribution via `//go:embed all:dist`.
- CLI binary `memremark-ui` defaults to `127.0.0.1:8765` with configurable `--host`, `--port`/`-p`, `--no-open`, `--db`.
- Tests must use `testing` package and pass with `go test -v -race ./...`.

---

### Task 1: Storage Layer Stats & Summary Queries

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/drawers.go`
- Test: `internal/storage/drawers_test.go`

**Interfaces:**
- Produces:
  - `type WingStats struct { ID int64; Path string; Name string; CreatedAt time.Time; SummaryCount int; VerbatimCount int }`
  - `type GlobalStats struct { TotalWings int; TotalSummaries int; TotalVerbatim int; HallCounts map[string]int }`
  - `(s *Store) ListWingsWithStats() ([]WingStats, error)`
  - `(s *Store) GetGlobalStats() (GlobalStats, error)`

- [ ] **Step 1: Write the failing tests in `internal/storage/drawers_test.go`**

```go
func TestStore_ListWingsWithStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	w1, _ := store.GetOrCreateWing("/path/to/project-a")
	w2, _ := store.GetOrCreateWing("/path/to/project-b")

	now := time.Now()
	_ = store.InsertSummaryDrawer(w1, "s1", HallFact, "Fact 1", now, now, now)
	_ = store.InsertSummaryDrawer(w1, "s1", HallDiscovery, "Discovery 1", now, now, now)
	_ = store.InsertVerbatimDrawer(w1, "s1", "tool_read", "content 1", now)
	_ = store.InsertSummaryDrawer(w2, "s2", HallAdvice, "Advice 1", now, now, now)

	wings, err := store.ListWingsWithStats()
	if err != nil {
		t.Fatalf("ListWingsWithStats failed: %v", err)
	}
	if len(wings) != 2 {
		t.Fatalf("expected 2 wings, got %d", len(wings))
	}
	if wings[0].SummaryCount+wings[1].SummaryCount != 3 {
		t.Fatalf("expected total 3 summaries across wings, got %d and %d", wings[0].SummaryCount, wings[1].SummaryCount)
	}
}

func TestStore_GetGlobalStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	w1, _ := store.GetOrCreateWing("/path/to/project-a")
	now := time.Now()
	_ = store.InsertSummaryDrawer(w1, "s1", HallFact, "Fact 1", now, now, now)
	_ = store.InsertSummaryDrawer(w1, "s1", HallDiscovery, "Discovery 1", now, now, now)
	_ = store.InsertVerbatimDrawer(w1, "s1", "tool_read", "content 1", now)

	stats, err := store.GetGlobalStats()
	if err != nil {
		t.Fatalf("GetGlobalStats failed: %v", err)
	}
	if stats.TotalWings != 1 || stats.TotalSummaries != 2 || stats.TotalVerbatim != 1 {
		t.Fatalf("unexpected global stats: %+v", stats)
	}
	if stats.HallCounts[HallFact] != 1 || stats.HallCounts[HallDiscovery] != 1 {
		t.Fatalf("unexpected hall counts: %+v", stats.HallCounts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/storage -run "TestStore_ListWingsWithStats|TestStore_GetGlobalStats"`
Expected: FAIL with undefined `ListWingsWithStats` / `GetGlobalStats`

- [ ] **Step 3: Implement `ListWingsWithStats` and `GetGlobalStats` in `internal/storage/drawers.go`**

```go
// WingStats contains a wing's metadata and drawer counts.
type WingStats struct {
	ID            int64     `json:"id"`
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	SummaryCount  int       `json:"summary_count"`
	VerbatimCount int       `json:"verbatim_count"`
}

// GlobalStats contains overall repository statistics.
type GlobalStats struct {
	TotalWings     int            `json:"total_wings"`
	TotalSummaries int            `json:"total_summaries"`
	TotalVerbatim  int            `json:"total_verbatim"`
	HallCounts     map[string]int `json:"halls"`
}

// ListWingsWithStats returns every wing along with summary and verbatim counts.
func (s *Store) ListWingsWithStats() ([]WingStats, error) {
	rows, err := s.db.Query(`
		SELECT 
			w.id, w.path, w.name, w.created_at,
			COALESCE(SUM(CASE WHEN d.type = 'summary' THEN 1 ELSE 0 END), 0) AS summary_count,
			COALESCE(SUM(CASE WHEN d.type = 'verbatim' THEN 1 ELSE 0 END), 0) AS verbatim_count
		FROM wings w
		LEFT JOIN drawers d ON w.id = d.wing_id
		GROUP BY w.id
		ORDER BY w.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: list wings with stats: %w", err)
	}
	defer rows.Close()

	var out []WingStats
	for rows.Next() {
		var ws WingStats
		var createdAt int64
		if err := rows.Scan(&ws.ID, &ws.Path, &ws.Name, &createdAt, &ws.SummaryCount, &ws.VerbatimCount); err != nil {
			return nil, fmt.Errorf("storage: scan wing stats: %w", err)
		}
		ws.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, ws)
	}
	return out, rows.Err()
}

// GetGlobalStats calculates total wing, summary, verbatim, and per-hall counts.
func (s *Store) GetGlobalStats() (GlobalStats, error) {
	var stats GlobalStats
	stats.HallCounts = make(map[string]int)

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wings`).Scan(&stats.TotalWings); err != nil {
		return stats, fmt.Errorf("storage: count wings: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM drawers WHERE type = 'summary'`).Scan(&stats.TotalSummaries); err != nil {
		return stats, fmt.Errorf("storage: count summaries: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM drawers WHERE type = 'verbatim'`).Scan(&stats.TotalVerbatim); err != nil {
		return stats, fmt.Errorf("storage: count verbatim: %w", err)
	}

	rows, err := s.db.Query(`SELECT hall, COUNT(*) FROM drawers WHERE type = 'summary' GROUP BY hall`)
	if err != nil {
		return stats, fmt.Errorf("storage: count halls: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var hall string
		var count int
		if err := rows.Scan(&hall, &count); err != nil {
			return stats, fmt.Errorf("storage: scan hall count: %w", err)
		}
		stats.HallCounts[hall] = count
	}
	return stats, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/storage -run "TestStore_ListWingsWithStats|TestStore_GetGlobalStats"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/drawers.go internal/storage/drawers_test.go
git commit -m "feat(storage): add ListWingsWithStats and GetGlobalStats queries"
```

---

### Task 2: Backend REST API & HTTP Server (`internal/ui`)

**Files:**
- Create: `internal/ui/assets.go`
- Create: `internal/ui/api.go`
- Create: `internal/ui/server.go`
- Test: `internal/ui/api_test.go`
- Test: `internal/ui/server_test.go`
- Create: `internal/ui/dist/index.html` (initial placeholder)

**Interfaces:**
- Consumes: `internal/storage.Store`
- Produces:
  - `type Server struct`
  - `NewServer(store *storage.Store, assets fs.FS) *Server`
  - `(s *Server) Handler() http.Handler`
  - `(s *Server) Serve(ctx context.Context, addr string) error`

- [ ] **Step 1: Create initial placeholder asset and `internal/ui/assets.go`**

```go
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embeddedDist embed.FS

// Assets returns the filesystem rooted inside the dist directory.
func Assets() (fs.FS, error) {
	return fs.Sub(embeddedDist, "dist")
}
```

- [ ] **Step 2: Write API & Server tests in `internal/ui/api_test.go` and `internal/ui/server_test.go`**

```go
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

func TestAPI_Wings(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	wID, _ := store.GetOrCreateWing("/test/wing")
	_ = store.InsertSummaryDrawer(wID, "s1", storage.HallFact, "Fact content", time.Now(), time.Now(), time.Now())

	assets, _ := Assets()
	srv := NewServer(store, assets)

	req := httptest.NewRequest("GET", "/api/wings", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var wings []storage.WingStats
	if err := json.NewDecoder(rec.Body).Decode(&wings); err != nil {
		t.Fatalf("decode wings: %v", err)
	}
	if len(wings) != 1 || wings[0].SummaryCount != 1 {
		t.Fatalf("unexpected wings response: %+v", wings)
	}
}

func TestAPI_Timeline_And_Stats(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	wID, _ := store.GetOrCreateWing("/test/wing")
	_ = store.InsertSummaryDrawer(wID, "s1", storage.HallDiscovery, "Found a bug", time.Now(), time.Now(), time.Now())

	assets, _ := Assets()
	srv := NewServer(store, assets)

	// Timeline
	req := httptest.NewRequest("GET", "/api/timeline?hall=discovery&q=bug", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected timeline status 200, got %d", rec.Code)
	}
	var timeline []DrawerItem
	if err := json.NewDecoder(rec.Body).Decode(&timeline); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(timeline) != 1 || timeline[0].Hall != "discovery" {
		t.Fatalf("unexpected timeline response: %+v", timeline)
	}

	// Stats
	reqStats := httptest.NewRequest("GET", "/api/stats", nil)
	recStats := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recStats, reqStats)

	if recStats.Code != http.StatusOK {
		t.Fatalf("expected stats status 200, got %d", recStats.Code)
	}
	var stats storage.GlobalStats
	if err := json.NewDecoder(recStats.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.TotalSummaries != 1 || stats.HallCounts["discovery"] != 1 {
		t.Fatalf("unexpected stats response: %+v", stats)
	}
}

func TestServer_SPAFallback(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	assets, _ := Assets()
	srv := NewServer(store, assets)

	req := httptest.NewRequest("GET", "/some/client/route", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 on SPA fallback, got %d", rec.Code)
	}
}
```

- [ ] **Step 3: Implement `internal/ui/api.go` and `internal/ui/server.go`**

```go
package ui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

type DrawerItem struct {
	ID         int64     `json:"id"`
	WingID     int64     `json:"wing_id"`
	Type       string    `json:"type"`
	Hall       string    `json:"hall"`
	ToolName   string    `json:"tool_name,omitempty"`
	Content    string    `json:"content"`
	SessionID  string    `json:"session_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type Server struct {
	store  *storage.Store
	assets fs.FS
	mux    *http.ServeMux
}

func NewServer(store *storage.Store, assets fs.FS) *Server {
	s := &Server{
		store:  store,
		assets: assets,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/wings", s.handleWings)
	s.mux.HandleFunc("GET /api/timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)

	// Static asset server with SPA fallback
	fileServer := http.FileServer(http.FS(s.assets))
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// Check if file exists in assets
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(s.assets, path); err != nil {
			// Fallback to index.html for SPA routing
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleWings(w http.ResponseWriter, r *http.Request) {
	wings, err := s.store.ListWingsWithStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, wings)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wingID, _ := strconv.ParseInt(q.Get("wing_id"), 10, 64)
	hall := q.Get("hall")
	drawerType := q.Get("type")
	query := q.Get("q")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	drawers, err := s.store.SearchDrawers(wingID, query, hall, drawerType, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	var items []DrawerItem
	for _, d := range drawers {
		items = append(items, DrawerItem{
			ID:        d.ID,
			WingID:    wingID,
			Type:      drawerType,
			Hall:      d.Hall,
			ToolName:  d.ToolName,
			Content:   d.Content,
			CreatedAt: d.CreatedAt,
		})
	}
	if items == nil {
		items = []DrawerItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetGlobalStats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/ui/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ui/
git commit -m "feat(ui): add REST API endpoints and static SPA server"
```

---

### Task 3: Vue 3 + TailwindCSS v4 Frontend (`web/`)

**Files:**
- Create: `web/package.json`
- Create: `web/vite.config.js`
- Create: `web/index.html`
- Create: `web/src/main.js`
- Create: `web/src/style.css`
- Create: `web/src/App.vue`
- Create: `web/src/components/Header.vue`
- Create: `web/src/components/ControlBar.vue`
- Create: `web/src/components/TimelineView.vue`
- Create: `web/src/components/MemoryCard.vue`
- Create: `web/src/components/EmptyState.vue`

- [ ] **Step 1: Create `web/package.json` and `web/vite.config.js`**

`web/package.json`:
```json
{
  "name": "memremark-web",
  "private": true,
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "@vueuse/core": "^14.4.0",
    "lucide-vue-next": "^1.0.0",
    "vue": "^3.5.41"
  },
  "devDependencies": {
    "@tailwindcss/vite": "^4.3.3",
    "@vitejs/plugin-vue": "^6.0.8",
    "tailwindcss": "^4.3.3",
    "vite": "^8.2.1"
  }
}
```

`web/vite.config.js`:
```javascript
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [
    vue(),
    tailwindcss()
  ],
  build: {
    outDir: '../internal/ui/dist',
    emptyOutDir: true
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8765'
    }
  }
})
```

- [ ] **Step 2: Create `web/index.html` and `web/src/style.css`**

`web/src/style.css`:
```css
@import "tailwindcss";

@layer base {
  body {
    @apply bg-zinc-950 text-zinc-100 font-sans antialiased min-h-screen selection:bg-indigo-500/30 selection:text-indigo-200;
  }
}
```

- [ ] **Step 3: Implement Vue Components**
  - Implement `Header.vue` with stats, status indicator, and dark/light toggle.
  - Implement `ControlBar.vue` with wing selector, live search (debounced), and Hall filter pills (`Fact`, `Discovery`, `Preference`, `Advice`, `Event`).
  - Implement `TimelineView.vue` and `MemoryCard.vue` with day-grouping, hall-specific badge styling, code-formatting, and 1-click clipboard copy.
  - Implement `EmptyState.vue` for clean feedback when search results are empty.
  - Connect full reactive store in `App.vue`.

- [ ] **Step 4: Build frontend and verify output in `internal/ui/dist`**

Run: `cd web && npm install && npm run build`
Expected: Production bundle created cleanly in `internal/ui/dist/` (`index.html`, `assets/*.js`, `assets/*.css`).

- [ ] **Step 5: Commit**

```bash
git add web/ internal/ui/dist/
git commit -m "feat(web): build Vue 3 + Tailwind v4 timeline dashboard"
```

---

### Task 4: CLI Entry Point (`cmd/memremark-ui`) & Configuration

**Files:**
- Create: `cmd/memremark-ui/main.go`
- Test: `cmd/memremark-ui/main_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- CLI Flags: `--host`, `--port` / `-p`, `--no-open`, `--db`
- Config fields: `cfg.UI.Host`, `cfg.UI.Port`, `cfg.UI.AutoOpen`

- [ ] **Step 1: Extend `internal/config/config.go` with UI configuration support**

```go
type UIConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	AutoOpen bool   `json:"auto_open"`
}

type Config struct {
	Summarizer SummarizerConfig `json:"summarizer"`
	UI         UIConfig         `json:"ui"`
}
```

- [ ] **Step 2: Write tests in `internal/config/config_test.go` and `cmd/memremark-ui/main_test.go`**

```go
func TestConfig_UILoadDefaultsAndEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MEMREMARK_UI_HOST", "0.0.0.0")
	t.Setenv("MEMREMARK_UI_PORT", "9999")

	cfg, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UI.Host != "0.0.0.0" || cfg.UI.Port != 9999 {
		t.Fatalf("unexpected UI config: %+v", cfg.UI)
	}
}
```

- [ ] **Step 3: Implement `cmd/memremark-ui/main.go`**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/ui"
)

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("memremark-ui: resolve home dir: %v", err)
	}

	cfg, _ := config.Load(home)

	hostFlag := flag.String("host", "", "Host address to listen on (default: 127.0.0.1)")
	portFlag := flag.Int("port", 0, "Port to listen on (default: 8765)")
	flag.IntVar(portFlag, "p", 0, "Port to listen on (shorthand)")
	noOpenFlag := flag.Bool("no-open", false, "Do not open browser automatically")
	dbFlag := flag.String("db", "", "SQLite database path")
	flag.Parse()

	host := "127.0.0.1"
	if cfg.UI.Host != "" {
		host = cfg.UI.Host
	}
	if *hostFlag != "" {
		host = *hostFlag
	}

	port := 8765
	if cfg.UI.Port > 0 {
		port = cfg.UI.Port
	}
	if *portFlag > 0 {
		port = *portFlag
	}

	dbPath := filepath.Join(home, ".memremark", "memremark.db")
	if *dbFlag != "" {
		dbPath = *dbFlag
	}

	store, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("memremark-ui: open database %s: %v", dbPath, err)
	}
	defer store.Close()

	assets, err := ui.Assets()
	if err != nil {
		log.Fatalf("memremark-ui: load static assets: %v", err)
	}

	server := ui.NewServer(store, assets)
	addr := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("memremark-ui: failed to listen on %s: %v", addr, err)
	}

	httpServer := &http.Server{
		Handler: server.Handler(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	url := fmt.Sprintf("http://localhost:%d", port)
	if host != "127.0.0.1" && host != "localhost" && host != "0.0.0.0" {
		url = fmt.Sprintf("http://%s:%d", host, port)
	}
	log.Printf("MemRemark Dashboard running at %s (listening on %s)", url, addr)

	if !*noOpenFlag {
		go func() {
			time.Sleep(100 * time.Millisecond)
			openBrowser(url)
		}()
	}

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("memremark-ui: server error: %v", err)
	}
	log.Println("MemRemark Dashboard stopped.")
}
```

- [ ] **Step 4: Run unit tests**

Run: `go test -v ./internal/config/... ./cmd/memremark-ui/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/memremark-ui/ internal/config/
git commit -m "feat(ui): add memremark-ui CLI command with customizable host and port"
```

---

### Task 5: Makefile & Installer Integration & E2E Verification

**Files:**
- Modify: `Makefile`
- Modify: `install.sh`

- [ ] **Step 1: Update `Makefile`**
  - Add `build-web` target (`cd web && npm run build`).
  - Add `memremark-ui` compilation to `build` target (`go build -o bin/memremark-ui ./cmd/memremark-ui`).

- [ ] **Step 2: Update `install.sh`**
  - Add `memremark-ui` to the list of binaries built and copied to `$BINDIR`.

- [ ] **Step 3: Run full test suite and compilation**

Run: `make test && make build`
Expected: 5 binaries compiled cleanly in `bin/` (`memremarkd`, `memremark-hook-claude-sessionstart`, `memremark-hook-antigravity-preinvocation`, `memremark-mcp`, `memremark-ui`).

- [ ] **Step 4: Run End-to-End smoke test on CLI**

```bash
./bin/memremark-ui --no-open --port 8769 &
PID=$!
sleep 1
curl -s http://127.0.0.1:8769/api/wings
curl -s http://127.0.0.1:8769/api/stats
curl -s http://127.0.0.1:8769/ | grep -i "MemRemark"
kill $PID
```
Expected: HTTP 200 OK responses with valid JSON and HTML.

- [ ] **Step 5: Commit**

```bash
git add Makefile install.sh
git commit -m "feat(build): integrate memremark-ui into Makefile and install script"
```
