# Workspace-Level Compact Summarizer: RAM Control, Pre-Compaction & One-Shot Distillation

- **Author:** Antigravity & User Pair Programming
- **Date:** 2026-09-04
- **Status:** In Review / Specification

---

## 1. Overview & Problem Statement

`memremark` distills developer tool interactions from AI coding assistants into persistent semantic memories (`drawers` table, `type='summary'`, halls: `fact`, `discovery`, `preference`, `advice`).

When running with CLI-based summarizer invokers (specifically `agy` via `AntigravityInvoker` and `claude` via `ClaudeCodeInvoker`), the current architecture suffers from critical performance and resource issues that make the daemon practically unusable under real-world loads:

### 1.1. Root Causes of Failure
1. **Per-Session Granularity:**
   - Debounce and summarization are tracked per `session_id` (`d.Tracker.Touch(obs.SessionID)`).
   - When a user works in a workspace with multiple terminal tabs, subagents, or background tasks, each session triggers an independent summarization flow, multiplying process spawns.
2. **Runaway Batch Loops & Digest Multipliers:**
   - In `daemon_summarize.go`, an unsummarized backlog is split into 100 KB chunks in a loop:
     ```go
     for len(verbatim) > 0 {
         batch := takeBatch(verbatim, maxBatchBytes)
         items, err := summarizer.SummarizeWithOptions(...) // Spawns CLI process
     }
     d.synthesizeSessionDigest(...) // Spawns an additional CLI process
     ```
   - A single active session can spawn 3–6 consecutive CLI executions per idle tick.
3. **Heavy MCP Runtime Overhead per CLI Process:**
   - Invoking `agy -p` initializes the Antigravity runtime along with all registered MCP servers (`gopls`, `php-lsp`, `chrome-devtools`, `claude-mem`, `mysql`, `kubernetes`, etc.).
   - Each MCP server is a separate child process (Node.js, Go, Python) consuming 200 MB – 400 MB RAM. A single `agy` run can spike memory usage by 1.5 GB – 3 GB.
   - When multiple sessions or loops trigger concurrently, the host experiences severe memory pressure, thrashing swap space, or triggering the OS OOM killer.
4. **Linux Kernel Argv Limit (`MAX_ARG_STRLEN`):**
   - Under Linux, the kernel restricts each single argument in `argv` to $32 \times \text{PAGE\_SIZE} = 131,072\text{ bytes}$ (128 KiB) ([execve(2)](https://man7.org/linux/man-pages/man2/execve.2.html)).
   - `agy -p` takes the prompt directly via argv. Any unbounded aggregation over ~128 KB will instantly fail with `E2BIG (Argument list too long)`.
5. **Overly Aggressive Debounce Window:**
   - `idleWindow` is currently hardcoded to 5 seconds (`const idleWindow = 5 * time.Second`).
   - Any 5-second pause between commands immediately triggers a heavy summarization cycle.

---

## 2. Design Goals

- **Workspace-Level Debounce & Batching (Wing Scope):** Group all pending verbatim observations by workspace (`wing_id`) across all sessions. Trigger summarization for the entire workspace as a unified context.
- **One-Shot Distillation:** Guarantee that each workspace idle event triggers at most **one single CLI invocation**, eliminating internal batching loops and standalone digest calls.
- **Observation Pre-Compaction & Hard Budget Cap:** Sanitize and truncate raw tool observation outputs (strip ANSI sequences, limit noisy outputs like file/log dumps to ~500–1,000 characters) and enforce a strict hard cap of **70 KB** on the total prompt payload, comfortably beneath the Linux 128 KB `MAX_ARG_STRLEN` limit.
- **Strict Concurrency Control (Concurrency = 1):** Use a daemon-wide mutex ensuring no two CLI processes ever run simultaneously, capping maximum CLI RAM consumption to that of a single isolated instance.
- **Calibrated Debounce Window:** Increase default `idleWindow` from 5s to **45s** (configurable), requiring a minimum observation threshold ($\ge 3$ observations or timeout) to prevent premature triggers.
- **Clean JSON Array Output:** Model outputs a pure JSON array of `SummaryItem` objects (`[{"hall": "...", "content": "...", "narrative": "..."}]`).
- **Atomic Pruning & Storage:** Record summary items and prune processed verbatim records in an atomic transaction, followed by SQLite incremental vacuum.

---

## 3. Architecture & System Design

```
+-------------------------------------------------------------+
|               Claude Code / Antigravity Events             |
+-------------------------------------------------------------+
                              |
                              v
+-------------------------------------------------------------+
|              InsertVerbatimDrawer(wing_id, ...)              |
+-------------------------------------------------------------+
                              |
                              v
+-------------------------------------------------------------+
|           Workspace Debounce Tracker (wing_id)              |
|           - Idle Window: 45 seconds                         |
|           - Min Observation Threshold: >= 3                 |
+-------------------------------------------------------------+
                              | (When Due)
                              v
+-------------------------------------------------------------+
|          Pre-Compaction & Payload Budgeter                  |
|   1. Query all unsummarized verbatim for wing_id            |
|   2. Truncate noisy tool outputs (500-1000 chars)           |
|   3. Cap total prompt <= 70 KB (Linux argv safe)            |
+-------------------------------------------------------------+
                              |
                              v
+-------------------------------------------------------------+
|          Daemon-wide Mutex (Concurrency = 1)                |
|          Executes: agy -p (or claude -p)                    |
+-------------------------------------------------------------+
                              |
                              v
+-------------------------------------------------------------+
|         Output Parser: Robust JSON Array Decoder            |
|         Extracts []SummaryItem (hall, content, narrative)   |
+-------------------------------------------------------------+
                              |
                              v
+-------------------------------------------------------------+
|                    Storage & Cleanup                        |
|   1. INSERT summary drawers for wing_id                     |
|   2. DELETE processed verbatim drawer IDs                   |
|   3. PRAGMA incremental_vacuum                              |
+-------------------------------------------------------------+
```

---

## 4. Detailed Component Specifications

### 4.1. Debounce Tracker Keyed by Workspace (`wing_id`)
- **Key Type:** Change `Tracker` keys from `sessionID` (string) to `wingID` (stringified `int64`).
- **Idle Window:** Change default from 5s to 45s. Make configurable via `config.json` (`debounce_seconds`).
- **Tracking:**
  - In `Daemon.recordObservation`, resolve `wingID`, store verbatim row, and call:
    `d.Tracker.Touch(strconv.FormatInt(wingID, 10), now)`
  - Retain session-to-invoker mapping or select invoker at the workspace/config level.

### 4.2. Storage Layer Additions (`internal/storage`)
- **`UnsummarizedVerbatimByWing(wingID int64, limit int) ([]Drawer, error)`:**
  Queries pending verbatim rows for a wing across all sessions:
  ```sql
  SELECT id, tool_name, content, session_id, created_at
  FROM drawers
  WHERE wing_id = ? AND type = 'verbatim'
  ORDER BY created_at ASC
  LIMIT ?
  ```
- **`LastSummaryCoversToByWing(wingID int64) (time.Time, bool, error)`:**
  Finds the latest `covers_to` timestamp across all summaries in a wing.

### 4.3. Observation Pre-Compaction (`internal/summarizer`)
Raw tool outputs (such as `git diff`, directory listings, full file view contents) contain massive redundancy.
- **Compaction Rule:**
  - Clean ANSI color codes.
  - If `len(content) > 800`, preserve the first 400 characters and last 400 characters with an omission indicator (`\n... [truncated X bytes] ...\n`).
- **Payload Budget:**
  - `MaxWorkspacePromptBytes = 70_000` (70 KB).
  - Include observations in chronological order until the budget limit is reached.
  - Guarantees `argv` payload remains well below Linux's 128 KiB limit.

### 4.4. Unified Summarization Flow (`internal/daemon/daemon_summarize.go`)
Replace `summarizeSession` and `summarizeSessionWithBatchSize` with `summarizeWing`:

```go
func (d *Daemon) summarizeWing(ctx context.Context, wingID int64, now time.Time) error {
    d.cliMutex.Lock()
    defer d.cliMutex.Unlock()

    verbatim, err := d.Store.UnsummarizedVerbatimByWing(wingID, maxVerbatimQueryLimit)
    if err != nil || len(verbatim) == 0 {
        return err
    }

    // Pre-compact and fit within budget
    batch, obs := compactAndBudget(verbatim, MaxWorkspacePromptBytes)

    // Invoke LLM exactly once
    items, err := summarizer.SummarizeWithOptions(ctx, invoker, obs, d.TargetLanguage, opts)
    if err != nil {
        return err
    }

    coversFrom := batch[0].CreatedAt
    coversTo := batch[len(batch)-1].CreatedAt
    summarySessionID := storage.WingSummarySessionID(wing.Path)

    // Store distilled summaries
    for _, item := range items {
        if err := d.Store.InsertSummaryDrawer(wingID, summarySessionID, item.Hall, item.Content, item.Narrative, coversFrom, coversTo, now); err != nil {
            return err
        }
    }

    // Prune raw verbatim rows
    ids := make([]int64, len(batch))
    for i, v := range batch {
        ids[i] = v.ID
    }
    if err := d.Store.DeleteDrawers(ids); err != nil {
        return err
    }

    return d.Store.IncrementalVacuum()
}
```

### 4.5. Model Prompt & Output Schema
Prompt instructs the model to synthesize observations from the workspace into discrete memory items:

```
Given the following recent tool observations from workspace coding sessions, distill them into concise memory items.
Each item must belong to one of 4 halls:
- fact (settled architectural decisions, conventions, invariants)
- discovery (new findings, root causes, investigation results)
- preference (user habits, workflow choices, preferences)
- advice (actionable recommendations, solutions to pitfalls)

Rules:
1. Output language: Write "content" and "narrative" in <TargetLanguage>.
2. Strict code preservation: ALWAYS keep code identifiers, file paths, tool/command names, and CLI flags exact.
3. Style:
   - "content": Telegraphic 1-line bullet point.
   - "narrative": 2-4 sentence explanation capturing context and rationale.
4. Format: Respond ONLY with a valid JSON array of objects:
   [{"hall":"...","content":"...","narrative":"..."}]
   If nothing is worth memorizing, return [].
```

---

## 5. Resource & Performance Comparison

| Metric | Current Implementation | Proposed Implementation |
| :--- | :--- | :--- |
| **Debounce Scope** | Per `session_id` | Per `wing_id` (Workspace) |
| **Idle Window** | 5 seconds (constant) | 45 seconds (calibrated) |
| **CLI Invocations per Cycle** | $O(\text{sessions} \times \text{batches}) + \text{Digests}$ (3–10+) | **Exactly 1 invocation** |
| **Concurrent CLI Processes** | Up to 2 (`maxSessionsPerTick`) | **Capped at 1** (Global Mutex) |
| **Prompt Payload Size** | Unbounded raw output (up to 100 KB chunks) | **Pre-compacted $\le$ 70 KB** |
| **Linux `MAX_ARG_STRLEN` Risk** | High (risk of `E2BIG`) | **Zero** (Safe margin $\approx 58\text{ KB}$) |
| **Peak RAM Impact** | 2 GB – 5 GB+ (overlapping MCP runtimes) | **Single CLI process footprint** |

---

## 6. Error Handling & Edge Cases

1. **CLI Failure / Non-Zero Exit:**
   - Verbatim records are NOT pruned.
   - The workspace remains dirty in the tracker with exponential backoff to avoid immediate retry loops.
2. **Empty Array Reply (`[]`):**
   - Valid response indicating trivial activity (e.g., directory listings with no meaningful architectural findings).
   - Verbatim batch is still pruned because it has been reviewed and deemed non-memorable.
3. **Invalid JSON / Parsing Error:**
   - Verbatim records are preserved.
   - Error is logged; retry is deferred to the next scheduled cycle.
4. **Daemon Shutdown (SIGINT/SIGTERM):**
   - Active CLI invocation receives context cancellation via `exec.CommandContext`.
   - Mutex ensures graceful termination without leaving dangling child processes.

---

## 7. Verification & Testing Plan

1. **Unit Tests:**
   - `internal/summarizer`: Test observation pre-compaction logic, verifying truncation and payload budget boundaries.
   - `internal/storage`: Test `UnsummarizedVerbatimByWing` and multi-session verbatim retrieval.
   - `internal/debounce`: Verify workspace-keyed debounce tracking.
2. **Integration Tests (`internal/daemon`):**
   - Verify that multiple sessions recording verbatim in the same workspace result in a single unified summarization call.
   - Verify that raw verbatim rows are pruned and summary drawers are created.
   - Verify concurrency lock prevents concurrent CLI executions.
3. **End-to-End CLI Verification:**
   - Run `memremarkd` in a live workspace with `agy` configured.
   - Monitor process tree (`ps aux | grep -E "(agy|node|gopls)"`) to verify no process storming occurs and RAM remains stable.
