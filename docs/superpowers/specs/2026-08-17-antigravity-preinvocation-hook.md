# Antigravity CLI PreInvocation Hook — Design Spec

Status: Approved for planning
Date: 2026-08-17
Scope: Context injection into Antigravity CLI sessions. Capture path (protoscan polling) unchanged.

## 1. Problem

The Core Engine spec (2026-08-10) identified that Antigravity CLI hooks didn't execute reliably (§2.1). This left a gap: Claude Code sessions get prior-session context injected via `memremark-hook-claude-sessionstart`, but Antigravity CLI sessions start with zero memremark context.

Antigravity CLI hooks now work (confirmed 2026-08-17, tested directly). The gap can be closed.

## 2. Goal

Inject memremark summaries into the start of every new Antigravity CLI session, matching the behavior the Claude Code hook already provides. No changes to capture, storage, summarization, or daemon polling.

## 3. Design

### 3.1 Hook event choice

Antigravity CLI has no `SessionStart` event. It does have `PreInvocation`, which fires before every model call. The input payload includes `invocationNum` (0-indexed). When `invocationNum == 0`, it is the first model call of the session — functionally equivalent to session start.

The hook binary runs on every `PreInvocation` but only does real work when `invocationNum == 0`. All other invocations return `{}` immediately (no DB open, no I/O).

### 3.2 Hook binary

New binary: `cmd/memremark-hook-antigravity-preinvocation/main.go`

**Input** (stdin, JSON — Antigravity CLI PreInvocation contract):
```json
{
  "invocationNum": 0,
  "initialNumSteps": 0,
  "conversationId": "...",
  "workspacePaths": ["/home/minh/personal/memremark"],
  "transcriptPath": "...",
  "artifactDirectoryPath": "...",
  "modelName": "..."
}
```

**Logic**:
1. Parse stdin JSON.
2. If `invocationNum != 0` → write `{}` to stdout, exit 0.
3. Read `workspacePaths[0]` as the project directory.
4. Open `~/.memremark/memremark.db`.
5. `GetOrCreateWing(workspacePaths[0])` → `wingID`.
6. `RecentSummaries(wingID, 10)` → summaries.
7. If no summaries → write `{}` to stdout, exit 0.
8. Format summaries into a single text block (same format as Claude hook).
9. Write to stdout:
```json
{
  "injectSteps": [
    {
      "ephemeralMessage": "Bối cảnh từ các phiên làm việc trước (memremark):\n- [fact] ...\n- [discovery] ..."
    }
  ]
}
```
10. Exit 0.

**Error handling**: Same as Claude hook — errors go to stderr, never fail the hook's exit code (a broken memory feature must not block the user from starting a session). On any error, output `{}` to stdout and exit 0.

### 3.3 Input payload struct

```go
type hookInput struct {
    InvocationNum int      `json:"invocationNum"`
    WorkspacePaths []string `json:"workspacePaths"`
}
```

Only fields the hook needs are decoded. Unknown fields are ignored (standard `json.Unmarshal` behavior).

### 3.4 Output payload struct

```go
type hookOutput struct {
    InjectSteps []injectStep `json:"injectSteps,omitempty"`
}

type injectStep struct {
    EphemeralMessage string `json:"ephemeralMessage,omitempty"`
}
```

No summaries → `hookOutput{}` marshals to `{}`.
With summaries → `hookOutput{InjectSteps: [...]}` marshals to `{"injectSteps": [{"ephemeralMessage": "..."}]}`.

### 3.5 Shared code

`formatSummaries` logic is identical between both hooks. Options:
- **Extract to `internal/hook/format.go`** — shared package.
- **Duplicate** — 6 lines, acceptable.

Decision: **duplicate**. Both hook binaries are thin (~80 LOC) entry points. A shared package for 6 lines adds a directory and import for negligible dedup. If a third hook is ever needed, extract then.

### 3.6 Installation

User adds a `hooks.json` to their workspace `.agents/` or global `~/.gemini/config/`:

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

README updated with install instructions for both hooks.

## 4. What does NOT change

- Daemon polling of `conversation_summaries.db` + `conversations/*.db` (capture path) — unchanged.
- Protoscan adapter — unchanged.
- Storage schema — unchanged.
- Claude Code hook — unchanged.
- Summarizer — unchanged.

## 5. Testing

1. **Unit test**: `main_test.go` mirrors `cmd/memremark-hook-claude-sessionstart/main_test.go` structure:
   - `invocationNum == 0`, summaries exist → output contains `injectSteps` with `ephemeralMessage`.
   - `invocationNum == 0`, no summaries → output is `{}`.
   - `invocationNum > 0` → output is `{}` (fast path, no DB opened).
   - Malformed stdin → output is `{}`, error on stderr.
   - Missing `workspacePaths` → output is `{}`, error on stderr.

2. **No integration test needed**: The hook is a stateless binary that reads from the same SQLite store already tested by `storage/*_test.go`.

## 6. File changes

| Action | Path | LOC estimate |
|---|---|---|
| Create | `cmd/memremark-hook-antigravity-preinvocation/main.go` | ~85 |
| Create | `cmd/memremark-hook-antigravity-preinvocation/main_test.go` | ~120 |
| Update | `README.md` | ~15 lines added |
