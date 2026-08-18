# Design Specification: Daemon I/O & Memory Optimization and Configurable Low-Cost Summarizer Models

**Date:** 2026-08-18  
**Status:** Draft / Pending Review  
**Target Components:** `internal/config`, `internal/summarizer`, `internal/adapter/claudecode`, `internal/adapter/antigravity`, `internal/daemon`, `cmd/memremarkd`

---

## 1. Context & Problem Statement

MemRemark currently operates as a background daemon (`memremarkd`) that periodically polls transcripts from Claude Code and Antigravity CLI, normalizes events into observations, and triggers headless sub-conversations (`claude -p` / `agy -p`) to distill raw observations into structured knowledge drawers.

Two critical performance and cost issues have been identified in production:

1. **High Disk I/O, CPU Churn, and Page Cache Bloat (~523MB–1.7GB peak):**
   - Every 3 seconds, `pollClaudeCode` traverses `~/.claude/projects/` with `filepath.WalkDir` and calls `os.Open` on **every** `.jsonl` transcript file to seek and check for EOF.
   - Every 3 seconds, `pollAntigravity` opens `conversation_summaries.db` and opens every conversation SQLite DB (`conversations/<id>.db`) to run `SELECT ... FROM steps WHERE idx > ?`.
   - Every 3 seconds, `SetPollState` executes `INSERT OR REPLACE INTO poll_state` in SQLite for every file and conversation, even when 0 new lines or rows were produced.
   - Subprocesses (`claude -p` / `agy -p`) run full environments without lightweight flags, loading unnecessary plugins, tools, and heavy MCP servers, pushing peak memory to 1.7GB+.

2. **Uncontrolled & Expensive Model Usage During Summarization:**
   - Subprocesses are spawned without the `--model` flag, falling back to the user's default interactive model (e.g. Claude 3.7 Sonnet / Opus / Gemini Pro).
   - Summarizing raw observations into JSON is a basic extraction task. Using top-tier reasoning models leads to unnecessary cost, slow inference latency, and rapid quota exhaustion (429 Rate Limits).
   - Model names cannot be hardcoded because CLI models evolve, get deprecated, or need custom override per user environment.

---

## 2. Goals & Non-Goals

### Goals
- **Per-File Dirty Checking & Zero-Churn Polling:** Eliminate redundant `os.Open`, DB queries, and SQLite `SetPollState` writes when files/databases have not changed.
- **Robust Subfolder Traversal:** Ensure new transcripts in deeply nested subdirectories (`~/.claude/projects/<slug>/...`) and truncated/rotated files are detected reliably.
- **Headless Subprocess Hardening:** Supply `--safe-mode`, `--tools ""` (Claude) and `--disable-slash-commands`, `--effort low` (Antigravity) to reduce subprocess memory footprint and startup overhead.
- **Hierarchical Configuration System:** Provide `~/.memremark/config.json` with fallback defaults (`haiku` for Claude, `gemini-3.7-flash-low` for Antigravity) and environment variable overrides (`MEMREMARK_*`).

### Non-Goals
- Introducing external file watcher dependencies (e.g., `fsnotify`). We stick strictly to the Go standard library (`os.Stat`, `FileInfo.ModTime`, `FileInfo.Size`) for zero-dependency portability and reliability.
- Changing the storage schema or observation normalization pipeline.

---

## 3. Architecture & Detailed Design

### 3.1. Package `internal/config`

A new lightweight package `internal/config` manages runtime configuration loading.

#### Configuration Schema (`~/.memremark/config.json`):
```json
{
  "summarizer": {
    "claude_model": "haiku",
    "antigravity_model": "gemini-3.7-flash-low",
    "antigravity_effort": "low"
  }
}
```

#### Go Structs:
```go
package config

type SummarizerConfig struct {
    ClaudeModel       string `json:"claude_model"`
    AntigravityModel  string `json:"antigravity_model"`
    AntigravityEffort string `json:"antigravity_effort"`
}

type Config struct {
    Summarizer SummarizerConfig `json:"summarizer"`
}
```

#### Precedence Hierarchy:
1. **Environment Variables:**
   - `MEMREMARK_CLAUDE_MODEL` -> overrides `Summarizer.ClaudeModel`
   - `MEMREMARK_ANTIGRAVITY_MODEL` -> overrides `Summarizer.AntigravityModel`
   - `MEMREMARK_ANTIGRAVITY_EFFORT` -> overrides `Summarizer.AntigravityEffort`
2. **File Configuration:** `~/.memremark/config.json` (if present and valid).
3. **Built-in Defaults:**
   - `ClaudeModel`: `"haiku"`
   - `AntigravityModel`: `"gemini-3.7-flash-low"`
   - `AntigravityEffort`: `"low"`
4. **Passthrough Keyword (`"default"` or `""`):** If explicitly configured as `"default"` or empty, the `--model` / `--effort` flags are omitted, letting the respective CLI use its own configured default.

---

### 3.2. Subprocess Hardening & Invoker Updates (`internal/summarizer`)

`ClaudeCodeInvoker` and `AntigravityInvoker` are updated to accept configuration parameters and append lightweight execution flags.

#### `ClaudeCodeInvoker`:
```go
type ClaudeCodeInvoker struct {
    Model string // e.g. "haiku"
}

func (inv ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
    args := []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""}
    if inv.Model != "" && inv.Model != "default" {
        args = append(args, "--model", inv.Model)
    }
    cmd := exec.CommandContext(ctx, "claude", args...)
    cmd.Stdin = strings.NewReader(prompt)
    ...
}
```

#### `AntigravityInvoker`:
```go
type AntigravityInvoker struct {
    Model  string // e.g. "gemini-3.7-flash-low"
    Effort string // e.g. "low"
}

func (inv AntigravityInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
    args := []string{"-p", prompt, "--output-format", "json", "--disable-slash-commands"}
    if inv.Model != "" && inv.Model != "default" {
        args = append(args, "--model", inv.Model)
    }
    if inv.Effort != "" && inv.Effort != "default" {
        args = append(args, "--effort", inv.Effort)
    }
    cmd := exec.CommandContext(ctx, "agy", args...)
    ...
}
```

---

### 3.3. File State Tracking & Dirty Checking (`internal/adapter/claudecode` & `Tailer`)

To prevent I/O and DB churn, `Tailer` tracks metadata per file:

```go
type fileMeta struct {
    modTime time.Time
    size    int64
    offset  int64
}

type Tailer struct {
    files map[string]*fileMeta
}
```

#### Read & Poll Logic:
1. `DiscoverTranscriptFiles` continues to use `filepath.WalkDir` so all nested subfolders are discovered without relying on parent directory `mtime`.
2. When evaluating a file:
   - Call `os.Stat(path)` (or `DirEntry.Info()`).
   - If `meta, exists := t.files[path]` exists:
     - **Unchanged check:** If `info.ModTime().Equal(meta.modTime) && info.Size() == meta.size`, **skip immediately** (no `os.Open`, no read, no DB write).
     - **Truncation check:** If `info.Size() < meta.offset`, reset `meta.offset = 0` to re-read from the beginning.
   - If new or modified:
     - Open file, seek to `meta.offset`, read newly appended complete lines.
     - Update `meta.modTime = info.ModTime()`, `meta.size = info.Size()`, `meta.offset = newConsumedOffset`.
3. **Dirty-check in Daemon:**
   - Only call `d.Store.SetPollState(key, offset)` when `newLinesRead > 0` or offset changed.

---

### 3.4. Antigravity Adapter Optimization (`internal/adapter/antigravity` & `daemon`)

1. **Summaries DB Stat Check:**
   - In `pollAntigravity`, check `os.Stat(d.antigravitySummariesDB)`.
   - If `info.ModTime().Equal(d.antigravitySummariesModTime)` and size is unchanged, skip querying `conversation_summaries.db`.
2. **Per-Conversation Stat & MaxIdx Check:**
   - For individual `conversations/<id>.db`, check `os.Stat` before `openReadOnly`.
   - Only query `steps WHERE idx > sinceIdx` if the DB file was modified or unseen.
   - Only execute `d.Store.SetPollState(antigravityConvKey(conv.ID), maxIdx)` when `maxIdx > sinceIdx`.

---

## 4. Edge Cases & Safety

1. **Deeply Nested Subdirectories:** `filepath.WalkDir` naturally handles arbitrary subdirectory depth (e.g. `~/.claude/projects/org-repo-sub/...`). Per-file dirty checking guarantees nested new files are caught on the next tick.
2. **File Truncation / Rewrites:** If a transcript is truncated or rewritten (size < offset), offset resets to 0 cleanly.
3. **Corrupt or Missing Config File:** If `~/.memremark/config.json` is missing or contains invalid JSON, the daemon logs a warning and falls back to safe built-in defaults (`haiku` and `gemini-3.7-flash-low`).
4. **Fallback Invoker Resilience:** `FallbackInvoker` seamlessly delegates to the secondary CLI if the primary CLI fails (e.g. invalid model error, CLI binary not found, or temporary network issue).

---

## 5. Verification & Testing Plan

1. **`internal/config` Unit Tests:**
   - `TestLoad_Defaults`: Non-existent config file returns defaults.
   - `TestLoad_CustomJSON`: Custom JSON file parses correctly.
   - `TestLoad_EnvOverrides`: Environment variables override JSON and defaults.
   - `TestLoad_DefaultKeyword`: Handling of `"default"` and empty string.
2. **`internal/summarizer` Unit Tests:**
   - Verify argument building for `ClaudeCodeInvoker` (includes `--model`, `--safe-mode`, `--tools ""`).
   - Verify argument building for `AntigravityInvoker` (includes `--model`, `--effort`, `--disable-slash-commands`).
3. **`internal/adapter/claudecode` & `daemon` Unit Tests:**
   - Verify `Tailer` skips opening files when `ModTime` and `Size` are unchanged.
   - Verify `Tailer` resets offset when file is truncated.
   - Verify `Daemon` does not call `SetPollState` when no new lines/steps are detected.
4. **End-to-End Verification:**
   - Run `go test ./...` across all packages.
   - Run `go build -o /tmp/memremarkd ./cmd/memremarkd` to verify binary compilation.
   - Verify systemd service startup and check that idle memory and I/O remain negligible.
