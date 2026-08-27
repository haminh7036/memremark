# Richer Knowledge Distillation Schema: Facts vs Narrative Split & Session-Level Digest

- **Issue:** [#3 - [RFC] Richer knowledge distillation schema: facts vs narrative split & session-level digest](https://github.com/haminh7036/memremark/issues/3)
- **Author:** Antigravity & User Pair Programming
- **Date:** 2026-08-27
- **Status:** Approved / In Review

---

## 1. Overview & Problem Statement

Currently, `memremark` distills raw tool activity into `drawers` with `type='summary'` and one of 4 halls (`fact`, `discovery`, `preference`, `advice`). Each summary row contains a single `content` field that is enforced to be "direct, concise, telegraphic bullet points" because that same field is re-injected on every fresh session start via `hookctx.GetSummaries`.

### Limitations
1. **Single Flat Style (Atomic vs Narrative):** There is no richer, optional view of an event when full context matters (e.g. browsing history in the Web UI or deep-diving with `search_memory`). Everything is compressed to telegraphic bullets.
2. **No Macro Session Digest:** Long sessions with multiple batches produce many individual summary items, but no single high-level synthesis ("what was this session about, what did we investigate, what was completed, what are the next steps?").

### Goals
- **Atomic vs Narrative Split:** Add an optional `narrative` column to summary drawers. Keep `content` as the cheap, telegraphic bullet point for hook injection, and populate `narrative` with richer prose (what was done, why, impact) from the same LLM call.
- **Session-Level Digests (`session_digests` table):** Synthesize a structured, multi-field digest per session (`request`, `investigated`, `learned`, `completed`, `next_steps`, `notes`) upon session idle/close.
- **Zero Ingestion Token Overhead:** Hook session startup context (`hookctx`) continues to read *only* `content`, preserving 0% overhead on prompt tokens for new sessions.
- **Full Backward Compatibility:** Automatic migration for existing databases, graceful handling of NULL/missing narratives, and non-breaking MCP/UI enhancements.

---

## 2. Database Schema & Migration

### 2.1. Alter `drawers` Table (`narrative` column)
Add `narrative TEXT` (nullable) to the `drawers` table.

```sql
-- DDL update for new databases
CREATE TABLE IF NOT EXISTS drawers (
	id INTEGER PRIMARY KEY,
	wing_id INTEGER NOT NULL REFERENCES wings(id),
	type TEXT NOT NULL CHECK (type IN ('verbatim','summary')),
	hall TEXT NOT NULL CHECK (hall IN ('event','fact','discovery','preference','advice')),
	content TEXT NOT NULL,
	narrative TEXT,
	tool_name TEXT,
	session_id TEXT NOT NULL,
	covers_from INTEGER,
	covers_to INTEGER,
	created_at INTEGER NOT NULL
);
```

**Migration in `storage.Open`:**
Inspect table columns using `PRAGMA table_info(drawers);`. If `narrative` is absent, execute:
```sql
ALTER TABLE drawers ADD COLUMN narrative TEXT;
```

### 2.2. New Table: `session_digests`
Dedicated table for macro session summaries.

```sql
CREATE TABLE IF NOT EXISTS session_digests (
	id INTEGER PRIMARY KEY,
	wing_id INTEGER NOT NULL REFERENCES wings(id),
	session_id TEXT UNIQUE NOT NULL,
	request TEXT NOT NULL,
	investigated TEXT NOT NULL,
	learned TEXT NOT NULL,
	completed TEXT NOT NULL,
	next_steps TEXT NOT NULL,
	notes TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_digests_wing_created ON session_digests(wing_id, created_at);
```

### 2.3. Storage Layer Data Structures & Methods (`internal/storage`)

#### Structs
```go
type Drawer struct {
	ID         int64
	WingID     int64
	Type       string
	Hall       string
	ToolName   string
	Content    string
	Narrative  string // New field
	SessionID  string
	CoversFrom int64
	CoversTo   int64
	CreatedAt  time.Time
}

type SessionDigest struct {
	ID           int64     `json:"id"`
	WingID       int64     `json:"wing_id"`
	SessionID    string    `json:"session_id"`
	Request      string    `json:"request"`
	Investigated string    `json:"investigated"`
	Learned      string    `json:"learned"`
	Completed    string    `json:"completed"`
	NextSteps    string    `json:"next_steps"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
}
```

#### Methods
- `InsertSummaryDrawer(wingID int64, sessionID, hall, content, narrative string, coversFrom, coversTo, createdAt time.Time) error`
- `UpsertSessionDigest(digest SessionDigest) error` (uses `INSERT INTO session_digests (...) VALUES (...) ON CONFLICT(session_id) DO UPDATE SET request=excluded.request, investigated=excluded.investigated, learned=excluded.learned, completed=excluded.completed, next_steps=excluded.next_steps, notes=excluded.notes, created_at=excluded.created_at`)
- `GetSessionDigest(sessionID string) (*SessionDigest, error)`
- `ListSessionDigests(wingID int64, limit int) ([]SessionDigest, error)`
- `GetDrawersBySession(wingID int64, sessionID string, drawerType string) ([]Drawer, error)`

---

## 3. Distillation & Synthesis Pipeline (`internal/summarizer`)

### 3.1. Micro Summarization (`SummaryItem` with Narrative)
Update `SummaryItem`:
```go
type SummaryItem struct {
	Hall      string `json:"hall"`
	Content   string `json:"content"`
	Narrative string `json:"narrative,omitempty"`
}
```

#### Prompt Guidelines
- `content`: Terse, concise, telegraphic bullet point (1 line) stating the invariant, discovery, or rule.
- `narrative`: 1–3 sentences providing context, rationale, and consequences (what happened, why, and how it was resolved).
- Output format: `[{"hall": "fact|discovery|preference|advice", "content": "...", "narrative": "..."}]`

#### API Schemas
- **Gemini (`gemini.go`):** Add `narrative` (`STRING`) to `geminiItemsSchema.Properties`.
- **Anthropic (`anthropic.go`):** Add `narrative` (`string`) to `output_config.format.schema.items.properties`.

### 3.2. Session Digest Synthesis (`internal/summarizer/digest.go`)
Create `internal/summarizer/digest.go`:

```go
type SessionDigestResult struct {
	Request      string `json:"request"`
	Investigated string `json:"investigated"`
	Learned      string `json:"learned"`
	Completed    string `json:"completed"`
	NextSteps    string `json:"next_steps"`
	Notes        string `json:"notes"`
}

func SynthesizeSessionDigest(
	ctx context.Context,
	invoker Invoker,
	summaries []storage.Drawer,
	fallbackVerbatim []storage.Drawer,
	lang locale.TargetLanguage,
	opts ...InvokerOptions,
) (*SessionDigestResult, error)
```

**Synthesis Logic:**
1. If `len(summaries) > 0`, format the prompt with all `content` and `narrative` items from that session.
2. If `len(summaries) == 0` but `len(fallbackVerbatim) > 0`, extract key observations from the verbatim entries.
3. Prompt instructs LLM to produce a JSON object with 6 structured fields (`request`, `investigated`, `learned`, `completed`, `next_steps`, `notes`) localized to `lang.Name`.
4. Parse and return `*SessionDigestResult`.

---

## 4. Daemon Pipeline & Session Lifecycle (`internal/daemon`)

### 4.1. Automatic Digest Synthesis
In `internal/daemon/daemon_summarize.go`:
1. When `Tracker.Due(now, idleWindow)` triggers for an inactive session:
   - Call `summarizeSession(ctx, sessionID, now)`: batches verbatim rows into summary drawers and prunes them.
   - Immediately call `synthesizeSessionDigest(ctx, sessionID, wingID, invoker, now)`.
2. `synthesizeSessionDigest`:
   - Queries all summary drawers for `sessionID`.
   - Invokes `summarizer.SynthesizeSessionDigest`.
   - On success, saves to database with `Store.UpsertSessionDigest`.
   - Logs: `daemon: synthesized session digest for %s (wing %d)`.

### 4.2. Error Resilience
- Failures during digest generation (e.g. LLM timeout) are logged as warnings and do not roll back distilled summary drawers or fail subsequent polling tasks.
- Re-triggerable at any time via on-demand MCP tool or on next session batch.

---

## 5. Read Paths, MCP Tools & Web UI Integration

### 5.1. Hook Context Injection (`internal/hookctx`)
- `FormatSummaries` strictly formats `- [%s] %s\n` using `d.Content`.
- `d.Narrative` is **not** included in the ephemeral injection prompt.
- **Invariant:** Zero extra token consumption on session start.

### 5.2. MCP Server (`internal/mcp/server.go`)
1. **Tool `search_memory`**:
   - Returns both `content` and `narrative` (when present) in the text response and output objects.
2. **Tool `get_session_digest` (New)**:
   - `session_id` (string, required)
   - `wing_path` (string, optional)
   - `regenerate` (boolean, optional, default: false)
   - Fetches cached `SessionDigest` or triggers fresh synthesis if `regenerate: true` or no cached digest exists.
3. **Tool `get_timeline`**:
   - Includes `digest` metadata at the top of the session timeline response if available.

### 5.3. Web UI Dashboard (`internal/ui` & `web/`)
1. **API Endpoints**:
   - `GET /api/wings/{id}/digests`
   - `GET /api/sessions/{sessionId}/digest`
2. **UI Enhancements**:
   - **Timeline View**: Renders a dedicated "Session Digest" card at the top of each session group with collapsible sections for Request, Investigated, Learned, Completed, and Next Steps.
   - **Drawers View**: Renders `content` as primary title/bullet, with expandable accordion or subtitle for `narrative`.

---

## 6. Invariants & Non-Goals

1. **Storage Integrity:** `drawers.type` remains strictly `'verbatim'` or `'summary'`. Macro summaries reside exclusively in `session_digests`.
2. **Prompt Budget:** Session digest prompt uses already distilled memory items (<2 KB), avoiding raw verbatim transcript bloat.
3. **Graceful Fallback:** If LLM invoker fails, the core memory system continues normal operation.

---

## 7. Testing & Verification

1. **Unit Tests:**
   - `internal/storage`: migration test, `InsertSummaryDrawer` with narrative, `UpsertSessionDigest`, `GetSessionDigest`, `ListSessionDigests`.
   - `internal/summarizer`: `Summarize` parsing `narrative`, `SynthesizeSessionDigest` parsing structured fields, Gemini/Anthropic request schemas.
   - `internal/daemon`: test that idle session triggers batch summarization + digest synthesis and writes to `session_digests`.
   - `internal/mcp`: test `get_session_digest` and updated `search_memory`.
   - `internal/hookctx`: verify `FormatSummaries` output format is unchanged.
2. **End-to-End Verification:**
   - `go test -v -race ./...` passing.
   - Daemon run with simulated session -> verify both summary drawers (with narrative) and `session_digests` row exist in SQLite.
