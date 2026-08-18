# Single-CLI Standalone & Environment Robustness Implementation Plan

> **Goal:** Enable MemRemark to operate seamlessly, reliably, and with clean logging when only one (or zero) AI assistant CLI (`claude` or `agy`) is installed in `$PATH`.

---

## Proposed Changes

### 1. `internal/summarizer`
- Add `NopInvoker` struct implementing `Invoker` interface that returns `fmt.Errorf("summarizer: no active LLM CLI available in PATH")`.
- Add unit tests for `NopInvoker`.

### 2. `cmd/memremarkd`
- Add `InvokerSetup` struct.
- Add `resolveInvokers(cfg config.Config, lookPath func(string) (string, error)) InvokerSetup` function:
  - If both `claude` and `agy` exist: configure bidirectional `FallbackInvoker`.
  - If only `claude` exists: use `ClaudeCodeInvoker` for both Claude and Antigravity sessions (no blind fallback to `agy`).
  - If only `agy` exists: use `AntigravityInvoker` for both sessions (no blind fallback to `claude`).
  - If neither exists: use `NopInvoker` with warning log.
- Update `main()` in `cmd/memremarkd/main.go` to use `resolveInvokers`.
- Add unit tests in `cmd/memremarkd/main_test.go` covering all 4 matrix combinations.

### 3. `internal/daemon`
- Verify / add tests in `daemon_test.go` confirming `PollOnce` runs without errors when transcript paths do not exist.

---

## Verification Plan

### Automated Tests
1. `go test -v -race ./internal/summarizer/...`
2. `go test -v -race ./cmd/memremarkd/...`
3. `go test -v -race ./internal/daemon/...`
4. Full test suite: `go test -v -race ./...`

### Build Verification
- `make build` -> ensures all 5 binaries compile cleanly.
