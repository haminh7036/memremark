# Consolidate Summarizer Sessions & Provider Offloading Design

- **Issue:** [#4 - Consolidate memremarkd CLI summarizer sessions & offload to Antigravity](https://github.com/haminh7036/memremark/issues/4)
- **Author:** Antigravity & User Pair Programming
- **Date:** 2026-08-25
- **Status:** Approved / In Review

---

## 1. Overview & Goals

Currently, whenever the background daemon (`memremarkd`) summarizes verbatim observations:
1. It creates an arbitrary unmanaged session for each invocation (`claude -p` creates a new UUID `.jsonl` transcript, `agy -p` creates an unattached conversation).
2. It executes commands from whatever working directory the daemon was launched in (typically user root or systemd working dir), creating cluttered log artifacts in arbitrary locations.
3. It defaults to invoking Claude Code CLI (`claude -p`) for Claude Code transcripts and Antigravity CLI (`agy -p`) for Antigravity conversations. Users who want to preserve Claude quota for interactive coding have no way to route 100% of background summarization through Antigravity (`gemini-3.7-flash-low`).

### Goals
- **Configurable Summarizer Provider:** Support `provider: "auto" | "antigravity" | "claude"` in `~/.memremark/config.json` and environment variable `MEMREMARK_SUMMARIZER_PROVIDER`. Default in codebase is `"auto"`.
- **Deterministic Workspace Session UUID:** Consolidate all summarization prompts for a given workspace (`wing`) into a single persistent conversation UUID per workspace across both adapters.
- **Project Scoping:** Set `cmd.Dir = wing.Path` during headless summarization invocations so transcript artifacts stay inside the respective project directories.
- **Zero Feedback Loop:** Ensure daemon's transcript discoverers ignore dedicated summarizer sessions to prevent infinite recursive summarization.

---

## 2. Architecture & Components

### 2.1. Provider Configuration (`internal/config/config.go`)

Extend `SummarizerConfig` with `Provider`:

```go
const (
	DefaultProvider          = "auto"
	DefaultLanguage          = "auto"
	DefaultClaudeModel       = "haiku"
	DefaultAntigravityModel  = "gemini-3.7-flash-low"
	DefaultAntigravityEffort = "low"
)

type SummarizerConfig struct {
	Provider          string `json:"provider"` // "auto" (default), "antigravity", "claude"
	ClaudeModel       string `json:"claude_model"`
	AntigravityModel  string `json:"antigravity_model"`
	AntigravityEffort string `json:"antigravity_effort"`
}
```

Environment variable override:
- `MEMREMARK_SUMMARIZER_PROVIDER`: overrides `cfg.Summarizer.Provider`.

### 2.2. Deterministic UUIDv5 per Wing (`internal/storage/uuid.go`)

Generate a deterministic UUIDv5 (RFC 4122) from the canonical workspace path using SHA-1 and standard URL namespace:

```go
// WingSummarySessionID returns a deterministic RFC 4122 UUIDv5 for a canonical wing path.
func WingSummarySessionID(wingPath string) string
```

Properties:
- Purely deterministic and stateless. No schema migration required in SQLite.
- Given the same normalized path (e.g. `/home/minh/personal/memremark`), it always yields the exact same UUID.
- Meets CLI validation requirements (standard 36-char string format: `xxxxxxxx-xxxx-5xxx-xxxx-xxxxxxxxxxxx`).

### 2.3. Invoker Interface & Concrete Implementations (`internal/summarizer/summarizer.go`)

Add `InvokerOptions` struct to pass execution metadata:

```go
type InvokerOptions struct {
	SessionID string // Persistent workspace summary UUID
	WorkDir   string // Canonical workspace path (wing.Path)
}

type Invoker interface {
	Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)
}
```

#### Concrete Invoker Behaviors:
1. **`ClaudeCodeInvoker`**:
   - If `opts.SessionID != ""`, append `"--session-id", opts.SessionID` to arguments.
   - If `opts.WorkDir != ""`, set `cmd.Dir = opts.WorkDir`.
2. **`AntigravityInvoker`**:
   - If `opts.SessionID != ""`, append `"--conversation", opts.SessionID` to arguments.
   - If `opts.WorkDir != ""`, set `cmd.Dir = opts.WorkDir`.
3. **`FallbackInvoker`**:
   - Forwards `opts...` to `Primary.Invoke` and `Fallback.Invoke`.
4. **`NopInvoker`**:
   - Accepts `opts...` and returns no-op error.

### 2.4. Daemon Provider Routing (`cmd/memremarkd/main.go` & `internal/daemon`)

In `resolveInvokers(cfg config.Config, lookPath func(string) (string, error))`:
- **`provider == "antigravity"`**:
  - `ClaudeInvoker = AntigravityInvoker (with fallback to Claude)`
  - `AntigravityInvoker = AntigravityInvoker (with fallback to Claude)`
- **`provider == "claude"`**:
  - `ClaudeInvoker = ClaudeCodeInvoker (with fallback to Antigravity)`
  - `AntigravityInvoker = ClaudeCodeInvoker (with fallback to Antigravity)`
- **`provider == "auto"`**:
  - `ClaudeInvoker = ClaudeCodeInvoker (with fallback to Antigravity)`
  - `AntigravityInvoker = AntigravityInvoker (with fallback to Claude)`

### 2.5. Daemon Summarize & Scoping Flow (`internal/daemon/daemon_summarize.go`)

When `d.summarizeSessionWithBatchSize` executes:
1. Retrieve `wing, err := d.Store.GetWingByID(wingID)` (or query path from `wings` table).
2. Calculate `summarySessionID := storage.WingSummarySessionID(wing.Path)`.
3. Pass `InvokerOptions{SessionID: summarySessionID, WorkDir: wing.Path}` to `summarizer.Summarize`.

### 2.6. Loop Prevention (Excluding Summary Sessions)

To prevent the daemon from ingesting its own summarization requests/responses:
1. **Claude Code Adapter (`internal/adapter/claudecode/tailer.go` & `daemon_claudecode.go`)**:
   - In `pollClaudeCode`, extract session ID from `.jsonl` filename: `base := strings.TrimSuffix(filepath.Base(file), ".jsonl")`.
   - Check if `base` matches `WingSummarySessionID(wingPath)` of any active wing (or match against known summary session UUIDs). If matched, skip processing.
2. **Antigravity Adapter (`internal/daemon/daemon_antigravity.go`)**:
   - In `pollAntigravity`, check if `conv.ConversationID` matches `WingSummarySessionID(wingPath)`. If matched, skip recording verbatim observations.

---

## 3. Storage Layer Additions

Add helper to `internal/storage/wings.go`:
```go
// Wing represents a row in the wings table.
type Wing struct {
	ID        int64
	Path      string
	Name      string
	CreatedAt time.Time
}

// GetWingByID returns the Wing matching id.
func (s *Store) GetWingByID(id int64) (*Wing, error)
```

---

## 4. Testing & Verification

1. **UUIDv5 Determinism Test (`internal/storage/uuid_test.go`)**:
   - Verify `WingSummarySessionID("/path/to/project")` returns valid RFC 4122 UUIDv5.
   - Verify idempotency across identical paths and normalization (trailing slashes).
2. **Invoker Options & Args Test (`internal/summarizer/summarizer_test.go`)**:
   - Verify `ClaudeCodeInvoker.buildArgs` includes `--session-id` when supplied.
   - Verify `AntigravityInvoker.buildArgs` includes `--conversation` when supplied.
   - Verify `cmd.Dir` is set to `WorkDir`.
3. **Loop Prevention Test (`internal/daemon/daemon_test.go`)**:
   - Verify `pollClaudeCode` skips `<wing-summary-uuid>.jsonl`.
   - Verify `pollAntigravity` skips conversation with `<wing-summary-uuid>`.
4. **Provider Routing Integration Test (`cmd/memremarkd/main_test.go`)**:
   - Test resolveInvokers with `provider: "antigravity"`, `"claude"`, `"auto"`.
