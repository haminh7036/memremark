# Summarizer Fallback Invoker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `FallbackInvoker` in `internal/summarizer` to automatically delegate summarization to a fallback CLI when the primary CLI fails (such as 429 quota exhaustion), wire it up in `cmd/memremarkd`, and verify with a comprehensive test suite.

**Architecture:** A decorator `FallbackInvoker` implementing `summarizer.Invoker` wraps a Primary and Fallback invoker. It checks context health, triggers an optional `OnFallback` callback, and wraps errors from both invokers when both fail.

**Tech Stack:** Go (Standard Library: `context`, `fmt`, `sync`, `time`, `testing`).

## Global Constraints

- No external dependencies outside Go stdlib.
- Thread-safe / stateless invocation.
- Must honor `context.Canceled` and `context.DeadlineExceeded` without invoking Fallback.
- 100% test coverage for all edge cases with race detector (`go test -race ./...`).

---

### Task 1: Implement `FallbackInvoker` and Unit Tests

**Files:**
- Modify: `internal/summarizer/summarizer.go`
- Modify: `internal/summarizer/summarizer_test.go`

**Interfaces:**
- Produces:
  ```go
  type FallbackInvoker struct {
      Primary    Invoker
      Fallback   Invoker
      OnFallback func(primaryErr error)
  }
  func (f FallbackInvoker) Invoke(ctx context.Context, prompt string) (string, error)
  ```

- [ ] **Step 1: Write the failing tests in `internal/summarizer/summarizer_test.go`**

Add unit tests covering all edge cases to `internal/summarizer/summarizer_test.go`:

```go
func TestFallbackInvoker_PrimarySuccess(t *testing.T) {
	primary := &stubInvoker{reply: `[{"hall":"fact","content":"a"}]`}
	fallback := &stubInvoker{err: errors.New("fallback should not be called")}
	fallbackCalled := false
	onFallback := func(err error) { fallbackCalled = true }

	invoker := FallbackInvoker{
		Primary:    primary,
		Fallback:   fallback,
		OnFallback: onFallback,
	}

	got, err := invoker.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[{"hall":"fact","content":"a"}]` {
		t.Fatalf("unexpected reply: %q", got)
	}
	if fallbackCalled {
		t.Fatalf("expected OnFallback not to be called")
	}
}

func TestFallbackInvoker_PrimaryFails_FallbackSucceeds(t *testing.T) {
	primary := &stubInvoker{err: errors.New("exit status 1: session limit")}
	fallback := &stubInvoker{reply: `[{"hall":"fact","content":"fallback"}]`}
	var capturedPrimaryErr error
	onFallback := func(err error) { capturedPrimaryErr = err }

	invoker := FallbackInvoker{
		Primary:    primary,
		Fallback:   fallback,
		OnFallback: onFallback,
	}

	got, err := invoker.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[{"hall":"fact","content":"fallback"}]` {
		t.Fatalf("unexpected reply: %q", got)
	}
	if capturedPrimaryErr == nil || !strings.Contains(capturedPrimaryErr.Error(), "session limit") {
		t.Fatalf("expected OnFallback to receive primary error, got: %v", capturedPrimaryErr)
	}
}

func TestFallbackInvoker_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	primary := &stubInvoker{err: errors.New("primary error")}
	fallback := &stubInvoker{err: errors.New("fallback should not be called")}

	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	_, err := invoker.Invoke(ctx, "test prompt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestFallbackInvoker_ContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond) // ensure deadline passed

	primary := &stubInvoker{err: errors.New("primary error")}
	fallback := &stubInvoker{err: errors.New("fallback should not be called")}

	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	_, err := invoker.Invoke(ctx, "test prompt")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestFallbackInvoker_BothFail_ReturnsCombinedError(t *testing.T) {
	primary := &stubInvoker{err: errors.New("quota limit 429")}
	fallback := &stubInvoker{err: errors.New("model timeout")}

	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	_, err := invoker.Invoke(context.Background(), "test prompt")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "quota limit 429") || !strings.Contains(err.Error(), "model timeout") {
		t.Fatalf("expected error to mention both failures, got: %v", err)
	}
}

func TestFallbackInvoker_NilPrimary(t *testing.T) {
	fallback := &stubInvoker{reply: `[{"hall":"fact","content":"from fallback"}]`}
	invoker := FallbackInvoker{
		Primary:  nil,
		Fallback: fallback,
	}

	got, err := invoker.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[{"hall":"fact","content":"from fallback"}]` {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestFallbackInvoker_NilFallback(t *testing.T) {
	primary := &stubInvoker{err: errors.New("primary failed")}
	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: nil,
	}

	_, err := invoker.Invoke(context.Background(), "test prompt")
	if err == nil || !strings.Contains(err.Error(), "primary failed") {
		t.Fatalf("expected primary error, got: %v", err)
	}
}

func TestFallbackInvoker_BothNil(t *testing.T) {
	invoker := FallbackInvoker{
		Primary:  nil,
		Fallback: nil,
	}

	_, err := invoker.Invoke(context.Background(), "test prompt")
	if err == nil || !strings.Contains(err.Error(), "no invokers configured") {
		t.Fatalf("expected error about no invokers configured, got: %v", err)
	}
}

func TestFallbackInvoker_ConcurrentRace(t *testing.T) {
	primary := &stubInvoker{err: errors.New("fail")}
	fallback := &stubInvoker{reply: `[]`}
	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := invoker.Invoke(context.Background(), "test")
			if err != nil {
				t.Errorf("concurrent invocation failed: %v", err)
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/summarizer/...`
Expected: FAIL (compilation error: `undefined: FallbackInvoker`)

- [ ] **Step 3: Implement `FallbackInvoker` in `internal/summarizer/summarizer.go`**

Add `FallbackInvoker` definition and method:

```go
// FallbackInvoker wraps a Primary and a Fallback invoker.
// If Primary fails, it automatically delegates to Fallback unless the context
// was canceled or timed out.
type FallbackInvoker struct {
	Primary    Invoker
	Fallback   Invoker
	OnFallback func(primaryErr error)
}

// Invoke implements Invoker with automatic fallback.
func (f FallbackInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	if f.Primary == nil && f.Fallback == nil {
		return "", fmt.Errorf("summarizer: no invokers configured in FallbackInvoker")
	}
	if f.Primary == nil {
		return f.Fallback.Invoke(ctx, prompt)
	}

	res, err := f.Primary.Invoke(ctx, prompt)
	if err == nil {
		return res, nil
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if f.Fallback == nil {
		return "", err
	}

	if f.OnFallback != nil {
		f.OnFallback(err)
	}

	fallbackRes, fallbackErr := f.Fallback.Invoke(ctx, prompt)
	if fallbackErr != nil {
		return "", fmt.Errorf("summarizer: primary failed (%w); fallback failed (%w)", err, fallbackErr)
	}

	return fallbackRes, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/summarizer/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/summarizer.go internal/summarizer/summarizer_test.go
git commit -m "feat(summarizer): implement FallbackInvoker with comprehensive edge case tests"
```

---

### Task 2: Add Integration Test in Daemon

**Files:**
- Modify: `internal/daemon/daemon_summarize_test.go`

**Interfaces:**
- Consumes: `summarizer.FallbackInvoker`

- [ ] **Step 1: Write integration test in `internal/daemon/daemon_summarize_test.go`**

Add `TestDaemon_SummarizeSession_FallbackIntegration`:

```go
func TestDaemon_SummarizeSession_FallbackIntegration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	primary := &stubInvoker{err: errors.New("claude -p 429 quota exceeded")}
	fallback := &stubInvoker{reply: `[{"hall":"fact","content":"fallback worked"}]`}
	fallbackTriggered := false

	invoker := summarizer.FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
		OnFallback: func(err error) {
			fallbackTriggered = true
		},
	}

	d := New(store, t.TempDir(), filepath.Join(t.TempDir(), "antigravity.db"), invoker, invoker)

	now := time.Now()
	obs := observation.Observation{
		SessionID: "session-fallback-test",
		WingPath:  "/test/ws",
		ToolName:  "ViewFile",
		Content:   "viewed file",
		Timestamp: now,
	}

	if err := d.recordObservation(obs, invoker, now); err != nil {
		t.Fatalf("recordObservation: %v", err)
	}

	if err := d.summarizeSession(context.Background(), obs.SessionID, now); err != nil {
		t.Fatalf("summarizeSession: %v", err)
	}

	if !fallbackTriggered {
		t.Fatalf("expected fallback callback to have triggered")
	}

	wingID, err := store.GetOrCreateWing("/test/ws")
	if err != nil {
		t.Fatalf("GetOrCreateWing: %v", err)
	}

	drawers, err := store.ListSummaryDrawers(wingID, "fact")
	if err != nil {
		t.Fatalf("ListSummaryDrawers: %v", err)
	}
	if len(drawers) != 1 || drawers[0].Content != "fallback worked" {
		t.Fatalf("expected 1 drawer with content 'fallback worked', got: %+v", drawers)
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -v -run TestDaemon_SummarizeSession_FallbackIntegration ./internal/daemon/...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/daemon/daemon_summarize_test.go
git commit -m "test(daemon): add integration test for FallbackInvoker summarization"
```

---

### Task 3: Wire `FallbackInvoker` in `cmd/memremarkd` and Verify End-to-End

**Files:**
- Modify: `cmd/memremarkd/main.go`

- [ ] **Step 1: Update `cmd/memremarkd/main.go`**

Wrap Claude and Antigravity invokers with `summarizer.FallbackInvoker`:

```go
	claudeInvoker := summarizer.FallbackInvoker{
		Primary:  summarizer.ClaudeCodeInvoker{},
		Fallback: summarizer.AntigravityInvoker{},
		OnFallback: func(err error) {
			log.Printf("memremarkd: claude summarizer failed (%v), falling back to antigravity", err)
		},
	}

	antigravityInvoker := summarizer.FallbackInvoker{
		Primary:  summarizer.AntigravityInvoker{},
		Fallback: summarizer.ClaudeCodeInvoker{},
		OnFallback: func(err error) {
			log.Printf("memremarkd: antigravity summarizer failed (%v), falling back to claude", err)
		},
	}

	d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB,
		claudeInvoker, antigravityInvoker)
```

- [ ] **Step 2: Run entire test suite with race detector**

Run: `go test -race ./...`
Expected: PASS across all packages.

- [ ] **Step 3: Build binaries**

Run: `go build -o /tmp/memremarkd ./cmd/memremarkd`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git add cmd/memremarkd/main.go
git commit -m "feat(daemon): wire bidirectional FallbackInvoker in memremarkd"
```
