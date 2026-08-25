# Consolidate Summarizer Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Consolidate memremarkd CLI summarizer sessions into a deterministic UUID per workspace across Claude Code and Antigravity, add configurable provider routing with Antigravity offloading, and prevent daemon transcript feedback loops.

**Architecture:** We use deterministic RFC 4122 UUIDv5 (derived from canonical workspace path) to identify each workspace's persistent summary session. `ClaudeCodeInvoker` and `AntigravityInvoker` accept `InvokerOptions` (`SessionID` and `WorkDir`) to attach to the persistent session and run in the workspace root directory. Tailers and discovery loops ignore dedicated summary session UUIDs to prevent infinite feedback loops. `SummarizerConfig.Provider` routes summarization requests to `antigravity`, `claude`, or `auto`.

**Tech Stack:** Go (standard library `crypto/sha1`, `database/sql`, `os/exec`, `path/filepath`, `testing`; existing `internal/config`, `internal/storage`, `internal/summarizer`, `internal/daemon`).

## Global Constraints

- Standard library + existing internal packages only (no new third-party dependencies).
- Zero external LLM token expenditure during testing: all tests must use stub/mock invokers.
- `DefaultConfig()` provider default is `"auto"`.
- Backward compatible with existing databases without requiring schema migration.

---

### Task 1: Config Provider Setting

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `os.Getenv("MEMREMARK_SUMMARIZER_PROVIDER")`
- Produces: `SummarizerConfig.Provider` (`"auto"`, `"antigravity"`, `"claude"`), `DefaultProvider = "auto"`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestConfig_SummarizerProvider(t *testing.T) {
	// Test default value
	cfg := DefaultConfig()
	if cfg.Summarizer.Provider != "auto" {
		t.Fatalf("expected default provider 'auto', got %q", cfg.Summarizer.Provider)
	}

	// Test loading from file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	content := `{
		"summarizer": {
			"provider": "antigravity"
		}
	}`
	if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := LoadFromFile(configFile)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if loaded.Summarizer.Provider != "antigravity" {
		t.Fatalf("expected loaded provider 'antigravity', got %q", loaded.Summarizer.Provider)
	}

	// Test environment variable override
	t.Setenv("MEMREMARK_SUMMARIZER_PROVIDER", "claude")
	loadedEnv, err := LoadFromFile(configFile)
	if err != nil {
		t.Fatalf("LoadFromFile with env: %v", err)
	}
	if loadedEnv.Summarizer.Provider != "claude" {
		t.Fatalf("expected env overridden provider 'claude', got %q", loadedEnv.Summarizer.Provider)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestConfig_SummarizerProvider -v`
Expected: FAIL (`cfg.Summarizer.Provider undefined`)

- [ ] **Step 3: Implement minimal code**

In `internal/config/config.go`:
1. Add `DefaultProvider = "auto"` to `const` block.
2. Add `Provider string ` + "`json:\"provider\"`" to `SummarizerConfig` struct.
3. In `DefaultConfig()`, set `Provider: DefaultProvider`.
4. In `LoadFromFile()`, add:
```go
	if env := os.Getenv("MEMREMARK_SUMMARIZER_PROVIDER"); env != "" {
		cfg.Summarizer.Provider = env
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -run TestConfig_SummarizerProvider -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add provider option to SummarizerConfig"
```

---

### Task 2: Deterministic UUIDv5 & Wing Lookup

**Files:**
- Create: `internal/storage/uuid.go`
- Test: `internal/storage/uuid_test.go`
- Modify: `internal/storage/wings.go`
- Test: `internal/storage/wings_test.go`

**Interfaces:**
- Produces:
  ```go
  func WingSummarySessionID(wingPath string) string
  func (s *Store) GetWingByID(id int64) (*Wing, error)
  ```

- [ ] **Step 1: Write failing tests for UUIDv5 and GetWingByID**

Create `internal/storage/uuid_test.go`:

```go
package storage

import (
	"regexp"
	"testing"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestWingSummarySessionID_Determinism(t *testing.T) {
	path1 := "/home/user/project"
	path2 := "/home/user/project/" // trailing slash should normalize to same ID
	path3 := "/home/user/other-project"

	id1 := WingSummarySessionID(path1)
	id2 := WingSummarySessionID(path2)
	id3 := WingSummarySessionID(path3)

	if !uuidRegex.MatchString(id1) {
		t.Fatalf("id1 %q is not a valid UUIDv5", id1)
	}
	if id1 != id2 {
		t.Fatalf("expected normalized paths to yield identical ID: id1=%q, id2=%q", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("expected different paths to yield different IDs: id1=%q, id3=%q", id1, id3)
	}
}
```

Add to `internal/storage/drawers_test.go` (or `wings_test.go`):

```go
func TestStore_GetWingByID(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.GetOrCreateWing("/tmp/project-wing-test")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	wing, err := s.GetWingByID(id)
	if err != nil {
		t.Fatalf("GetWingByID: %v", err)
	}
	if wing.ID != id || wing.Path != "/tmp/project-wing-test" || wing.Name != "project-wing-test" {
		t.Fatalf("unexpected wing retrieved: %+v", wing)
	}

	// Non-existent ID
	missing, err := s.GetWingByID(999999)
	if err == nil || missing != nil {
		t.Fatalf("expected error for non-existent wing ID, got %v, err: %v", missing, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/storage/... -run "TestWingSummarySessionID|TestStore_GetWingByID" -v`
Expected: FAIL (undefined symbols)

- [ ] **Step 3: Implement UUIDv5 and GetWingByID**

Create `internal/storage/uuid.go`:

```go
package storage

import (
	"crypto/sha1"
	"fmt"
)

// Standard URL namespace UUID (RFC 4122): 6ba7b811-9dad-11d1-80b4-00c04fd430c8
var urlNamespace = []byte{
	0x6b, 0xa7, 0xb8, 0x11,
	0x9d, 0xad,
	0x11, 0xd1,
	0x80, 0xb4,
	0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8,
}

// WingSummarySessionID returns a deterministic RFC 4122 UUIDv5 for a canonical wing path.
func WingSummarySessionID(wingPath string) string {
	cleanPath := normalizePath(wingPath)
	h := sha1.New()
	h.Write(urlNamespace)
	h.Write([]byte(cleanPath))
	sum := h.Sum(nil)

	// Set version 5 (bits 4-7 of byte 6 = 0101)
	sum[6] = (sum[6] & 0x0f) | 0x50
	// Set variant RFC 4122 (bits 6-7 of byte 8 = 10)
	sum[8] = (sum[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4],
		sum[4:6],
		sum[6:8],
		sum[8:10],
		sum[10:16],
	)
}
```

In `internal/storage/wings.go`:

```go
// Wing represents a single project workspace.
type Wing struct {
	ID        int64     `json:"id"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// GetWingByID returns the Wing matching id.
func (s *Store) GetWingByID(id int64) (*Wing, error) {
	var w Wing
	var createdAt int64
	err := s.db.QueryRow(`SELECT id, path, name, created_at FROM wings WHERE id = ?`, id).
		Scan(&w.ID, &w.Path, &w.Name, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("storage: get wing by id %d: %w", id, err)
	}
	w.CreatedAt = time.Unix(createdAt, 0)
	return &w, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/storage/... -run "TestWingSummarySessionID|TestStore_GetWingByID" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/uuid.go internal/storage/uuid_test.go internal/storage/wings.go internal/storage/drawers_test.go
git commit -m "feat(storage): add WingSummarySessionID UUIDv5 generator and GetWingByID"
```

---

### Task 3: Invoker Options & Args Update

**Files:**
- Modify: `internal/summarizer/summarizer.go`
- Modify: `internal/summarizer/summarizer_test.go`

**Interfaces:**
- Produces:
  ```go
  type InvokerOptions struct {
      SessionID string
      WorkDir   string
  }
  type Invoker interface {
      Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)
  }
  ```

- [ ] **Step 1: Write failing tests for Invoker argument builders with options**

Add to `internal/summarizer/summarizer_test.go`:

```go
func TestClaudeCodeInvoker_BuildArgs_WithSessionID(t *testing.T) {
	inv := ClaudeCodeInvoker{Model: "haiku"}
	args := inv.buildArgs("sess-uuid-1234")
	hasSessionFlag := false
	for i, a := range args {
		if a == "--session-id" && i+1 < len(args) && args[i+1] == "sess-uuid-1234" {
			hasSessionFlag = true
			break
		}
	}
	if !hasSessionFlag {
		t.Fatalf("expected --session-id sess-uuid-1234 in args, got: %v", args)
	}
}

func TestAntigravityInvoker_BuildArgs_WithConversationID(t *testing.T) {
	inv := AntigravityInvoker{Model: "gemini-3.7-flash-low", Effort: "low"}
	args := inv.buildArgs("test prompt", "conv-uuid-5678")
	hasConvFlag := false
	for i, a := range args {
		if a == "--conversation" && i+1 < len(args) && args[i+1] == "conv-uuid-5678" {
			hasConvFlag = true
			break
		}
	}
	if !hasConvFlag {
		t.Fatalf("expected --conversation conv-uuid-5678 in args, got: %v", args)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/summarizer/... -v`
Expected: FAIL (build args signature mismatch)

- [ ] **Step 3: Update `Invoker` interface and implementations**

In `internal/summarizer/summarizer.go`:
1. Define:
```go
type InvokerOptions struct {
	SessionID string
	WorkDir   string
}

type Invoker interface {
	Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)
}
```
2. Update `ClaudeCodeInvoker`:
```go
func (inv ClaudeCodeInvoker) buildArgs(sessionID string) []string {
	args := []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	model := inv.Model
	if model == "" {
		model = "haiku"
	}
	if model != "default" && model != "none" {
		args = append(args, "--model", model)
	}
	return args
}

func (inv ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	var opt InvokerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	cmd := exec.CommandContext(ctx, "claude", inv.buildArgs(opt.SessionID)...)
	if opt.WorkDir != "" {
		cmd.Dir = opt.WorkDir
	}
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: claude -p failed: %w", err)
	}
	var res claudeCodeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse claude -p output: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("summarizer: claude -p reported an error result")
	}
	return res.Result, nil
}
```
3. Update `AntigravityInvoker`:
```go
func (inv AntigravityInvoker) buildArgs(prompt, sessionID string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--disable-slash-commands"}
	if sessionID != "" {
		args = append(args, "--conversation", sessionID)
	}
	model := inv.Model
	if model == "" {
		model = "gemini-3.7-flash-low"
	}
	if model != "default" && model != "none" {
		args = append(args, "--model", model)
	}
	effort := inv.Effort
	if effort == "" {
		effort = "low"
	}
	if effort != "default" && effort != "none" {
		args = append(args, "--effort", effort)
	}
	return args
}

func (inv AntigravityInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	var opt InvokerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	cmd := exec.CommandContext(ctx, "agy", inv.buildArgs(prompt, opt.SessionID)...)
	if opt.WorkDir != "" {
		cmd.Dir = opt.WorkDir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: agy -p failed: %w", err)
	}
	var res antigravityResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse agy -p output: %w", err)
	}
	if res.Status != "SUCCESS" {
		return "", fmt.Errorf("summarizer: agy -p returned status %q: %s", res.Status, res.Error)
	}
	return res.Response, nil
}
```
4. Update `NopInvoker` & `FallbackInvoker` to accept `opts ...InvokerOptions` and forward them.
5. Update `Summarize` to take `options ...InvokerOptions` or signature:
```go
func SummarizeWithOptions(ctx context.Context, invoker Invoker, observations []observation.Observation, lang locale.TargetLanguage, opts ...InvokerOptions) ([]SummaryItem, error)
```
and keep backward-compatible `Summarize(ctx, invoker, obs, lang...)`.

- [ ] **Step 4: Update mock invokers in tests & run test suite**

Update any test stubs in `internal/summarizer/summarizer_test.go` and `internal/daemon/daemon_summarize_test.go` to match `Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)`.

Run: `go test ./internal/summarizer/... ./internal/daemon/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/ internal/daemon/daemon_summarize_test.go
git commit -m "feat(summarizer): support InvokerOptions for session ID and work directory scoping"
```

---

### Task 4: Prevent Daemon Summarizer Feedback Loops

**Files:**
- Modify: `internal/daemon/daemon_claudecode.go`
- Modify: `internal/daemon/daemon_antigravity.go`
- Test: `internal/daemon/daemon_loop_test.go`

**Interfaces:**
- Consumes: `storage.WingSummarySessionID`

- [ ] **Step 1: Write failing tests for loop exclusion**

Create `internal/daemon/daemon_loop_test.go`:

```go
package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestPollClaudeCode_SkipsWingSummarySessions(t *testing.T) {
	store, err := storage.Open(tempDBPath(t))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	projectDir := filepath.Join(t.TempDir(), "my-project")
	_ = os.MkdirAll(projectDir, 0o755)
	wingID, _ := store.GetOrCreateWing(projectDir)

	claudeProjects := t.TempDir()
	claudeProjectSubdir := filepath.Join(claudeProjects, "-tmp-my-project")
	_ = os.MkdirAll(claudeProjectSubdir, 0o755)

	summaryUUID := storage.WingSummarySessionID(projectDir)
	summaryFile := filepath.Join(claudeProjectSubdir, summaryUUID+".jsonl")

	// Write mock summary session transcript
	jsonlContent := `{"type":"user","message":{"content":"Given the following raw tool observations..."}}` + "\n"
	_ = os.WriteFile(summaryFile, []byte(jsonlContent), 0o600)

	invoker := stubInvoker{}
	d := New(store, claudeProjects, t.TempDir()+"/conv.db", invoker, invoker)

	if err := d.pollClaudeCode(time.Now()); err != nil {
		t.Fatalf("pollClaudeCode: %v", err)
	}

	drawers, err := store.SearchDrawers(wingID, "", "", "verbatim", 10)
	if err != nil {
		t.Fatalf("SearchDrawers: %v", err)
	}
	if len(drawers) != 0 {
		t.Fatalf("expected 0 verbatim drawers for summary session, got %d", len(drawers))
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/daemon/... -run TestPollClaudeCode_SkipsWingSummarySessions -v`
Expected: FAIL (either read or recorded)

- [ ] **Step 3: Implement loop exclusion in pollClaudeCode and pollAntigravity**

In `internal/daemon/daemon_claudecode.go`:
```go
		sessionID := strings.TrimSuffix(filepath.Base(file), ".jsonl")
		if d.isSummarySession(sessionID) {
			continue
		}
```

Add helper `isSummarySession(sessionID string) bool` to `Daemon` (checks if `sessionID` matches `storage.WingSummarySessionID` for any known wing or cached summary session IDs).

In `internal/daemon/daemon_antigravity.go`:
```go
		if d.isSummarySession(conv.ConversationID) {
			continue
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/... -run TestPollClaudeCode_SkipsWingSummarySessions -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/daemon_claudecode.go internal/daemon/daemon_antigravity.go internal/daemon/daemon.go internal/daemon/daemon_loop_test.go
git commit -m "feat(daemon): exclude dedicated summarizer sessions from transcript polling"
```

---

### Task 5: Summarizer Scoping & Provider Routing Wire-up

**Files:**
- Modify: `internal/daemon/daemon_summarize.go`
- Modify: `cmd/memremarkd/main.go`
- Test: `cmd/memremarkd/main_test.go`
- Test: `internal/daemon/daemon_summarize_test.go`

**Interfaces:**
- Passes `InvokerOptions{SessionID: storage.WingSummarySessionID(wing.Path), WorkDir: wing.Path}` to `summarizer.SummarizeWithOptions`.
- `resolveInvokers` respects `cfg.Summarizer.Provider` (`"antigravity"`, `"claude"`, `"auto"`).

- [ ] **Step 1: Write failing tests for provider routing and daemon scoping**

Create `cmd/memremarkd/main_test.go`:

```go
package main

import (
	"testing"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/summarizer"
)

func mockLookPathAll(cmd string) (string, error) {
	return "/bin/" + cmd, nil
}

func TestResolveInvokers_ProviderPreference(t *testing.T) {
	cfg := config.DefaultConfig()

	// Provider: "antigravity"
	cfg.Summarizer.Provider = "antigravity"
	setupAgy := resolveInvokers(cfg, mockLookPathAll)
	fallbackAgy, ok := setupAgy.ClaudeInvoker.(summarizer.FallbackInvoker)
	if !ok {
		t.Fatalf("expected FallbackInvoker for ClaudeInvoker under antigravity provider")
	}
	if _, isAgy := fallbackAgy.Primary.(summarizer.AntigravityInvoker); !isAgy {
		t.Fatalf("expected Primary to be AntigravityInvoker when provider=antigravity")
	}

	// Provider: "claude"
	cfg.Summarizer.Provider = "claude"
	setupClaude := resolveInvokers(cfg, mockLookPathAll)
	fallbackClaude, ok := setupClaude.AntigravityInvoker.(summarizer.FallbackInvoker)
	if !ok {
		t.Fatalf("expected FallbackInvoker for AntigravityInvoker under claude provider")
	}
	if _, isClaude := fallbackClaude.Primary.(summarizer.ClaudeCodeInvoker); !isClaude {
		t.Fatalf("expected Primary to be ClaudeCodeInvoker when provider=claude")
	}

	// Provider: "auto"
	cfg.Summarizer.Provider = "auto"
	setupAuto := resolveInvokers(cfg, mockLookPathAll)
	fbClaudeAuto := setupAuto.ClaudeInvoker.(summarizer.FallbackInvoker)
	if _, isClaude := fbClaudeAuto.Primary.(summarizer.ClaudeCodeInvoker); !isClaude {
		t.Fatalf("expected ClaudeInvoker.Primary to be ClaudeCodeInvoker under auto")
	}
	fbAgyAuto := setupAuto.AntigravityInvoker.(summarizer.FallbackInvoker)
	if _, isAgy := fbAgyAuto.Primary.(summarizer.AntigravityInvoker); !isAgy {
		t.Fatalf("expected AntigravityInvoker.Primary to be AntigravityInvoker under auto")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./cmd/memremarkd/... -v`
Expected: FAIL

- [ ] **Step 3: Implement provider routing in main.go and scoping in daemon_summarize.go**

1. In `cmd/memremarkd/main.go`:
Update `resolveInvokers` to handle `cfg.Summarizer.Provider == "antigravity"` and `cfg.Summarizer.Provider == "claude"`.
2. In `internal/daemon/daemon_summarize.go`:
Query `wing, err := d.Store.GetWingByID(wingID)` and pass:
```go
summarySessionID := storage.WingSummarySessionID(wing.Path)
opts := summarizer.InvokerOptions{
	SessionID: summarySessionID,
	WorkDir:   wing.Path,
}
items, err := summarizer.SummarizeWithOptions(ctx, invoker, obs, d.TargetLanguage, opts)
```

- [ ] **Step 4: Run all tests to verify passing**

Run: `go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cmd/memremarkd/ internal/daemon/
git commit -m "feat(daemon): wire provider preference and workspace scoped summary sessions"
```

---

### Task 6: Configure User Local Settings & Final Verification

**Files:**
- Modify: `~/.memremark/config.json`

- [ ] **Step 1: Set `"provider": "antigravity"` in `~/.memremark/config.json`**
- [ ] **Step 2: Run `go build ./... && go test ./...`**
- [ ] **Step 3: Verification summary**
