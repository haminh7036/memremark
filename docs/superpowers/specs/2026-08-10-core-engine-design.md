# Core Engine — Design Spec

Status: Approved for planning
Date: 2026-08-10
Scope: memremark Core Engine (single machine). Sync Layer across devices is a separate, later spec.

## 1. Problem & Goal

The user works across 3 devices (PC1 at the office, PC2 at home, a laptop at home), each running Claude Code and/or Antigravity CLI. Every new session currently starts with zero context — prior discussion, decisions, and project knowledge have to be re-explained by hand.

The Core Engine automatically captures what happens during a CLI session, stores it durably, distills it into reusable knowledge, and re-injects that knowledge into future sessions on the **same machine** — without the user doing anything manual. Cross-device propagation of that knowledge is out of scope here; it is the Sync Layer's job (next spec), built once the Core Engine has something worth syncing.

## 2. Reference prior art

Two existing tools were inspected directly (source read locally / via GitHub) to ground design decisions instead of guessing:

- **claude-mem** (`thedotmack/claude-mem`, plugin source available locally): long-running worker daemon started from `SessionStart`; `PostToolUse` logs raw observations; `Stop` triggers a debounced LLM summarization pass via `@anthropic-ai/claude-agent-sdk`; memory is scoped per project via a `project_id`/`projectPath` column in a single `~/.claude-mem/claude-mem.db`.
- **Mempalace** (`MemPalace/mempalace`, README + docs fetched): local-first, **verbatim** storage (no summarization) indexed with embeddings (ChromaDB by default); organizes memory into **wings** (a person, project, or topic — top-level), **rooms** (topics within a wing, auto-detected from folder structure), **halls** (memory-type categories: facts, events, discoveries, preferences, advice), and **tunnels** (cross-wing links when two wings share a room name — used for multi-person setups). Auto-save hooks exist for Claude Code, Codex CLI, and Cursor; a separate official plugin adds `Stop`/`PreInvocation` hooks for **Antigravity IDE**.
- **Antigravity CLI** (`google-antigravity/antigravity-cli`, CHANGELOG read from source archive v1.1.11): confirmed to have its own `hooks.json` (global `~/.gemini/config/hooks.json` or workspace-local `<workspace>/.agents/hooks.json`) supporting `PostToolUse` (with matchers), `Stop`, and `PostInvocation` hooks, plus a headless print mode (`agy -p "..." --output-format json|stream-json --json-schema <file>`) — a close parallel to Claude Code's hook system and `-p --output-format json`.

memremark's Core Engine is a deliberate combination of these: verbatim storage (Mempalace) for zero information loss, plus periodic LLM-driven summarization (claude-mem) to produce the "distilled knowledge" the project's goal calls for, organized with Mempalace's wing/hall vocabulary but trimmed to what a single-user, multi-CLI setup actually needs.

## 3. Non-goals (explicitly out of scope for this spec)

- Syncing memory across devices (Sync Layer — separate spec).
- Semantic/vector search over verbatim content. v1 context injection is recency-based only (most recent summaries), not similarity search.
- `room` auto-detection logic. The column exists in the schema so it can be added later without a migration, but no detection logic is built now.
- `tunnels` (cross-wing linking). This solves multi-person palace-sharing; the user is a single person across their own devices, so there is nothing to link.
- Any CLI other than Claude Code and Antigravity CLI.

## 4. Architecture

A single long-running daemon process (`memremarkd`) runs per machine, started idempotently. Each CLI's hooks call a thin adapter (a short script/binary) that talks to the daemon over a local Unix socket (loopback HTTP is an acceptable fallback if sockets prove awkward cross-platform).

Storage is one SQLite database per machine at `~/.memremark/memremark.db`. A single file, not one per project — matches both reference tools' actual behavior (they scope logically via columns, not physically via separate files), and keeps the Sync Layer's future job simpler (sync one file, not an unbounded set).

```
CLI (Claude Code / Antigravity CLI)
   |  hook fires (PostToolUse / Stop / SessionStart-equivalent)
   v
Adapter (per-CLI: parses hook JSON, talks to daemon)
   |  local socket
   v
memremarkd (long-running daemon)
   |
   v
SQLite (~/.memremark/memremark.db)
```

## 5. Adapters

Each CLI gets an adapter implementing two responsibilities:

1. **Hook payload parsing** — normalize that CLI's hook JSON into a common internal `Observation` struct (tool name, args/result summary, session id, timestamp, cwd).
2. **Headless invocation** — shell out to that CLI's own non-interactive mode to run the summarization sub-conversation, so memremark never needs its own LLM API key or billing.

| Capability | Claude Code | Antigravity CLI |
|---|---|---|
| Hook config | plugin `hooks.json` | `hooks.json` (global or workspace-local) |
| Hooks used | `PostToolUse`, `Stop`, `SessionStart` | `PostToolUse`, `Stop`, `PostInvocation` |
| Headless invoke | `claude -p "..." --output-format json` | `agy -p "..." --output-format json --json-schema <file>` |

`SessionStart` (Claude Code) and `PostInvocation` (Antigravity CLI) both fire at/near the start of a session and are the injection point for prior context — treated as equivalent in the adapter interface (`OnSessionStart`).

## 6. Data flow

1. **Verbatim capture** — on `PostToolUse` (both CLIs), the adapter posts the observation to the daemon, which writes it straight to SQLite as a `drawer` row (`type = 'verbatim'`, `hall = 'event'`). No LLM call. This is the source of truth and cannot lose information.
2. **Summarization** — on `Stop` (both CLIs, end of one agent turn), the adapter notifies the daemon. The daemon debounces (waits a few seconds of inactivity, coalescing rapid consecutive turns) and then spawns a headless sub-conversation through that session's own CLI, asking it to distill the verbatim drawers accumulated since the last summary into one or more summary `drawer` rows (`type = 'summary'`), each classified into a `hall` (`fact`, `discovery`, `preference`, or `advice`).
3. **Context injection** — on session start (`SessionStart` / `PostInvocation`), the daemon looks up the current working directory's `wing`, queries its most recent summary drawers ordered by time, and returns them to the adapter to inject into the new session's context.

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

- **Daemon unreachable when a hook fires**: the adapter fails fast with a short timeout and does not block the CLI's own responsiveness. No permanent data loss — the next successful `PostToolUse`/`Stop` call simply resumes from where the daemon's state left off.
- **Headless summarization call fails** (auth expired, network down, rate limited): verbatim drawers are already durably stored regardless, so nothing is lost. The daemon retries summarization on the next `Stop` debounce window.
- **Daemon crash**: restarted idempotently by the next `SessionStart`/`PostInvocation` hook firing (same pattern claude-mem uses for its worker-service).

## 9. Testing

- Unit tests for SQLite schema operations (insert/query drawers and wings) against an in-memory or temp-file SQLite DB.
- Adapter-level tests that feed each CLI's real hook JSON shape (captured from actual hook invocations) through the parser and assert the resulting `Observation` struct.
- One end-to-end smoke test: fake `PostToolUse` + `Stop` events through the daemon's socket API, assert a verbatim drawer and (with a stubbed headless invoker) a summary drawer both land correctly.

## 10. Open risks

- Antigravity CLI's hook system was confirmed via its public CHANGELOG (mentions of `hooks.json`, `PostToolUse`, `Stop`, `PostInvocation`, headless `-p`/`--output-format`), not by running the actual binary. The exact hook JSON payload shape needs to be captured empirically during implementation before the adapter's parser can be finalized.
- Debounce window length (how many seconds of inactivity before summarizing) is not yet tuned; start with claude-mem's approach as a reference point and adjust based on real usage.
