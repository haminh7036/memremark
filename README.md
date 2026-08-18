# MemRemark

[English](README.md) | [Tiếng Việt](README_vi.md)

Seamless cross-session working memory and context continuity engine for AI coding assistants (**Antigravity CLI** & **Claude Code**). MemRemark automatically captures tool usage observations, generates distilled semantic summaries via a lightweight background daemon, and injects relevant project memory into future sessions.

## Tech Stack
- **Go (Golang)**: High performance, static binary, zero-dependency.
- **Storage**: SQLite (`modernc.org/sqlite` pure-Go driver, concurrency-safe atomic operations).
- **Protocol**: Model Context Protocol (MCP) JSON-RPC over stdio.
- **Frontend Dashboard**: Vue 3.5 + TailwindCSS v4 embedded directly into the Go binary.

## Inspiration
Built with inspiration from [Claude Mem](https://github.com/thedotmack/claude-mem) and [Mempalace](https://github.com/mempalace/mempalace).

## Motivation
When working across multiple machines (e.g. Work PC, Home PC, Laptop) and restarting AI coding sessions, context is often lost, requiring developers to repeatedly re-explain project background, conventions, and architectural decisions.

MemRemark solves this by **automatically** recording, distilling, and maintaining continuous project knowledge across sessions.

---

## Project Status & Roadmap
- **Phase 1: Core Engine & MCP Server (Single Machine)** — **Complete & Fully Tested**
  - Transcript tailing & SQLite state capture for Claude Code and Antigravity CLI.
  - Headless background summarizer daemon with bidirectional auto-fallback.
  - Seamless context injection hooks (`SessionStart` and `PreInvocation`).
  - Model Context Protocol (MCP) server with 4 tools.
  - Interactive Web Dashboard (`memremark-ui`) with Vue 3.5 + Tailwind v4 timeline.
- **Phase 2: Sync Layer (Multi-Device Sync)** — *Planned*.

---

## Quick Installation (Recommended)

The `install.sh` script automatically:
1. Builds all binaries (`memremarkd`, `memremark-hook-claude-sessionstart`, `memremark-hook-antigravity-preinvocation`, `memremark-mcp`, `memremark-ui`) and installs them to `~/.local/bin/`.
2. Sets up and enables the `systemd --user` background service.
3. **Smart patches** CLI hook and MCP configurations:
   - **Antigravity CLI**: Configures `PreInvocation` hook in `~/.gemini/config/hooks.json` and MCP server in `~/.gemini/config/mcp_config.json`.
   - **Claude Code**: Configures `SessionStart` hook in `~/.claude/settings.json` and MCP server in `~/.claude/mcp.json`.
   *(Creates automated `.bak` backups and preserves all existing settings).*

```bash
# Install for both Antigravity CLI and Claude Code:
./install.sh

# Or install for Antigravity CLI only:
./install.sh --cli=antigravity-cli

# Or install for Claude Code only:
./install.sh --cli=claude-code

# Complete uninstall (removes service, binaries, hooks, and MCP servers):
./install.sh --uninstall
```

### Daemon Management
```bash
# Check daemon status:
systemctl --user status memremarkd

# Start / Stop / Restart:
systemctl --user start memremarkd
systemctl --user stop memremarkd
systemctl --user restart memremarkd

# View live daemon logs:
journalctl --user -u memremarkd -f
```

---

## Web Dashboard & Timeline Viewer (`memremark-ui`)

MemRemark includes a self-contained local Web Dashboard built with **Vue 3.5**, **TailwindCSS v4**, and **Lucide Icons** embedded directly into the Go binary.

```bash
# Launch dashboard (opens browser at http://127.0.0.1:8765):
memremark-ui

# Customize listening port or bind to 0.0.0.0 for LAN access:
memremark-ui --host 0.0.0.0 --port 9000

# Run in headless mode without opening browser (useful for remote SSH):
memremark-ui --no-open
```

### Features:
- **Interactive Timeline**: Visual vertical timeline grouped by day.
- **Hall Badges**: Color-coded categorization (`📌 Fact`, `💡 Discovery`, `⚙️ Preference`, `🎯 Advice`, `⚡ Event`).
- **Instant Live Search**: Debounced keyword search with `/` keyboard shortcut.
- **Workspace Switcher**: Filter memory by specific project repository.
- **1-Click Copy**: Copy distilled insights directly to clipboard.
- **Dark / Light Mode**: Modern aesthetic with theme persistence.

---

## MCP Server & Tools (`memremark-mcp`)

`memremark-mcp` exposes a stdio **Model Context Protocol (MCP)** interface directly connected to SQLite, enabling AI agents to search, record, and manage project memories on demand.

### Available Tools:

1. **`search_memory`**: Search memories by keyword, hall classification, drawer type, or workspace.
   - `query` *(string)*: Keyword search in memory content.
   - `hall` *(string, optional)*: Filter by classification (`fact`, `discovery`, `preference`, `advice`, `event`).
   - `type` *(string, optional)*: Filter by drawer type (`summary`, `verbatim`, or `all`).
   - `wing_path` *(string, optional)*: Workspace directory path (defaults to current working directory).
   - `limit` *(integer, optional)*: Maximum results (default: 10, max: 50).

2. **`remember`**: Explicitly record a lesson, convention, architectural decision, or discovery.
   - `content` *(string, required)*: Content to memorize.
   - `hall` *(string, required)*: Classification (`fact`, `discovery`, `preference`, `advice`).
   - `wing_path` *(string, optional)*: Workspace directory path (defaults to current working directory).

3. **`get_timeline`**: Retrieve chronologically ordered events and summaries.
   - `session_id` *(string, optional)*: Filter by specific session ID.
   - `wing_path` *(string, optional)*: Workspace directory path (defaults to current working directory).
   - `since` *(integer, optional)*: Unix timestamp threshold.
   - `limit` *(integer, optional)*: Maximum items (default: 20, max: 100).

4. **`forget_memory`**: Delete an outdated or incorrect memory drawer by ID.
   - `id` *(integer, required)*: Drawer ID to delete.

---

## Default File Locations

| Item | Path | Description |
| :--- | :--- | :--- |
| **Database** | `~/.memremark/memremark.db` | SQLite store: wings, verbatim observations, distilled summaries, poll watermark |
| **Configuration** | `~/.memremark/config.json` | Model & UI settings (`claude_model`, `antigravity_model`, `ui.port`, etc.) |
| **Binaries** | `~/.local/bin/memremark*` | Installed executables (`memremarkd`, hooks, MCP server, UI) |
| **Systemd Service** | `~/.config/systemd/user/memremarkd.service` | User-level background daemon unit |
| **Antigravity Hook** | `~/.gemini/config/hooks.json` | `PreInvocation` hook configuration |
| **Antigravity MCP** | `~/.gemini/config/mcp_config.json` | MCP server entry for Antigravity CLI |
| **Claude Code Hook** | `~/.claude/settings.json` | `SessionStart` hook configuration |
| **Claude Code MCP** | `~/.claude/mcp.json` | MCP server entry for Claude Code |

---

## Configuration (`config.json`)

By default, `memremarkd` uses the lightest and fastest models to minimize token costs and RAM usage (`haiku` for Claude Code, `gemini-3.7-flash-low` with `--effort low` for Antigravity CLI).

You can customize models and UI settings in `~/.memremark/config.json`:

```json
{
  "summarizer": {
    "claude_model": "haiku",
    "antigravity_model": "gemini-3.7-flash-low",
    "antigravity_effort": "low"
  },
  "ui": {
    "host": "127.0.0.1",
    "port": 8765,
    "auto_open": true
  }
}
```

* **Precedence:** Environment variables (`MEMREMARK_*`) > `config.json` > Default fallback.
* **Environment variables:**
  - `MEMREMARK_CLAUDE_MODEL`: Override Claude model (e.g. `haiku`, `claude-3-5-sonnet`, `default`).
  - `MEMREMARK_ANTIGRAVITY_MODEL`: Override Antigravity model (e.g. `gemini-3.7-flash-low`, `flash_lite`, `default`).
  - `MEMREMARK_ANTIGRAVITY_EFFORT`: Override effort level (`low`, `medium`, `high`, `default`).
  - `MEMREMARK_UI_HOST`: Custom UI host binding.
  - `MEMREMARK_UI_PORT`: Custom UI port.

---

## Manual Configuration (Without `install.sh`)

### 1. Build Binaries
```bash
go build -o bin/memremarkd ./cmd/memremarkd
go build -o bin/memremark-hook-claude-sessionstart ./cmd/memremark-hook-claude-sessionstart
go build -o bin/memremark-hook-antigravity-preinvocation ./cmd/memremark-hook-antigravity-preinvocation
go build -o bin/memremark-mcp ./cmd/memremark-mcp
go build -o bin/memremark-ui ./cmd/memremark-ui
```

### 2. Configure Claude Code Hooks & MCP
Add to `~/.claude/settings.json`:
```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/memremark-hook-claude-sessionstart"
          }
        ]
      }
    ]
  }
}
```

Add to `~/.claude/mcp.json`:
```json
{
  "mcpServers": {
    "memremark": {
      "command": "/path/to/memremark-mcp"
    }
  }
}
```

### 3. Configure Antigravity CLI Hooks & MCP
Add to `~/.gemini/config/hooks.json`:
```json
{
  "memremark": {
    "PreInvocation": [
      {
        "type": "command",
        "command": "/path/to/memremark-hook-antigravity-preinvocation",
        "timeout": 5
      }
    ]
  }
}
```

Add to `~/.gemini/config/mcp_config.json`:
```json
{
  "mcpServers": {
    "memremark": {
      "command": "/path/to/memremark-mcp"
    }
  }
}
```

---

## Limitations
- Extraction from Antigravity CLI (`internal/adapter/antigravity`) uses schema-less Protobuf wire scanning over SQLite blobs (sufficient for distilled memory, but raw structured arguments are not separately typed).
- Multi-device sync (Sync Layer) is scheduled for Phase 2.
