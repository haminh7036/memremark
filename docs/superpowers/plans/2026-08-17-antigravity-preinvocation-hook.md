# Antigravity CLI PreInvocation Hook — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Inject memremark summaries into new Antigravity CLI sessions via a `PreInvocation` hook binary.

**Architecture:** A standalone Go binary receives the `PreInvocation` stdin payload, checks `invocationNum == 0`, queries the existing memremark SQLite store for recent summaries scoped to `workspacePaths[0]`, and returns an `injectSteps` JSON response with an `ephemeralMessage`. Mirrors the existing Claude Code `SessionStart` hook pattern.

**Tech Stack:** Go, `modernc.org/sqlite` (already in go.mod), Antigravity CLI `PreInvocation` hook contract.

## Global Constraints

- No new dependencies — only `modernc.org/sqlite` (already present) and stdlib.
- Hook binary must never fail with a non-zero exit code — errors go to stderr, stdout always gets valid JSON.
- Hook must be fast: when `invocationNum != 0`, exit immediately without opening the DB.
- Follow existing patterns from `cmd/memremark-hook-claude-sessionstart/`.

---

## File Structure

| Action | Path | Responsibility |
|---|---|---|
| Create | `cmd/memremark-hook-antigravity-preinvocation/main.go` | Hook binary entry point — parse stdin, query store, format output |
| Create | `cmd/memremark-hook-antigravity-preinvocation/main_test.go` | Unit tests — all input/output scenarios |
| Modify | `README.md` | Add Antigravity hook install instructions |

---

### Task 1: Hook binary + tests

**Files:**
- Create: `cmd/memremark-hook-antigravity-preinvocation/main.go`
- Create: `cmd/memremark-hook-antigravity-preinvocation/main_test.go`
- Reference: `cmd/memremark-hook-claude-sessionstart/main.go` (pattern to follow)
- Reference: `internal/storage/` (Store, GetOrCreateWing, RecentSummaries, Drawer)

**Interfaces:**
- Consumes: `storage.Open`, `storage.Store.GetOrCreateWing`, `storage.Store.RecentSummaries`, `storage.Drawer` — all existing, unchanged.
- Produces: standalone binary, no exported Go API.

- [ ] **Step 1: Write the test file with all scenarios**

```go
// cmd/memremark-hook-antigravity-preinvocation/main_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestGetSummaries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing("/workspace/project")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	coversFrom := now.Add(-time.Hour)
	coversTo := now.Add(-time.Minute)

	if err := store.InsertSummaryDrawer(wingID, "session-1", "fact", "Go 1.26 is the minimum version", coversFrom, coversTo, now); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSummaryDrawer(wingID, "session-1", "discovery", "SQLite WAL mode is needed for concurrent reads", coversFrom, coversTo, now); err != nil {
		t.Fatal(err)
	}

	summaries, err := getSummaries("/workspace/project", filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
}

func TestGetSummaries_NoSummaries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	summaries, err := getSummaries("/workspace/project", filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestBuildOutput_WithSummaries(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: "fact", Content: "minimum Go version is 1.26"},
		{Hall: "discovery", Content: "WAL mode needed"},
	}
	out := buildOutput(summaries)
	if len(out.InjectSteps) != 1 {
		t.Fatalf("expected 1 inject step, got %d", len(out.InjectSteps))
	}
	if out.InjectSteps[0].EphemeralMessage == "" {
		t.Fatal("expected non-empty ephemeral message")
	}
}

func TestBuildOutput_Empty(t *testing.T) {
	out := buildOutput(nil)
	if out.InjectSteps != nil {
		t.Fatalf("expected nil InjectSteps, got %v", out.InjectSteps)
	}

	b, _ := json.Marshal(out)
	if string(b) != "{}" {
		t.Fatalf("expected {}, got %s", string(b))
	}
}

func TestParseInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNum int
		wantWS  string
		wantErr bool
	}{
		{
			name:    "valid first invocation",
			input:   `{"invocationNum":0,"workspacePaths":["/workspace/project"]}`,
			wantNum: 0,
			wantWS:  "/workspace/project",
		},
		{
			name:    "later invocation",
			input:   `{"invocationNum":3,"workspacePaths":["/workspace/project"]}`,
			wantNum: 3,
			wantWS:  "/workspace/project",
		},
		{
			name:    "empty workspace paths",
			input:   `{"invocationNum":0,"workspacePaths":[]}`,
			wantErr: true,
		},
		{
			name:    "malformed json",
			input:   `{invalid`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := parseInput([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if in.InvocationNum != tt.wantNum {
				t.Fatalf("invocationNum: want %d, got %d", tt.wantNum, in.InvocationNum)
			}
			if in.WorkspacePaths[0] != tt.wantWS {
				t.Fatalf("workspacePaths[0]: want %s, got %s", tt.wantWS, in.WorkspacePaths[0])
			}
		})
	}
}

func TestFormatSummaries(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: "fact", Content: "Go 1.26 minimum"},
		{Hall: "advice", Content: "Always run tests before commit"},
	}
	got := formatSummaries(summaries)
	expected := "Bối cảnh từ các phiên làm việc trước (memremark):\n- [fact] Go 1.26 minimum\n- [advice] Always run tests before commit\n"
	if got != expected {
		t.Fatalf("formatSummaries mismatch:\ngot:  %q\nwant: %q", got, expected)
	}
}

func TestRun_SkipsNonZeroInvocation(t *testing.T) {
	// run() with invocationNum > 0 should return nil summaries without touching DB
	old := os.Stdin
	defer func() { os.Stdin = old }()

	r, w, _ := os.Pipe()
	w.Write([]byte(`{"invocationNum":5,"workspacePaths":["/tmp/nonexistent"]}`))
	w.Close()
	os.Stdin = r

	summaries, err := run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summaries != nil {
		t.Fatalf("expected nil summaries for invocationNum > 0, got %v", summaries)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/memremark-hook-antigravity-preinvocation/ -v -count=1`
Expected: compilation error — `main.go` doesn't exist yet.

- [ ] **Step 3: Write the hook binary**

```go
// cmd/memremark-hook-antigravity-preinvocation/main.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/haminh7036/memremark/internal/storage"
)

type hookInput struct {
	InvocationNum  int      `json:"invocationNum"`
	WorkspacePaths []string `json:"workspacePaths"`
}

type hookOutput struct {
	InjectSteps []injectStep `json:"injectSteps,omitempty"`
}

type injectStep struct {
	EphemeralMessage string `json:"ephemeralMessage,omitempty"`
}

func main() {
	summaries, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-antigravity-preinvocation:", err)
	}
	if summaries == nil {
		summaries = []storage.Drawer{}
	}
	if err := json.NewEncoder(os.Stdout).Encode(buildOutput(summaries)); err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-antigravity-preinvocation: failed to encode output:", err)
	}
	os.Exit(0)
}

func parseInput(data []byte) (hookInput, error) {
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return hookInput{}, fmt.Errorf("parse stdin: %w", err)
	}
	if len(in.WorkspacePaths) == 0 {
		return hookInput{}, fmt.Errorf("workspacePaths is empty")
	}
	return in, nil
}

func run() ([]storage.Drawer, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	in, err := parseInput(data)
	if err != nil {
		return nil, err
	}
	// Fast path: only inject context at session start (first model call).
	if in.InvocationNum != 0 {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return getSummaries(in.WorkspacePaths[0], home)
}

func getSummaries(workspacePath, home string) ([]storage.Drawer, error) {
	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		return nil, err
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing(workspacePath)
	if err != nil {
		return nil, err
	}
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

func buildOutput(summaries []storage.Drawer) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		InjectSteps: []injectStep{
			{EphemeralMessage: formatSummaries(summaries)},
		},
	}
}

func formatSummaries(summaries []storage.Drawer) string {
	out := "Bối cảnh từ các phiên làm việc trước (memremark):\n"
	for _, d := range summaries {
		out += fmt.Sprintf("- [%s] %s\n", d.Hall, d.Content)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/memremark-hook-antigravity-preinvocation/ -v -count=1`
Expected: all 7 tests PASS.

- [ ] **Step 5: Run the full test suite to check for regressions**

Run: `go test ./... -count=1`
Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/memremark-hook-antigravity-preinvocation/
git commit -m "feat: add Antigravity CLI PreInvocation hook for context injection"
```

---

### Task 2: README update

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: Task 1 binary exists.
- Produces: user-facing install instructions.

- [ ] **Step 1: Add Antigravity hook section to README**

Add a section under the existing Claude Code hook docs explaining:
- Build: `go build ./cmd/memremark-hook-antigravity-preinvocation/`
- Install: create/update `.agents/hooks.json` (or `~/.gemini/config/hooks.json`) with the PreInvocation config:

```json
{
  "memremark": {
    "PreInvocation": [
      {
        "type": "command",
        "command": "/absolute/path/to/memremark-hook-antigravity-preinvocation",
        "timeout": 5
      }
    ]
  }
}
```

- What it does: injects recent summaries as an ephemeral message at the start of each new Antigravity CLI session.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add Antigravity CLI hook install instructions to README"
```
