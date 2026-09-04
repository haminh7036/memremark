# Workspace-Level Compact Summarizer: RAM Control, Pre-Compaction, Emergency Stop & Token Budgeting

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
3. **Heavy MCP Runtime Overhead & Orphaned Child Processes:**
   - Invoking `agy -p` initializes the Antigravity runtime along with all registered MCP servers (`gopls`, `php-lsp`, `chrome-devtools`, `claude-mem`, `mysql`, `kubernetes`, etc.).
   - Each MCP server is a separate child process (Node.js, Go, Python) consuming 200 MB – 400 MB RAM. A single `agy` run can spike memory usage by 1.5 GB – 3 GB.
   - Without process group isolation (`Setpgid: true`), killing `agy` leaves its child MCP servers running as orphaned processes, reparented to init/systemd.
4. **Lack of Emergency Stop / Circuit Breaker:**
   - There is currently no internal mechanism to instantly abort in-flight summarization processes or pause the daemon without invoking `systemctl --user stop memremarkd`.
   - Because `memremarkd.service` sets `Restart=always`, manual `kill` or `pkill` simply causes systemd to revive the daemon 5 seconds later.
5. **Linux Kernel Argv Limit (`MAX_ARG_STRLEN`):**
   - Under Linux, the kernel restricts each single argument in `argv` to $32 \times \text{PAGE\_SIZE} = 131,072\text{ bytes}$ (128 KiB) ([execve(2)](https://man7.org/linux/man-pages/man2/execve.2.html)).
   - `agy -p` takes the prompt directly via argv. Any unbounded aggregation over ~128 KB will instantly fail with `E2BIG (Argument list too long)`.
6. **Uncontrolled Token Consumption:**
   - There is no quota or daily token limit per provider/model. Runaway loops or large sessions can exhaust API rate limits or CLI quotas without warning.
7. **Overly Aggressive Debounce Window:**
   - `idleWindow` is currently hardcoded to 5 seconds (`const idleWindow = 5 * time.Second`).
   - Any 5-second pause between commands immediately triggers a heavy summarization cycle.

---

## 2. Design Goals

- **Workspace-Level Debounce & Batching (Wing Scope):** Group all pending verbatim observations by workspace (`wing_id`) across all sessions. Trigger summarization for the entire workspace as a unified context.
- **One-Shot Distillation:** Guarantee that each workspace idle event triggers at most **one single CLI invocation**, eliminating internal batching loops and standalone digest calls.
- **Observation Pre-Compaction & Hard Budget Cap:** Sanitize and truncate raw tool observation outputs (strip ANSI sequences, limit noisy outputs like file/log dumps to ~500–1,000 characters) and enforce a strict hard cap of **70 KB** on the total prompt payload, comfortably beneath the Linux 128 KB `MAX_ARG_STRLEN` limit.
- **Strict Concurrency Control (Concurrency = 1):** Use a daemon-wide mutex ensuring no two CLI processes ever run simultaneously, capping maximum CLI RAM consumption to that of a single isolated instance.
- **Process Group Isolation & Clean Termination:** Run CLI processes in isolated process groups (`Setpgid: true`) and kill the entire process group (`-pgid`) on timeout or cancel, preventing zombie/orphaned MCP server processes.
- **Emergency Stop & Pause Control:** Provide instant pause/resume capability (`memremark pause` / `memremark resume` via `~/.memremark/paused` flag) that immediately terminates running child processes and halts daemon polling without requiring `systemctl`.
- **Daily Token / Usage Budget & Circuit Breaker:** Track token consumption per provider/model in SQLite. Trip a hard circuit breaker when the daily quota is reached, blocking further invocations until midnight while preserving verbatim records.
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
|     Gate 1: Emergency Pause Check (~/.memremark/paused)     |
|     Gate 2: Daily Token Budget Check (Circuit Breaker)      |
+-------------------------------------------------------------+
                              | (Allowed)
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
|       Daemon Mutex (Concurrency = 1) + Setpgid: true        |
|       Executes: agy -p (or claude -p) in new Process Group  |
|       On Cancel/Timeout/Pause: Kill(-pgid, SIGKILL)         |
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
|              Storage, Token Accounting & Cleanup            |
|   1. INSERT summary drawers for wing_id                     |
|   2. Record token usage to token_usage table                |
|   3. DELETE processed verbatim drawer IDs                   |
|   4. PRAGMA incremental_vacuum                              |
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
- **`token_usage` Table:**
  ```sql
  CREATE TABLE IF NOT EXISTS token_usage (
      date TEXT NOT NULL,
      provider TEXT NOT NULL,
      model TEXT NOT NULL,
      prompt_tokens INTEGER NOT NULL DEFAULT 0,
      completion_tokens INTEGER NOT NULL DEFAULT 0,
      total_tokens INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (date, provider, model)
  );
  ```

### 4.3. Observation Pre-Compaction (`internal/summarizer`)
Raw tool outputs contain massive redundancy.
- **Compaction Rule:**
  - Clean ANSI color codes.
  - If `len(content) > 800`, preserve the first 400 characters and last 400 characters with an omission indicator (`\n... [truncated X bytes] ...\n`).
- **Payload Budget:**
  - `MaxWorkspacePromptBytes = 70_000` (70 KB).
  - Include observations in chronological order until the budget limit is reached.
  - Guarantees `argv` payload remains well below Linux's 128 KiB limit.

### 4.4. Process Group Isolation & Clean Subprocess Termination
To completely eliminate orphaned MCP server child processes:
```go
cmd := exec.CommandContext(ctx, binary, args...)
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

// Ensure process group termination on cancellation
go func() {
    <-ctx.Done()
    if cmd.Process != nil && cmd.Process.Pid > 0 {
        // Send SIGKILL to the entire process group
        _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
    }
}()
```

### 4.5. Emergency Stop & Pause Control (`memremark pause` / `memremark resume`)
1. **Pause Detection:**
   - Daemon periodically checks for the existence of `$HOME/.memremark/paused`.
2. **Behavior on Pause:**
   - If a summarizer process is actively running, its `context.CancelFunc` is triggered immediately, killing the entire process group (`-pgid`) via `SIGKILL`.
   - The daemon enters a suspended state: polling of transcripts and execution of summarizer calls are paused.
   - Status endpoint or CLI returns `STATUS: PAUSED`.
3. **Behavior on Resume:**
   - When `$HOME/.memremark/paused` is removed (`memremark resume`), daemon logs resume event and resumes scheduled polling.

### 4.6. Daily Token Budget & Provider/Model Circuit Breaker
1. **Configuration (`config.json`):**
   ```json
   {
     "summarizer": {
       "daily_token_limits": {
         "antigravity/gemini-3.7-flash-low": 200000,
         "gemini/gemini-2.5-flash": 500000,
         "claude/haiku": 100000
       }
     }
   }
   ```
2. **Token Accounting:**
   - **Direct API:** Read exact prompt/candidate tokens from API response (`usageMetadata` for Gemini, `usage` for Anthropic).
   - **CLI (`agy` / `claude`):** Read token usage from JSON output if available; otherwise calculate deterministic estimation:
     $$\text{Estimated Tokens} = \lceil (\text{len(prompt)} + \text{len(response)}) / 3.5 \rceil$$
   - Increment `token_usage` table in SQLite for the current date (`YYYY-MM-DD`).
3. **Circuit Breaker Check:**
   - Prior to calling any CLI or API, query total tokens used today for that provider/model.
   - If `current_usage + estimated_prompt >= limit`:
     - Trip circuit breaker.
     - Log warning: `daemon: daily token limit reached for %s/%s (%d/%d). Skipping summarization.`.
     - Skip invocation without error. Verbatim drawers remain intact for processing after the quota resets at midnight.

### 4.7. Unified Summarization Flow (`internal/daemon/daemon_summarize.go`)
Replace `summarizeSession` and `summarizeSessionWithBatchSize` with `summarizeWing`:

```go
func (d *Daemon) summarizeWing(ctx context.Context, wingID int64, now time.Time) error {
    if d.isPaused() {
        return nil
    }

    provider, model := d.resolveProviderAndModel(wingID)
    if d.isDailyBudgetExceeded(provider, model) {
        log.Printf("daemon: daily budget exceeded for %s/%s, deferring", provider, model)
        return nil
    }

    d.cliMutex.Lock()
    defer d.cliMutex.Unlock()

    verbatim, err := d.Store.UnsummarizedVerbatimByWing(wingID, maxVerbatimQueryLimit)
    if err != nil || len(verbatim) == 0 {
        return err
    }

    // Pre-compact and fit within budget
    batch, obs := compactAndBudget(verbatim, MaxWorkspacePromptBytes)

    // Invoke LLM exactly once
    items, usage, err := summarizer.SummarizeWorkspaceWithOptions(ctx, invoker, obs, d.TargetLanguage, opts)
    if err != nil {
        return err
    }

    // Record token usage
    _ = d.Store.RecordTokenUsage(now.Format("2006-01-02"), provider, model, usage)

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

---

## 5. Resource & Performance Comparison

| Metric | Current Implementation | Proposed Implementation |
| :--- | :--- | :--- |
| **Debounce Scope** | Per `session_id` | Per `wing_id` (Workspace) |
| **Idle Window** | 5 seconds (constant) | 45 seconds (calibrated) |
| **CLI Invocations per Cycle** | $O(\text{sessions} \times \text{batches}) + \text{Digests}$ (3–10+) | **Exactly 1 invocation** |
| **Concurrent CLI Processes** | Up to 2 (`maxSessionsPerTick`) | **Capped at 1** (Global Mutex) |
| **Process Group Clean-up** | None (`kill` leaves MCP zombies) | **`Setpgid: true` + `-pgid` kill** |
| **Emergency Stop** | None (only `systemctl stop`) | **`memremark pause` & instant group kill** |
| **Quota Protection** | None (unbounded spend/tokens) | **Daily Token Budget per Provider/Model** |
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
4. **Daily Token Quota Trip:**
   - When quota is reached, no calls are made.
   - Verbatim rows remain in SQLite, ensuring zero memory loss. Once quota resets at midnight, summarization resumes.
5. **Emergency Pause Invocation:**
   - In-flight execution is abruptly terminated via group SIGKILL.
   - Verbatim records remain intact for the next resume.

---

## 7. Verification & Testing Plan

1. **Unit Tests:**
   - `internal/summarizer`: Test observation pre-compaction logic and token estimation algorithms.
   - `internal/storage`: Test `token_usage` table upsert and quota querying logic.
   - `internal/storage`: Test `UnsummarizedVerbatimByWing` and multi-session verbatim retrieval.
   - `internal/debounce`: Verify workspace-keyed debounce tracking.
2. **Integration Tests (`internal/daemon`):**
   - Verify that multiple sessions recording verbatim in the same workspace result in a single unified summarization call.
   - Verify that triggering emergency pause stops the running subprocess group cleanly.
   - Verify that exceeding the daily token quota prevents further CLI/API calls and preserves verbatim drawers.
3. **End-to-End CLI Verification:**
   - Run `memremarkd` in a live workspace with `agy` configured.
   - Verify process tree (`ps aux | grep -E "(agy|node|gopls)"`) during normal operation and upon emergency stop to confirm zero orphaned processes.
