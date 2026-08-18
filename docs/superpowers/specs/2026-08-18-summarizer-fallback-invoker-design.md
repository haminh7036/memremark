# Summarizer Fallback Invoker Design

## 1. Problem Statement & Context

In `memremark`, background sessions are summarized by shelling out to a CLI's headless mode via the `summarizer.Invoker` interface (`Invoke(ctx context.Context, prompt string) (string, error)`):
- Claude Code transcript sessions invoke `summarizer.ClaudeCodeInvoker` (`claude -p --output-format json`).
- Antigravity transcript sessions invoke `summarizer.AntigravityInvoker` (`agy -p <prompt> --output-format json`).

When a CLI runs out of session quota (e.g. Claude Code hits rate limit HTTP 429: `"You've hit your session limit"`, exiting with status 1) or fails due to network/transient errors, the daemon repeatedly retries summarization and logs errors on every poll tick without making progress until the quota resets.

Since multiple LLM CLIs (`claude`, `agy`) are available on the user's system and both conform to the `summarizer.Invoker` interface and prompt distillation format, `memremark` can automatically delegate summarization to the secondary CLI when the primary CLI fails.

---

## 2. Goals & Non-Goals

### Goals
- **Automatic Fallback**: Transparently delegate summarization to a fallback CLI when the primary CLI fails.
- **Bi-directional Support**: Claude Code sessions fall back to Antigravity CLI; Antigravity sessions fall back to Claude Code CLI.
- **Context-Aware Safety**: If failure is caused by context cancellation or deadline timeout (`context.Canceled`, `context.DeadlineExceeded`), do not call the fallback invoker — return the context error immediately.
- **Comprehensive Observability**: Combine error messages if both fail; trigger an optional callback (`OnFallback`) when fallback occurs for logging/monitoring.
- **Edge-Case Resilience**: Safely handle nil invokers, concurrent invocations, and error chains.

### Non-Goals
- Complex multi-provider load balancing or round-robin algorithms (simple Primary -> Fallback decorator is sufficient).
- Modifying the core `summarizer.Invoker` interface signature or `daemon.Daemon` polling architecture.

---

## 3. Architecture & Components

```
                    +------------------------------------+
                    |        summarizer.Invoker          |
                    +-----------------+------------------+
                                      |
                                      | implements
                                      v
                    +------------------------------------+
                    |      FallbackInvoker (Decorator)   |
                    +-----------------+------------------+
                                      |
               +----------------------+----------------------+
               |                                             |
               v (1. Try first)                              v (2. On primary error)
   +-----------------------+                     +-----------------------+
   |   Primary: Invoker    |                     |   Fallback: Invoker   |
   | (e.g. ClaudeCode)     |                     | (e.g. Antigravity)    |
   +-----------------------+                     +-----------------------+
```

### Component Definition (`internal/summarizer/summarizer.go`)

```go
// FallbackInvoker wraps a Primary and a Fallback invoker.
// If Primary fails, it automatically delegates to Fallback unless the context
// was canceled or timed out.
type FallbackInvoker struct {
	Primary    Invoker
	Fallback   Invoker
	OnFallback func(primaryErr error)
}
```

### Execution Flow in `Invoke(ctx, prompt)`

1. **Nil Checks**:
   - If both `Primary == nil` and `Fallback == nil`, return error: `summarizer: no invokers configured in FallbackInvoker`.
   - If `Primary == nil`, delegate directly to `Fallback.Invoke(ctx, prompt)`.
2. **Primary Invocation**:
   - Call `res, err := Primary.Invoke(ctx, prompt)`.
   - If `err == nil`, return `(res, nil)`.
3. **Context / Cancellation Check**:
   - If `ctx.Err() != nil`, return `("", ctx.Err())` immediately without invoking Fallback.
4. **Fallback Availability**:
   - If `Fallback == nil`, return `("", err)` (Primary's error).
5. **Fallback Execution**:
   - If `OnFallback != nil`, call `OnFallback(err)`.
   - Call `fallbackRes, fallbackErr := Fallback.Invoke(ctx, prompt)`.
   - If `fallbackErr != nil`, return `("", fmt.Errorf("summarizer: primary failed (%w); fallback failed (%w)", err, fallbackErr))`.
   - Return `(fallbackRes, nil)`.

---

## 4. Integration into Daemon (`cmd/memremarkd/main.go`)

In `cmd/memremarkd/main.go`, construct the invokers passed to `daemon.New`:

```go
claudeInvoker := summarizer.FallbackInvoker{
	Primary:  summarizer.ClaudeCodeInvoker{},
	Fallback: summarizer.AntigravityInvoker{},
	OnFallback: func(err error) {
		log.Printf("daemon: claude summarizer failed (%v), falling back to antigravity", err)
	},
}

antigravityInvoker := summarizer.FallbackInvoker{
	Primary:  summarizer.AntigravityInvoker{},
	Fallback: summarizer.ClaudeCodeInvoker{},
	OnFallback: func(err error) {
		log.Printf("daemon: antigravity summarizer failed (%v), falling back to claude", err)
	},
}

d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB, claudeInvoker, antigravityInvoker)
```

---

## 5. Edge Cases & Handling Strategy

1. **Context Cancellation / Timeout during Primary**:
   - Condition: Context deadline fires or is canceled during `Primary.Invoke`.
   - Handling: `ctx.Err() != nil` check prevents calling `Fallback`, immediately returning `ctx.Err()`.
2. **Both Invokers Fail**:
   - Condition: Primary fails (e.g. 429 quota), Fallback also fails (e.g. invalid JSON response).
   - Handling: Error wraps both errors using `%w` so logs contain full diagnostics and `errors.Is`/`errors.As` works.
3. **Prompt Preservation**:
   - Condition: Prompt contains large text or special characters.
   - Handling: Pass `prompt` unchanged by value to both `Primary` and `Fallback`.
4. **Nil Invokers and Callbacks**:
   - Condition: Any combination of `Primary == nil`, `Fallback == nil`, `OnFallback == nil`.
   - Handling: Explicit branch guards without panics.
5. **Thread Safety**:
   - Condition: Multiple goroutines calling `Invoke()` concurrently.
   - Handling: Struct is stateless; reads only immutable fields; safe for concurrent use.
6. **Daemon Retry Behavior on Combined Failure**:
   - If fallback succeeds: summary drawer inserted, watermark advanced, session consumed from due queue.
   - If both fail: error returned to `daemon.PollOnce`, session left due in debounce tracker for next tick retry.

---

## 6. Testing Strategy

### Unit Tests (`internal/summarizer/summarizer_test.go`)
1. `TestFallbackInvoker_PrimarySuccess`: Primary succeeds -> Fallback not called, `OnFallback` not called.
2. `TestFallbackInvoker_PrimaryFails_FallbackSucceeds`: Primary fails -> Fallback succeeds -> returns fallback result and triggers `OnFallback` with primary error.
3. `TestFallbackInvoker_ContextCanceled`: Context canceled -> Primary fails -> Fallback NOT called, returns `context.Canceled`.
4. `TestFallbackInvoker_ContextTimeout`: Context deadline exceeded -> Primary fails -> Fallback NOT called, returns `context.DeadlineExceeded`.
5. `TestFallbackInvoker_BothFail_ReturnsCombinedError`: Both fail -> returns combined error containing both failure messages.
6. `TestFallbackInvoker_NilPrimary`: `Primary == nil`, `Fallback` valid -> calls Fallback directly.
7. `TestFallbackInvoker_NilFallback`: `Primary` fails, `Fallback == nil` -> returns Primary error safely without panic.
8. `TestFallbackInvoker_BothNil`: Both nil -> returns error without panic.
9. `TestFallbackInvoker_ConcurrentRace`: Run concurrent invocations under `-race` to ensure thread safety.

### Daemon Integration Test (`internal/daemon/daemon_summarize_test.go`)
10. `TestDaemon_SummarizeSession_FallbackIntegration`:
    - Setup daemon with a `FallbackInvoker` where Primary returns 429 error and Fallback returns valid summary items.
    - Run `summarizeSession`.
    - Assert that summary drawers are created with the expected content and watermark is updated.
