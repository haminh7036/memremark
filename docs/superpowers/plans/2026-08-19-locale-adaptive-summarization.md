# Implementation Plan: Locale-Adaptive Knowledge Distillation & Multi-Language Context Injection

**Date:** 2026-08-19  
**Spec Reference:** [`docs/superpowers/specs/2026-08-19-locale-adaptive-summarization-design.md`](file:///home/minh/Documents/memremark/docs/superpowers/specs/2026-08-19-locale-adaptive-summarization-design.md)  
**Version Target:** `v0.1.2`  

---

## 1. Plan Overview & Phases

- [ ] **Phase 1: Module `internal/locale`**
  - Implement `TargetLanguage` struct, `Normalize(raw string)`, and `DetectLanguage(configuredLang string)`.
  - Add comprehensive unit tests in `internal/locale/locale_test.go` covering POSIX env vars, aliases, and fallbacks.
- [ ] **Phase 2: Config Extension (`internal/config`)**
  - Add `Language` field with `"auto"` default in `DefaultConfig()`.
  - Add environment variable override `MEMREMARK_LANGUAGE`.
  - Update `internal/config/config_test.go`.
- [ ] **Phase 3: Prompt & Context Header Refactoring**
  - Update `buildPrompt` in `internal/summarizer/summarizer.go` to accept `locale.TargetLanguage` and use English framing + output rule.
  - Update `internal/summarizer/summarizer_test.go`.
  - Update `FormatSummaries` in `internal/hookctx/hookctx.go` to accept `locale.TargetLanguage` and render localized headers.
  - Update `internal/hookctx/hookctx_test.go`.
- [ ] **Phase 4: Daemon & Hook Wiring**
  - Wire `locale.DetectLanguage(cfg.Language)` into `cmd/memremarkd/main.go`, `cmd/memremark-hook-claude/main.go`, and `cmd/memremark-hook-agy/main.go`.
  - Pass target language through `daemon.Daemon`.
- [ ] **Phase 5: Version Bump, Verification & Documentation**
  - Bump `internal/version/version.go` to `0.1.2`, along with `web/package.json` and tests.
  - Run full test suite: `go test -v -race ./...`
  - Build all binaries: `make build`
  - Update `README.md` and `README_vi.md`.
