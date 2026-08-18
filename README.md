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

## Architecture & Working Principles

MemRemark operates as an **Active Memory Palace**:
1. **Passive Observation**: Asynchronously tails CLI transcripts and tool interactions from Claude Code and Antigravity CLI without introducing runtime overhead.
2. **Semantic Distillation**: A lightweight background daemon leverages low-cost models (`haiku`, `gemini-3.7-flash-low`) to distill session logs into structured SQLite drawers (`fact`, `discovery`, `preference`, `advice`).
3. **Zero-Latency Context Injection**: Automatically injects relevant workspace knowledge into new sessions via CLI hooks (`SessionStart`, `PreInvocation`).
4. **Active Knowledge Retrieval**: Empowers AI agents to explicitly search, record, and prune project memories on demand via Model Context Protocol (MCP) tools.
5. **Timeline Dashboard**: A standalone local web viewer (`memremark-ui`) with a Vue 3.5 + Tailwind v4 interactive timeline.

---

## Project Status & Roadmap
- **Phase 1: Core Engine, MCP Server & Web Dashboard (Single Host)** — **`v0.1.2` (Complete & Tested)**
  - Transcript tailing & SQLite state capture for Claude Code and Antigravity CLI.
  - Headless background summarizer daemon with **dynamic startup capability discovery** and bidirectional auto-fallback.
  - **Locale-Adaptive Knowledge Distillation**: Auto-detects user environment locale (`$LANG`, `$LC_ALL`) with dual-layer tech term preservation and multi-language context injection headers.
  - **Single-CLI & Multi-CLI Standalone Support**: Runs seamlessly on machines with only Claude Code, only Antigravity CLI, or both.
  - Seamless context injection hooks (`memremark-hook-claude` and `memremark-hook-agy`).
  - Model Context Protocol (MCP) server with 4 tools (`search_memory`, `remember`, `get_timeline`, `forget_memory`).
  - Interactive Web Dashboard (`memremark-ui`) with Vue 3.5 + Tailwind v4 timeline.
  - Smart installer (`install.sh`) with automatic CLI detection and selective configuration.
- **Phase 2: Sync Layer (Multi-Device Sync)** — *Planned (`v0.2.0`)*.

### Versioning Convention (Zero-Versioning)
MemRemark follows a **Zero-Versioning (`0.Y.Z`)** policy:
- **`v0.1.0` (Phase 1 Baseline)**: Feature-complete single-host release (daemon, hooks, MCP server, web UI).
- **`v0.1.x`**: Bug fixes, performance improvements, and patch updates for Phase 1.
- **`v0.2.0` (Phase 2 Baseline)**: Multi-device sync layer and cross-host synchronization.
- **`v0.2.x`**: Protocol enhancements, optimizations, and patch fixes for Phase 2.
- **`v0.Y.0`**: Subsequent major architectural phases and feature milestones increment `Y`.

---

## Quick Installation (Recommended)

The `install.sh` script automatically:
1. **Auto-detects** installed CLI tools (`Claude Code` and `Antigravity CLI`).
2. Builds and installs only the necessary binaries (`memremarkd`, `memremark-mcp`, `memremark-ui`, and detected hooks `memremark-hook-claude`, `memremark-hook-agy`) to `~/.local/bin/`.
3. Sets up and enables the `systemd --user` background service.
4. **Smart patches** CLI hook and MCP configurations:
   - **Antigravity CLI**: Configures `PreInvocation` hook in `~/.gemini/config/hooks.json` and MCP server in `~/.gemini/config/mcp_config.json`.
   - **Claude Code**: Configures `SessionStart` hook in `~/.claude/settings.json` and MCP server in `~/.claude/mcp.json`.
   *(Creates automated `.bak` backups and preserves all existing settings).*

```bash
# Auto-detect installed CLIs and configure automatically:
./install.sh

# Or force install for both Antigravity CLI and Claude Code:
./install.sh --cli=all

# Or install for Antigravity CLI only:
./install.sh --cli=agy

# Or install for Claude Code only:
./install.sh --cli=claude

# Complete uninstall (removes service, binaries, hooks, and MCP servers):
./install.sh --uninstall
```

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
go build -o bin/memremark-hook-claude ./cmd/memremark-hook-claude
go build -o bin/memremark-hook-agy ./cmd/memremark-hook-agy
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
            "command": "/path/to/memremark-hook-claude"
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
        "command": "/path/to/memremark-hook-agy",
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
