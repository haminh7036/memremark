# Direct API Invoker Design (Gemini & Anthropic REST APIs)

- **Issue:** [#2 - [RFC] Real-time Antigravity live session discovery and direct API invoker to reduce peak memory](https://github.com/haminh7036/memremark/issues/2)
- **Author:** Antigravity & User Pair Programming
- **Date:** 2026-08-26
- **Status:** Approved / In Review

---

## 1. Overview & Problem Statement

Currently, `memremarkd` summarizes verbatim observations using headless CLI subprocesses (`agy -p` or `claude -p`).

### Problems with CLI Subprocesses
1. **High Peak Memory Footprint (~1.2GB RSS):** `agy -p` initializes all configured MCP servers and tool plugins on CLI startup, causing a temporary ~1.2GB - 1.3GB peak memory spike across the process tree during background distillation.
2. **Cold-Start Latency:** Starting a full CLI subprocess, spinning up plugins, and tearing them down takes ~3–8 seconds per summarization run.
3. **Execution Overhead:** Spawning subprocesses and creating temporary files under `/tmp` is heavier than making a lightweight HTTP REST call.

### Goals
- **Direct API Invokers:** Provide native `net/http` REST clients for **Gemini API** and **Anthropic API** without adding external SDK dependencies.
- **Zero-Config Fallback:** If no API keys are configured, seamlessly fall back to CLI subprocesses (`agy -p` / `claude -p`).
- **Unified Output Format:** Enforce identical structured JSON output (`[{"hall":"...","content":"..."}]`) across both API providers and CLI invokers, seamlessly consumed by `parseSummaryItems`.
- **Memory & Latency Optimization:** Drop summarization memory overhead to near 0MB additional RSS and cut distillation latency to < 1s.

---

## 2. Architecture & Unified Output Contract

### 2.1. Unified Output Contract

All summarization invokers (CLI and API) return a raw string containing a JSON array of `SummaryItem` objects:
```json
[
  {
    "hall": "fact",
    "content": "Settled architectural decisions, conventions, invariants"
  },
  {
    "hall": "discovery",
    "content": "New findings, root causes, investigation results"
  }
]
```

- **Allowed `hall` values:** `"fact"`, `"discovery"`, `"preference"`, `"advice"` (validated by `storage.IsValidHall`).
- **Parsing:** Handled by `parseSummaryItems` in `internal/summarizer/summarizer.go`, which is robust against leading/trailing prose and validates all `hall` fields.

---

## 3. Invoker Implementations (`internal/summarizer/`)

### 3.1. `GeminiAPIInvoker`

Calls Google's Gemini REST API (`v1beta`):
- **Default Endpoint:** `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- **Default Model:** `gemini-2.5-flash` (configurable via `gemini_model`)
- **Authentication:** `x-goog-api-key: <key>` header (or `?key=<key>` query parameter)
- **Request Payload:**
```json
{
  "contents": [
    {
      "parts": [
        {
          "text": "<prompt>"
        }
      ]
    }
  ],
  "generationConfig": {
    "responseMimeType": "application/json",
    "responseSchema": {
      "type": "ARRAY",
      "items": {
        "type": "OBJECT",
        "properties": {
          "hall": {
            "type": "STRING",
            "enum": ["fact", "discovery", "preference", "advice"]
          },
          "content": {
            "type": "STRING"
          }
        },
        "required": ["hall", "content"]
      }
    }
  }
}
```
- **Response Extraction:** Reads `candidates[0].content.parts[0].text`.

### 3.2. `AnthropicAPIInvoker`

Calls Anthropic Messages REST API (`v1`) with **Forced Tool Calling** (`tool_choice`) to strictly enforce JSON schema and prevent conversational formatting:
- **Default Endpoint:** `https://api.anthropic.com/v1/messages`
- **Default Model:** `claude-3-5-haiku-20241022` (configurable via `anthropic_model`)
- **Headers:**
  - `x-api-key: <key>`
  - `anthropic-version: 2023-06-01`
  - `content-type: application/json`
- **Request Payload:**
```json
{
  "model": "<model>",
  "max_tokens": 4096,
  "messages": [
    {
      "role": "user",
      "content": "<prompt>"
    }
  ],
  "tools": [
    {
      "name": "distill_memories",
      "description": "Distill observations into memory items",
      "input_schema": {
        "type": "object",
        "properties": {
          "items": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "hall": {
                  "type": "string",
                  "enum": ["fact", "discovery", "preference", "advice"]
                },
                "content": {
                  "type": "string"
                }
              },
              "required": ["hall", "content"]
            }
          }
        },
        "required": ["items"]
      }
    }
  ],
  "tool_choice": {
    "type": "tool",
    "name": "distill_memories"
  }
}
```
- **Response Extraction:** Reads `content` block of `type == "tool_use"` with `name == "distill_memories"`, serializes `input.items` to JSON string. If response is standard `type == "text"`, extracts `text` directly (fallback compatibility).

### 3.3. Invoker Struct Definitions

```go
type GeminiAPIInvoker struct {
    APIKey     string
    Model      string
    BaseURL    string // defaults to "https://generativelanguage.googleapis.com"
    HTTPClient *http.Client
}

type AnthropicAPIInvoker struct {
    APIKey     string
    Model      string
    BaseURL    string // defaults to "https://api.anthropic.com"
    HTTPClient *http.Client
}
```

Both implement the `summarizer.Invoker` interface:
```go
Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)
```

---

## 4. Configuration & Resolution Hierarchy

### 4.1. Configuration (`internal/config/config.go`)

Extend `SummarizerConfig` with API settings:

```go
const (
    DefaultGeminiModel    = "gemini-2.5-flash"
    DefaultAnthropicModel = "claude-3-5-haiku-20241022"
)

type SummarizerConfig struct {
    Provider          string `json:"provider"` // "auto", "gemini", "anthropic", "antigravity", "claude"
    GeminiAPIKey      string `json:"gemini_api_key"`
    GeminiModel       string `json:"gemini_model"`
    AnthropicAPIKey   string `json:"anthropic_api_key"`
    AnthropicModel    string `json:"anthropic_model"`
    ClaudeModel       string `json:"claude_model"`
    AntigravityModel  string `json:"antigravity_model"`
    AntigravityEffort string `json:"antigravity_effort"`
}
```

Environment variable overrides:
- `GEMINI_API_KEY` / `MEMREMARK_GEMINI_API_KEY` -> `cfg.Summarizer.GeminiAPIKey`
- `MEMREMARK_GEMINI_MODEL` -> `cfg.Summarizer.GeminiModel`
- `ANTHROPIC_API_KEY` / `MEMREMARK_ANTHROPIC_API_KEY` -> `cfg.Summarizer.AnthropicAPIKey`
- `MEMREMARK_ANTHROPIC_MODEL` -> `cfg.Summarizer.AnthropicModel`

### 4.2. Resolution & Fallback Logic (`cmd/memremarkd/main.go`)

`resolveInvokers(cfg, lookPath)` operates as follows:

1. **Explicit `provider == "gemini"`:**
   - Primary: `GeminiAPIInvoker`
   - Fallback: `AnthropicAPIInvoker` (if key available) or `AntigravityInvoker` / `ClaudeCodeInvoker` (if CLI in PATH).
2. **Explicit `provider == "anthropic"`:**
   - Primary: `AnthropicAPIInvoker`
   - Fallback: `GeminiAPIInvoker` (if key available) or `ClaudeCodeInvoker` / `AntigravityInvoker` (if CLI in PATH).
3. **Default `provider == "auto"`:**
   - **Case A (Gemini Key present):** Primary is `GeminiAPIInvoker`. Fallback to Anthropic API (if key present) or CLI (`agy` / `claude`).
   - **Case B (Anthropic Key present, no Gemini Key):** Primary is `AnthropicAPIInvoker`. Fallback to CLI (`claude` / `agy`).
   - **Case C (No API Keys):** Pure CLI resolution (existing `v0.1.6` behavior: `agy` and `claude` subprocesses).
4. **Explicit `provider == "antigravity"` / `"claude"`:**
   - Route to CLI subprocesses as defined in Issue #4.

---

## 5. Error Handling & Resilience

1. **HTTP Status Code Validation:**
   - HTTP 200 OK: Parse JSON response payload.
   - HTTP 429 (Rate Limit / Quota Exceeded) or 5xx (Server Error): Return a clear error wrapping status code and response body (e.g. `summarizer: gemini API error (HTTP 429): Quota exceeded`).
   - `FallbackInvoker` catches this error and transparently invokes the secondary fallback invoker.
2. **Context & Timeouts:**
   - Requests honor `ctx.Done()`. If `pollTimeout` or tick context cancels, HTTP in-flight request is aborted cleanly via `http.NewRequestWithContext`.
3. **No External Dependencies:**
   - Uses standard library `net/http`, `encoding/json`, `context`, `time`, and `fmt`. Zero added lines to `go.mod`.

---

## 6. Testing & Verification

1. **`GeminiAPIInvoker` Unit Tests (`internal/summarizer/gemini_test.go`):**
   - Mock HTTP server using `httptest.Server`.
   - Test successful generation with `responseSchema` JSON parsing.
   - Test HTTP 401 (Invalid API key), HTTP 429 (Rate limit), HTTP 500 (API server error).
   - Test context timeout / cancellation.
2. **`AnthropicAPIInvoker` Unit Tests (`internal/summarizer/anthropic_test.go`):**
   - Mock HTTP server verifying headers (`x-api-key`, `anthropic-version: 2023-06-01`).
   - Test successful message extraction with multi-block or single text block.
   - Test HTTP 401, HTTP 429, HTTP 500 handling.
3. **End-to-End Fallback Test:**
   - Test `FallbackInvoker` routing from a failing `GeminiAPIInvoker` (e.g., 429 mock) to `AnthropicAPIInvoker` or mock CLI invoker.
4. **Configuration & Resolution Tests (`internal/config/config_test.go` & `cmd/memremarkd/main_test.go`):**
   - Test environment variable parsing and provider hierarchy.
