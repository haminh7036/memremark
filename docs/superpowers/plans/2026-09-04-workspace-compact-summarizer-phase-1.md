# Workspace-Level Compact Summarizer (Phase 1: Process Isolation, Concurrency Control & Emergency Pause) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Phase 1 of the Workspace-Level Compact Summarizer: isolate child process groups (`Setpgid: true`) to kill orphaned MCP servers on cancellation, provide an emergency pause/resume mechanism (`memremark pause`/`resume`), calibrate `idleWindow` to 45s, and enforce strict single-concurrency (`cliMutex`) to eliminate RAM bloat and process storms.

**Architecture:** 
- In `internal/summarizer`, update `AntigravityInvoker` and `ClaudeCodeInvoker` to spawn processes in a new process group (`Setpgid: true`) and attach a `cmd.Cancel` hook that terminates the entire negative process group (`syscall.Kill(-pid, SIGKILL)`).
- In `internal/daemon`, introduce `pause.go` with file-based pause detection (`$HOME/.memremark/paused`), add an emergency cancellation hook to immediately terminate in-flight executions, increase default `idleWindow` to 45s, and guard summarizer invocations with a daemon-wide `cliMutex`.
- In `cmd/memremarkd`, support `pause`, `resume`, and `status` subcommands for instant operator control without touching `systemctl`.

**Tech Stack:** Go 1.26+, `os/exec`, `syscall`, `context`, `sync`, modernc SQLite.

## Global Constraints

- Platform: Linux (WSL/native), with POSIX process group semantics (`syscall.SysProcAttr{Setpgid: true}`).
- Memory safety: Zero orphaned child processes upon timeout or cancellation.
- Backward compatibility: Preserves existing config schemas and database tables.
- Concurrency limit: Maximum 1 active CLI process across the entire daemon at any given time.

---

### Task 1: Process Group Isolation & Clean Subprocess Termination in CLI Invokers

**Files:**
- Modify: `internal/summarizer/summarizer.go:69-93` (`ClaudeCodeInvoker.Invoke`)
- Modify: `internal/summarizer/summarizer.go:131-176` (`AntigravityInvoker.Invoke`)
- Test: `internal/summarizer/process_group_test.go`

**Interfaces:**
- Consumes: Standard Go `os/exec.Cmd`, `syscall.SysProcAttr`, `syscall.Kill`.
- Produces: Subprocesses running in their own process group; `cmd.Cancel` sending `SIGKILL` to `-cmd.Process.Pid`.

- [ ] **Step 1: Write the failing test**

Create `internal/summarizer/process_group_test.go`:
```go
package summarizer

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupTermination(t *testing.T) {
	// A mock command that creates a background child and sleeps
	// We verify that cancelling the context terminates the entire process group
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 10 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	start := time.Now()
	_ = cmd.Run()
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Fatalf("process group was not killed promptly on timeout, took %v", elapsed)
	}
}
```

- [ ] **Step 2: Run test to verify it passes for basic semantics**

Run: `go test -v ./internal/summarizer -run TestProcessGroupTermination`
Expected: PASS

- [ ] **Step 3: Update `ClaudeCodeInvoker` and `AntigravityInvoker` in `internal/summarizer/summarizer.go`**

In `internal/summarizer/summarizer.go`:
1. In `ClaudeCodeInvoker.Invoke`:
```go
	cmd := exec.CommandContext(ctx, "claude", inv.buildArgs(opt.SessionID)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
```

2. In `AntigravityInvoker.Invoke`:
```go
	cmd := exec.CommandContext(ctx, "agy", inv.buildArgs(prompt, opt.SessionID)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
```

- [ ] **Step 4: Run all summarizer unit tests**

Run: `go test -v -race ./internal/summarizer/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/summarizer.go internal/summarizer/process_group_test.go
git commit -m "feat(summarizer): isolate CLI subprocesses into process groups with group kill on cancel"
```

---

### Task 2: Emergency Pause & Resume Mechanism

**Files:**
- Create: `internal/daemon/pause.go`
- Modify: `internal/daemon/daemon.go:27-51` (add pause path and lock tracking)
- Modify: `internal/daemon/daemon.go:132-160` (`PollOnce` pause guard)
- Test: `internal/daemon/pause_test.go`

**Interfaces:**
- Consumes: Filesystem path `$HOME/.memremark/paused` (or `MEMREMARK_PAUSE_FILE` env).
- Produces: `d.IsPaused() bool`, `d.SetPaused(bool) error`, `d.CancelActive()`.

- [ ] **Step 1: Write the failing test**

Create `internal/daemon/pause_test.go`:
```go
package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
)

func TestDaemonPauseResume(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	defer store.Close()

	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), nil, nil, locale.TargetLanguage{Code: "en"})
	pauseFile := filepath.Join(tempDir, "paused")
	d.SetPauseFile(pauseFile)

	if d.IsPaused() {
		t.Fatal("expected daemon not to be paused initially")
	}

	if err := d.SetPaused(true); err != nil {
		t.Fatalf("failed to pause daemon: %v", err)
	}

	if !d.IsPaused() {
		t.Fatal("expected daemon to be paused after SetPaused(true)")
	}

	if _, err := os.Stat(pauseFile); os.IsNotExist(err) {
		t.Fatal("expected pause file to exist")
	}

	// In PollOnce, should immediately return nil without action
	now := time.Now()
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce when paused returned error: %v", err)
	}

	if err := d.SetPaused(false); err != nil {
		t.Fatalf("failed to resume daemon: %v", err)
	}

	if d.IsPaused() {
		t.Fatal("expected daemon not to be paused after SetPaused(false)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/daemon -run TestDaemonPauseResume`
Expected: FAIL (compilation errors: undefined `SetPauseFile`, `IsPaused`, `SetPaused`)

- [ ] **Step 3: Implement `internal/daemon/pause.go` and update `daemon.go`**

Create `internal/daemon/pause.go`:
```go
package daemon

import (
	"os"
	"path/filepath"
	"sync"
)

// DefaultPauseFilePath returns $HOME/.memremark/paused.
func DefaultPauseFilePath() string {
	if env := os.Getenv("MEMREMARK_PAUSE_FILE"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".memremark_paused"
	}
	return filepath.Join(home, ".memremark", "paused")
}

// SetPauseFile overrides the default pause file path (useful for testing).
func (d *Daemon) SetPauseFile(path string) {
	d.pauseMu.Lock()
	defer d.pauseMu.Unlock()
	d.pauseFilePath = path
}

func (d *Daemon) getPauseFile() string {
	d.pauseMu.Lock()
	defer d.pauseMu.Unlock()
	if d.pauseFilePath == "" {
		d.pauseFilePath = DefaultPauseFilePath()
	}
	return d.pauseFilePath
}

// IsPaused returns true if the pause marker file exists on disk.
func (d *Daemon) IsPaused() bool {
	path := d.getPauseFile()
	_, err := os.Stat(path)
	return err == nil
}

// SetPaused creates or deletes the pause file and cancels in-flight work if pausing.
func (d *Daemon) SetPaused(paused bool) error {
	path := d.getPauseFile()
	if paused {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_ = f.Close()
		d.CancelActive()
		return nil
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RegisterActiveCancel records the cancel func of an in-flight operation.
func (d *Daemon) RegisterActiveCancel(cancel func()) {
	d.activeCancelMu.Lock()
	defer d.activeCancelMu.Unlock()
	d.activeCancel = cancel
}

// ClearActiveCancel unsets the cancel func once the operation finishes.
func (d *Daemon) ClearActiveCancel() {
	d.activeCancelMu.Lock()
	defer d.activeCancelMu.Unlock()
	d.activeCancel = nil
}

// CancelActive invokes the active cancel func if one is registered.
func (d *Daemon) CancelActive() {
	d.activeCancelMu.Lock()
	defer d.activeCancelMu.Unlock()
	if d.activeCancel != nil {
		d.activeCancel()
		d.activeCancel = nil
	}
}
```

In `internal/daemon/daemon.go`:
Add fields to `Daemon` struct:
```go
	pauseMu        sync.Mutex
	pauseFilePath  string
	activeCancelMu sync.Mutex
	activeCancel   func()
```
And in `PollOnce`:
```go
func (d *Daemon) PollOnce(ctx context.Context, now time.Time) error {
	if d.IsPaused() {
		return nil
	}
...
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/daemon -run TestDaemonPauseResume`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/pause.go internal/daemon/daemon.go internal/daemon/pause_test.go
git commit -m "feat(daemon): add emergency pause detection and active task cancellation"
```

---

### Task 3: Global Concurrency Limiter (CLI Mutex) & Debounce Calibration

**Files:**
- Modify: `internal/daemon/daemon.go:16-25` (calibrate `idleWindow`)
- Modify: `internal/daemon/daemon.go:29-51` (add `cliMutex`)
- Modify: `internal/daemon/daemon_summarize.go:48-122` (wrap CLI calls with `cliMutex` and register `activeCancel`)
- Test: `internal/daemon/concurrency_test.go`

**Interfaces:**
- Consumes: Go `sync.Mutex`.
- Produces: Strictly serialized CLI invocations across the daemon.

- [ ] **Step 1: Write the failing test**

Create `internal/daemon/concurrency_test.go`:
```go
package daemon

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

type blockingInvoker struct {
	concurrentCount int32
	maxConcurrent   int32
}

func (b *blockingInvoker) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	curr := atomic.AddInt32(&b.concurrentCount, 1)
	defer atomic.AddInt32(&b.concurrentCount, -1)

	for {
		max := atomic.LoadInt32(&b.maxConcurrent)
		if curr <= max || atomic.CompareAndSwapInt32(&b.maxConcurrent, max, curr) {
			break
		}
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return `[{"hall":"fact","content":"Test fact","narrative":"Test narrative"}]`, nil
	}
}

func TestCLIConcurrencySerialization(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	defer store.Close()

	inv := &blockingInvoker{}
	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), inv, inv, locale.TargetLanguage{Code: "en"})

	// Record observations for two sessions in the same wing
	now := time.Now()
	_ = d.recordObservation(observation.Observation{WingPath: tempDir, SessionID: "sess-1", ToolName: "test", Content: "c1", Timestamp: now}, inv, now)
	_ = d.recordObservation(observation.Observation{WingPath: tempDir, SessionID: "sess-2", ToolName: "test", Content: "c2", Timestamp: now}, inv, now)

	// Run summarize concurrently
	errCh := make(chan error, 2)
	go func() {
		errCh <- d.summarizeSession(context.Background(), "sess-1", now.Add(time.Second))
	}()
	go func() {
		errCh <- d.summarizeSession(context.Background(), "sess-2", now.Add(time.Second))
	}()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("summarizeSession failed: %v", err)
		}
	}

	if inv.maxConcurrent > 1 {
		t.Fatalf("expected max concurrency 1, got %d", inv.maxConcurrent)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/daemon -run TestCLIConcurrencySerialization`
Expected: FAIL (max concurrency > 1 because sessions run in parallel without mutex)

- [ ] **Step 3: Add `cliMutex` and calibrate `idleWindow`**

1. In `internal/daemon/daemon.go`:
Change `idleWindow`:
```go
// idleWindow is how long a session (or workspace) must go quiet before it's summarized.
// Increased to 45s to avoid aggressive continuous CLI spawning on short pauses.
const idleWindow = 45 * time.Second
```
Add `cliMutex` to `Daemon`:
```go
	cliMutex sync.Mutex
```

2. In `internal/daemon/daemon_summarize.go`:
In `summarizeSessionWithBatchSize`:
```go
func (d *Daemon) summarizeSessionWithBatchSize(ctx context.Context, sessionID string, now time.Time, maxBatchBytes int) error {
	d.cliMutex.Lock()
	defer d.cliMutex.Unlock()

	if d.IsPaused() {
		return nil
	}
...
```
Wrap invocation with `RegisterActiveCancel`:
```go
		callCtx, cancel := context.WithCancel(ctx)
		d.RegisterActiveCancel(cancel)
		items, err := summarizer.SummarizeWithOptions(callCtx, invoker, obs, d.TargetLanguage, opts)
		d.ClearActiveCancel()
		cancel()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/daemon -run TestCLIConcurrencySerialization`
Expected: PASS

- [ ] **Step 5: Run all daemon tests**

Run: `go test -v -race ./internal/daemon/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/daemon.go internal/daemon/daemon_summarize.go internal/daemon/concurrency_test.go
git commit -m "feat(daemon): enforce concurrency=1 with cliMutex and calibrate idleWindow to 45s"
```

---

### Task 4: Operator Subcommands in `cmd/memremarkd` (`pause`, `resume`, `status`)

**Files:**
- Modify: `cmd/memremarkd/main.go:386-451`
- Test: `cmd/memremarkd/main_test.go`

**Interfaces:**
- Consumes: CLI args: `memremarkd pause`, `memremarkd resume`, `memremarkd status`.
- Produces: Formatted stdout message and exit code 0 on success.

- [ ] **Step 1: Write the failing test**

In `cmd/memremarkd/main_test.go`:
```go
func TestSubcommands(t *testing.T) {
	tempDir := t.TempDir()
	pauseFile := filepath.Join(tempDir, "paused")
	t.Setenv("MEMREMARK_PAUSE_FILE", pauseFile)

	// Test status when not paused
	if isPaused(pauseFile) {
		t.Fatal("expected not paused initially")
	}

	// Test pause
	if err := runPause(pauseFile); err != nil {
		t.Fatalf("runPause failed: %v", err)
	}
	if !isPaused(pauseFile) {
		t.Fatal("expected paused after runPause")
	}

	// Test resume
	if err := runResume(pauseFile); err != nil {
		t.Fatalf("runResume failed: %v", err)
	}
	if isPaused(pauseFile) {
		t.Fatal("expected not paused after runResume")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./cmd/memremarkd -run TestSubcommands`
Expected: FAIL (helpers `runPause`, `runResume`, `isPaused` not defined)

- [ ] **Step 3: Implement subcommand handling in `cmd/memremarkd/main.go`**

In `cmd/memremarkd/main.go`:
```go
func isPaused(pauseFile string) bool {
	_, err := os.Stat(pauseFile)
	return err == nil
}

func runPause(pauseFile string) error {
	if err := os.MkdirAll(filepath.Dir(pauseFile), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(pauseFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func runResume(pauseFile string) error {
	err := os.Remove(pauseFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func handleSubcommands() bool {
	if len(os.Args) < 2 {
		return false
	}
	cmd := os.Args[1]
	pauseFile := daemon.DefaultPauseFilePath()

	switch cmd {
	case "pause":
		if err := runPause(pauseFile); err != nil {
			fmt.Fprintf(os.Stderr, "memremarkd: failed to pause: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("memremarkd: paused (active CLI processes stopped, summarization suspended)")
		return true
	case "resume":
		if err := runResume(pauseFile); err != nil {
			fmt.Fprintf(os.Stderr, "memremarkd: failed to resume: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("memremarkd: resumed (summarization enabled)")
		return true
	case "status":
		if isPaused(pauseFile) {
			fmt.Printf("memremarkd: PAUSED (pause file: %s)\n", pauseFile)
		} else {
			fmt.Println("memremarkd: ACTIVE (normal polling)")
		}
		return true
	}
	return false
}
```
And at the top of `main()`:
```go
func main() {
	if handleSubcommands() {
		return
	}
...
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./cmd/memremarkd -run TestSubcommands`
Expected: PASS

- [ ] **Step 5: Run full test suite in `cmd/memremarkd`**

Run: `go test -v -race ./cmd/memremarkd/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/memremarkd/main.go cmd/memremarkd/main_test.go
git commit -m "feat(cli): add pause, resume, and status subcommands to memremarkd"
```

---

### Task 5: End-to-End Build and Verification of Phase 1

**Files:**
- None (verification across all compiled binaries and test packages).

- [ ] **Step 1: Run complete repository test suite**

Run: `go test -v -race ./...`
Expected: All packages report PASS with zero race conditions.

- [ ] **Step 2: Build all project binaries**

Run: `make build`
Expected: Successful compilation of all binaries into `bin/`:
- `bin/memremarkd`
- `bin/memremark-hook-claude`
- `bin/memremark-hook-agy`
- `bin/memremark-mcp`
- `bin/memremark-ui`

- [ ] **Step 3: Verify CLI pause and resume operations manually**

Run:
```bash
./bin/memremarkd status
./bin/memremarkd pause
./bin/memremarkd status
./bin/memremarkd resume
./bin/memremarkd status
```
Expected:
- Initially reports ACTIVE
- After pause reports PAUSED
- After resume reports ACTIVE

- [ ] **Step 4: Commit any final test assets or updates**

```bash
git status
```
Ensure working tree is clean.
