# Core Engine — Design Spec

Status: Approved for planning
Date: 2026-08-10
Scope: memremark Core Engine (single machine). Sync Layer across devices is a separate, later spec.

## 1. Problem & Goal

The user works across 3 devices (PC1 at the office, PC2 at home, a laptop at home), each running Claude Code and/or Antigravity CLI. Every new session currently starts with zero context — prior discussion, decisions, and project knowledge have to be re-explained by hand.

The Core Engine automatically captures what happens during a CLI session, stores it durably, distills it into reusable knowledge, and re-injects that knowledge into future sessions on the **same machine** — without the user doing anything manual. Cross-device propagation of that knowledge is out of scope here; it is the Sync Layer's job (next spec), built once the Core Engine has something worth syncing.

## 2. Reference prior art

Two existing tools were inspected directly (source read locally / via GitHub) to ground design decisions instead of guessing:

- **claude-mem** (`thedotmack/claude-mem`, plugin source available locally): long-running worker daemon started from `SessionStart`; `PostToolUse` logs raw observations; `Stop` triggers a debounced LLM summarization pass via `@anthropic-ai/claude-agent-sdk`; memory is scoped per project via a `project_id`/`projectPath` column in a single `~/.claude-mem/claude-mem.db`. Critically, hooks are not its only data source: the source also contains a `transcriptWatcher`/`transcriptMirrorBatcher` that reads Claude Code's own on-disk transcript files (`~/.claude/projects/*.jsonl`) directly. Hooks act as a low-latency "something happened" signal; the transcript file is the actual source of truth it reads from.
- **Mempalace** (`MemPalace/mempalace`, README + docs fetched): local-first, **verbatim** storage (no summarization) indexed with embeddings (ChromaDB by default); organizes memory into **wings** (a person, project, or topic — top-level), **rooms** (topics within a wing, auto-detected from folder structure), **halls** (memory-type categories: facts, events, discoveries, preferences, advice), and **tunnels** (cross-wing links when two wings share a room name — used for multi-person setups). Its primary capture command, `mempalace mine <transcript-dir>`, likewise reads transcript files directly rather than depending solely on hooks. Auto-save hooks exist for Claude Code, Codex CLI, and Cursor; a separate official plugin adds `Stop`/`PreInvocation` hooks for **Antigravity IDE**.
- **Antigravity CLI** (`google-antigravity/antigravity-cli`): investigated empirically, not just from docs — see §2.1. Confirmed by direct testing on the installed binary (v1.1.7, then v1.1.11): it has its own `hooks.json` (global `~/.gemini/config/hooks.json` or workspace-local `<workspace>/.agents/hooks.json`) and a working headless print mode (`agy -p "..." --output-format json`). However, `PostToolUse`/`Stop` **command hooks load successfully but do not execute** in this environment, in both headless and interactive sessions. Antigravity CLI does persist full conversation transcripts on disk regardless, in per-conversation SQLite databases under `~/.gemini/antigravity-cli/conversations/*.db` (a `steps` table) with an index at `~/.gemini/antigravity-cli/conversation_summaries.db` mapping `conversation_id` to `workspace_uris`.

memremark's Core Engine is a deliberate combination of these: verbatim storage (Mempalace) for zero information loss, plus periodic LLM-driven summarization (claude-mem) to produce the "distilled knowledge" the project's goal calls for, organized with Mempalace's wing/hall vocabulary but trimmed to what a single-user, multi-CLI setup actually needs. Following the precedent both reference tools set — and confirmed necessary by the Antigravity CLI hook investigation below — **verbatim capture reads each CLI's own on-disk transcript directly; hooks are used only as an optional low-latency trigger, never as the sole data path.**

### 2.1 Antigravity CLI hook investigation (empirical findings, 2026-08-10)

Direct testing against the installed `agy` binary, not just its CHANGELOG:

1. **Schema.** The CHANGELOG's own wording and Claude Code's hooks.json convention both suggested a Claude-Code-shaped file (`{"hooks": {"PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "..."}]}]}}`). This is wrong for Antigravity CLI and fails to parse (confirmed via `~/.gemini/antigravity-cli/cli.log`: `invalid hook "hooks": command hook must specify 'command'`). Every plugin currently installed on the test machine that was ported from Claude Code (superpowers, context-mode, hookify, ralph-loop, security-guidance, claude-security, learning-output-style, explanatory-output-style) fails to parse under Antigravity CLI for the same reason — an existing, unrelated breakage worth knowing about but out of scope to fix here. The correct shape, found by iterating against the real parser error messages: each hook type maps directly to a single object (not an array, no `"hooks"` wrapper):
   ```json
   {
     "PostToolUse": { "matcher": "*", "command": "..." },
     "Stop": { "command": "..." }
   }
   ```
   This loads cleanly (`cli.log`: `loaded 2 named hooks from 1 hooks.json file(s)`).
2. **Execution.** Even with the corrected schema, a trusted workspace, and genuine tool calls (a real file write, confirmed on disk), neither `PostToolUse` nor `Stop` ever executed the configured command — no output file, no error, no log trace of execution. Retested after the user manually upgraded `agy` from 1.1.7 to 1.1.11 (the version whose CHANGELOG documents Stop-hook execution-order fixes): same result. Retested in a real interactive session (not just headless `-p`): same result. This is treated as a confirmed, reproducible limitation of command-type hooks in this environment, not an unverified risk.
3. **Working alternative.** `agy` persists everything needed regardless: per-conversation SQLite databases at `~/.gemini/antigravity-cli/conversations/*.db` (WAL mode — `.db-wal`/`.db-shm` present) with a `steps` table, and an index database `conversation_summaries.db` whose `conversation_summaries` table maps `conversation_id` → `workspace_uris`. This is what the Antigravity adapter reads directly for verbatim capture (§5, §6).

## 3. Non-goals (explicitly out of scope for this spec)

- Syncing memory across devices (Sync Layer — separate spec).
- Semantic/vector search over verbatim content. v1 context injection is recency-based only (most recent summaries), not similarity search.
- `room` auto-detection logic. The column exists in the schema so it can be added later without a migration, but no detection logic is built now.
- `tunnels` (cross-wing linking). This solves multi-person palace-sharing; the user is a single person across their own devices, so there is nothing to link.
- Any CLI other than Claude Code and Antigravity CLI.

## 4. Architecture

A single long-running daemon process (`memremarkd`) runs per machine, started idempotently. Its primary input is **each CLI's own on-disk transcript store**, which it tails/polls directly. Hooks, where they load and actually execute, are used only to wake the daemon immediately instead of waiting for the next poll tick — never as the only way an observation reaches the daemon. A thin per-CLI adapter still exists, but its job shifts from "parse a hook payload" to "know where that CLI's transcripts live and how to read them."

Storage is one SQLite database per machine at `~/.memremark/memremark.db`. A single file, not one per project — matches both reference tools' actual behavior (they scope logically via columns, not physically via separate files), and keeps the Sync Layer's future job simpler (sync one file, not an unbounded set).

```
CLI (Claude Code / Antigravity CLI)
   |  writes its own transcript as it works
   v
CLI's own on-disk store
   (Claude Code:    ~/.claude/projects/<project>/*.jsonl)
   (Antigravity CLI: ~/.gemini/antigravity-cli/conversations/*.db + conversation_summaries.db)
   |
   |  read-only tail/poll (+ optional hook nudge, best-effort)
   v
Adapter (per-CLI: knows the transcript format/location, normalizes to Observation)
   v
memremarkd (long-running daemon)
   |
   v
SQLite (~/.memremark/memremark.db)
```

## 5. Adapters

Each CLI gets an adapter implementing three responsibilities:

1. **Transcript reading (primary, required)** — read that CLI's own on-disk transcript store read-only, and normalize each step into a common internal `Observation` struct (tool name, args/result summary, session id, timestamp, cwd). This does not depend on hooks and works even if hooks never fire.
2. **Hook listening (optional, best-effort)** — if the CLI's hooks actually execute in the user's environment, use them purely to trigger an immediate re-read instead of waiting for the next poll interval. Never the sole trigger.
3. **Headless invocation** — shell out to that CLI's own non-interactive mode to run the summarization sub-conversation, so memremark never needs its own LLM API key or billing. Confirmed working for both CLIs (§2.1).

| Capability | Claude Code | Antigravity CLI |
|---|---|---|
| Transcript store | `~/.claude/projects/<project>/*.jsonl` (append-only, one line per event) | `~/.gemini/antigravity-cli/conversations/<conversation_id>.db` (SQLite, WAL mode, `steps` table) + `conversation_summaries.db` (`conversation_id` → `workspace_uris`) |
| Hook config (best-effort) | plugin `hooks.json` | `hooks.json` (global or workspace-local); schema confirmed in §2.1, but `PostToolUse`/`Stop` execution confirmed non-functional as of v1.1.11 in this environment — treat as unavailable until re-verified |
| Headless invoke | `claude -p "..." --output-format json` | `agy -p "..." --output-format json` |
| Session-start injection point | `SessionStart` hook | No working hook to rely on (§2.1); poll `conversation_summaries.db` for a new `conversation_id` under the current workspace as the signal that a session started |

Reading Antigravity CLI's live `.db` files must be strictly **read-only** (never write, migrate, or vacuum them) with a busy-timeout/retry for the rare case the writer holds a lock — see §8.

## 6. Data flow

1. **Verbatim capture** — the daemon polls (and, if a hook fires, immediately re-checks) each known project's transcript source. New steps/events are normalized into `Observation`s and written straight to SQLite as `drawer` rows (`type = 'verbatim'`, `hall = 'event'`). No LLM call. This is the source of truth and cannot lose information, and does not depend on any hook executing.
2. **Summarization** — after a turn ends (detected either from a `Stop` hook when available, or from the transcript itself going idle for a few seconds — the same debounce idea either way), the daemon spawns a headless sub-conversation through that session's own CLI, asking it to distill the verbatim drawers accumulated since the last summary into one or more summary `drawer` rows (`type = 'summary'`), each classified into a `hall` (`fact`, `discovery`, `preference`, or `advice`).
3. **Context injection** — at the start of a new session (detected via `SessionStart` for Claude Code, or via a new `conversation_id` appearing for the current workspace in Antigravity CLI's `conversation_summaries.db`), the daemon looks up the current working directory's `wing`, queries its most recent summary drawers ordered by time, and injects them into the new session's context.

## 7. Schema (SQLite)

```sql
wings (
  id INTEGER PRIMARY KEY,
  path TEXT UNIQUE NOT NULL,   -- project directory; auto-created on first observation from a new path
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
)

drawers (
  id INTEGER PRIMARY KEY,
  wing_id INTEGER NOT NULL REFERENCES wings(id),
  room TEXT,                   -- nullable; unused in v1, reserved for future topic sub-grouping
  type TEXT NOT NULL CHECK (type IN ('verbatim', 'summary')),
  hall TEXT NOT NULL CHECK (hall IN ('event', 'fact', 'discovery', 'preference', 'advice')),
  content TEXT NOT NULL,
  tool_name TEXT,               -- set only when type = 'verbatim'
  session_id TEXT NOT NULL,
  covers_from INTEGER,          -- set only when type = 'summary': time range of verbatim drawers it distills
  covers_to INTEGER,
  created_at INTEGER NOT NULL
)
```

No `tunnels` table (see Non-goals).

## 8. Error handling

- **Reading a CLI's live transcript file/DB**: always read-only, never write/migrate/vacuum the CLI's own files. For Antigravity CLI's SQLite databases specifically, open with a busy-timeout and retry-on-`SQLITE_BUSY`, since they are in WAL mode and can, rarely, be mid-write by `agy` itself; this is a supported concurrent-read pattern, not a race condition to work around structurally.
- **Poll tick finds nothing new / transcript temporarily unreadable** (file mid-write, momentary lock): skip and retry on the next tick. No permanent data loss — the daemon has no per-event state to lose, it just re-scans from its last-read watermark.
- **A hook fires but the daemon is unreachable**: harmless by design, since hooks are only an optional nudge to poll sooner; the next scheduled poll tick catches the same data regardless.
- **Headless summarization call fails** (auth expired, network down, rate limited): verbatim drawers are already durably stored regardless, so nothing is lost. The daemon retries summarization on the next debounce window.
- **Daemon crash**: restarted idempotently the next time anything triggers it (a hook, if working, or a lightweight launcher check on CLI startup — see Open risks on the injection path).

## 9. Testing

Every non-trivial piece of logic below gets a unit test as part of implementation — this is not optional:

- Unit tests for SQLite schema operations (insert/query drawers and wings) against an in-memory or temp-file SQLite DB.
- Unit tests for each adapter's transcript parser: feed it a real captured transcript fragment (a real `.jsonl` excerpt for Claude Code; a real exported `steps` table snippet for Antigravity CLI) and assert the resulting `Observation` structs are correct — including edge cases like a truncated/mid-write file and an empty transcript.
- Unit tests for the debounce/idle-detection logic that decides when to trigger summarization, independent of any real CLI.
- One end-to-end smoke test: seed a fake transcript file/DB, run the daemon's poll cycle, assert a verbatim drawer lands correctly; then, with a stubbed headless invoker standing in for the real CLI sub-conversation, assert a summary drawer lands correctly too.

## 10. Open risks

- **Antigravity CLI's `conversations/*.db` `steps` schema is undocumented** (an internal implementation detail, not a public API) and was only spot-checked (table list, not full column-level schema) during this investigation. It needs full reverse-engineering during implementation, and — because it's undocumented — may change without notice across `agy` versions; the adapter's parser should fail loudly (not silently drop data) if the schema shape it expects doesn't match.
- **The context-injection path for Antigravity CLI is unverified.** §2.1 confirmed `PostToolUse`/`Stop` command hooks load but don't execute; it did not test whether a hook-based injection mechanism (the equivalent of Claude Code's `SessionStart`) fares any better. Given the same underlying hook-execution engine is involved, it may not. §5/§6 already designed around this by not requiring a working hook — polling `conversation_summaries.db` for a new `conversation_id` is the fallback — but *how the daemon actually delivers text into a new Antigravity CLI session's context* (there is no confirmed equivalent of Claude Code's `SessionStart` hook output-injection mechanism) is unresolved and needs hands-on investigation before implementation can finish this adapter.
- Debounce window length (how many seconds of inactivity before summarizing) is not yet tuned; start with claude-mem's approach as a reference point and adjust based on real usage.
