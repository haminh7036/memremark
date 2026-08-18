# MemRemark Web Dashboard & Timeline Viewer Design Specification

- **Date:** 2026-08-18
- **Author:** Antigravity & User
- **Status:** Draft / Approved Design
- **Target Release:** v1.1.0

---

## 1. Overview & Motivation

MemRemark captures and distills developer actions and tool calls into a structured Memory Palace stored in SQLite (`~/.memremark/memremark.db`). While AI assistants access this knowledge automatically through CLI hooks (`SessionStart`, `PreInvocation`) and Model Context Protocol (`memremark-mcp`), developers currently have no visual tool to inspect, search, and verify what knowledge has been retained.

This specification defines **MemRemark Dashboard** (`memremark-ui`), a lightweight, embedded local web viewer that presents distilled knowledge across projects as an intuitive, modern, interactive timeline.

---

## 2. Goals & Non-Goals

### Goals
1. **Zero-Dependency Single Binary:** Distribute `memremark-ui` as a single, self-contained Go binary containing all frontend assets embedded via `embed.FS`. No Node.js or external runtime required on user machines.
2. **Configurable Host & Port:** Default to `127.0.0.1:8765`, with full support for custom host bindings (e.g. `0.0.0.0` for LAN / remote access), port customization, and `--no-open` flag.
3. **Modern Vue 3 + Tailwind v4 Timeline UI:** Clean, dark/light theme, day-grouped vertical timeline, Hall color coding (`fact`, `discovery`, `preference`, `advice`, `event`), instant live search, workspace switching, 1-click copy, and auto-poll toggle.
4. **Lean REST API Backend:** Pure Go standard library HTTP router interacting directly with `internal/storage`.

### Non-Goals
1. **Multi-User Authentication:** MemRemark is a single-user local tool. No complex login/RBAC system is built in v1.
2. **Web-Based Memory Editing in v1:** Keep v1 focused on high-speed, reliable inspection, filtering, and search (mutations are already handled via MCP tools `remember` / `forget_memory`).

---

## 3. Architecture & Component Boundaries

```
                    ┌──────────────────────────────────────────────┐
                    │                Browser (SPA)                 │
                    │        Vue 3.5 + Tailwind v4 + Lucide        │
                    └──────────────────────┬───────────────────────┘
                                           │ HTTP / JSON
                                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                   memremark-ui Go Binary                         │
│                                                                  │
│  ┌───────────────────────┐         ┌──────────────────────────┐  │
│  │   SPA File Server     │         │       REST API Router    │  │
│  │ (go:embed web assets) │         │   /api/wings, /timeline  │  │
│  └───────────────────────┘         └────────────┬─────────────┘  │
│                                                 │                │
└─────────────────────────────────────────────────┼────────────────┘
                                                  ▼
                                     ┌─────────────────────────┐
                                     │ internal/storage Store  │
                                     │   (~/.memremark.db)     │
                                     └─────────────────────────┘
```

### 3.1 Backend Components (`internal/ui`)
* **`cmd/memremark-ui/main.go`**: CLI entry point. Parses CLI flags (`--host`, `--port`, `--db`, `--no-open`), loads optional config overrides, initializes storage, starts the HTTP server, and triggers the browser launch.
* **`internal/ui/server.go`**: HTTP Server wrapper with graceful shutdown handling (SIGINT/SIGTERM) and custom listener bindings.
* **`internal/ui/api.go`**: JSON API handlers providing:
  * `GET /api/wings`: Returns list of workspaces (`id`, `name`, `path`, `created_at`, `drawer_count`).
  * `GET /api/timeline`: Returns drawers filtered by `wing_id`, `hall`, `type`, `q`, `limit`.
  * `GET /api/stats`: Returns overall count and per-hall breakdown statistics.
* **`internal/ui/assets.go`**: Embedded filesystem containing pre-built Vue production assets (`//go:embed dist/*`).

### 3.2 CLI Options & Configuration Precedence
1. **CLI Flags (Highest Priority):**
   * `--host <string>`: Bind address (default: `127.0.0.1`, accepts `0.0.0.0`).
   * `--port <int>`, `-p <int>`: Port to listen on (default: `8765`).
   * `--no-open`: Skip automatic browser opening (`xdg-open` / OS default browser).
   * `--db <path>`: SQLite database path (default: `~/.memremark/memremark.db`).
2. **Environment Variables:**
   * `MEMREMARK_UI_HOST`
   * `MEMREMARK_UI_PORT`
3. **Configuration File (`~/.memremark/config.json`):**
   ```json
   {
     "ui": {
       "host": "127.0.0.1",
       "port": 8765,
       "auto_open": true
     }
   }
   ```

---

## 4. Frontend Tech Stack & Component Hierarchy

### 4.1 Tech Stack
* **Vue 3.5+ (`vue@^3.5.41`)**: Composition API, `<script setup>`, single-file components.
* **Vite (`vite@^8.2.1` / `@vitejs/plugin-vue@^6.0.8`)**: High-performance bundler.
* **TailwindCSS v4 (`tailwindcss@^4.3.3` / `@tailwindcss/vite`)**: CSS framework with dark mode support.
* **Lucide Vue Next (`lucide-vue-next@^1.0.0`)**: Vector iconography.
* **VueUse (`@vueuse/core@^14.4.0`)**: Composables for `useDark`, `useClipboard`, `useDebounceFn`, and keyboard shortcuts.

### 4.2 Component Architecture
* **`App.vue`**: Root component managing global state (selected workspace, current filters, theme, auto-refresh timer).
  * **`Header.vue`**: Title, connection status indicator, quick statistics summary, dark/light theme switch, auto-poll toggle (5s/10s/Off), manual refresh button.
  * **`ControlBar.vue`**: Sticky toolbar containing:
    * Workspace dropdown selector (`All Workspaces` + list of projects).
    * Instant search input with keyboard shortcut `/` or `Ctrl+K`.
    * Hall filter pills (`All`, `Fact`, `Discovery`, `Preference`, `Advice`, `Event`).
    * Drawer type switcher (`Distilled Summary` vs `Verbatim Tool Calls`).
  * **`TimelineView.vue`**: Day-grouped feed with vertical timeline stem and indicator dots.
    * **`MemoryCard.vue`**: Card rendering Hall badge, relative/absolute timestamp, session ID tag, formatted text content with inline code / markdown styling, workspace path tag, and 1-click copy button.
  * **`EmptyState.vue`**: Clean placeholder when search returns no matching memories.

---

## 5. REST API Specifications

### 5.1 `GET /api/wings`
**Response (200 OK):**
```json
[
  {
    "id": 1,
    "name": "memremark",
    "path": "/home/minh/personal/memremark",
    "created_at": 1754829100,
    "summary_count": 142,
    "verbatim_count": 520
  }
]
```

### 5.2 `GET /api/timeline`
**Query Parameters:**
* `wing_id` *(int, optional)*: Filter by workspace ID.
* `hall` *(string, optional)*: `fact`, `discovery`, `preference`, `advice`, `event`, or empty for all.
* `type` *(string, optional)*: `summary` (default), `verbatim`, or `all`.
* `q` *(string, optional)*: Keyword query (case-insensitive substring match).
* `limit` *(int, optional)*: Max results to return (default: 50, max: 200).

**Response (200 OK):**
```json
[
  {
    "id": 17372,
    "wing_id": 1,
    "wing_name": "memremark",
    "wing_path": "/home/minh/personal/memremark",
    "type": "summary",
    "hall": "preference",
    "content": "Commit message theo Conventional Commits...",
    "tool_name": "",
    "session_id": "8f5badd9-dc0f-4940-bcf2-84c029b800d9",
    "covers_from": 1755502800,
    "covers_to": 1755503049,
    "created_at": 1755503049
  }
]
```

### 5.3 `GET /api/stats`
**Response (200 OK):**
```json
{
  "total_wings": 4,
  "total_summaries": 320,
  "total_verbatim": 1840,
  "halls": {
    "fact": 142,
    "discovery": 89,
    "preference": 45,
    "advice": 44
  }
}
```

---

## 6. Build & Packaging Workflow

### 6.1 Directory Structure
```
memremark/
├── cmd/
│   ├── memremark-ui/
│   │   └── main.go
│   └── ... (existing binaries)
├── internal/
│   ├── ui/
│   │   ├── api.go
│   │   ├── api_test.go
│   │   ├── server.go
│   │   ├── server_test.go
│   │   ├── assets.go
│   │   └── dist/              <-- Bundled HTML/JS/CSS output
│   └── ... (existing internal packages)
├── web/
│   ├── src/
│   │   ├── components/
│   │   ├── App.vue
│   │   ├── main.js
│   │   └── style.css
│   ├── index.html
│   ├── package.json
│   └── vite.config.js
```

### 6.2 Build Sequence
1. **Frontend Build:** `cd web && npm run build` (outputs optimized bundle directly to `internal/ui/dist/`).
2. **Go Binary Compilation:** `go build -o bin/memremark-ui ./cmd/memremark-ui` (embeds `internal/ui/dist/` into binary).
3. **Makefile Integration:**
   * `make build-web`: Runs Vite build.
   * `make build`: Builds all 5 binaries (`memremarkd`, `memremark-hook-claude-sessionstart`, `memremark-hook-antigravity-preinvocation`, `memremark-mcp`, `memremark-ui`).
4. **Installer Integration:** `install.sh` installs `memremark-ui` into `~/.local/bin/`.

---

## 7. Verification & Testing Strategy

1. **Backend Unit Tests:**
   * `internal/ui/api_test.go`: Test API endpoints `/api/wings`, `/api/timeline`, `/api/stats` against in-memory SQLite store.
   * `internal/ui/server_test.go`: Test custom host/port binding, SPA route fallback, and static asset delivery.
   * `cmd/memremark-ui/main_test.go`: Test CLI flag parsing and configuration fallback.
2. **Frontend Quality Verification:**
   * Verify responsive layout across Desktop and Mobile screen widths.
   * Verify instant live search, Hall filtering, dark/light theme persistence, and copy button.
3. **End-to-End CLI Verification:**
   * Run `memremark-ui --no-open --port 8769` and verify HTTP 200 responses for both frontend SPA assets and API endpoints.
