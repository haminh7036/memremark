# Direct API Invoker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement lightweight, native `net/http` Direct API Invokers for Google Gemini and Anthropic Claude in `internal/summarizer` to reduce peak memory from ~1.2GB to <10MB and cut latency to <1s, with graceful fallback to CLI subprocesses.

**Architecture:** Add `GeminiAPIInvoker` (using `generateContent` with `responseSchema`) and `AnthropicAPIInvoker` (using Messages API with forced `tool_choice` schema) implementing `summarizer.Invoker`. Extend `internal/config` to support API keys/models and update `cmd/memremarkd/main.go` to construct the optimal primary-fallback chain.

**Tech Stack:** Go 1.22+, `net/http`, `encoding/json`, `httptest` (standard library only, 0 added dependencies).

## Global Constraints

- Standard library only (`net/http`, `encoding/json`, `testing/httptest`). Do NOT add SDK dependencies to `go.mod`.
- Unified output contract: all invokers return a raw JSON array string `[{"hall":"...","content":"..."}]` parsed by `parseSummaryItems`.
- Resilient error handling: HTTP 4xx/5xx must return descriptive errors allowing `FallbackInvoker` to trigger.
- Full backwards compatibility: zero-config default (`provider = "auto"` without API keys) must continue using CLI subprocesses unchanged.

---

### Task 1: Implement `GeminiAPIInvoker` with Unit Tests

**Files:**
- Create: `internal/summarizer/gemini.go`
- Test: `internal/summarizer/gemini_test.go`

**Interfaces:**
- Produces: `type GeminiAPIInvoker struct { APIKey, Model, BaseURL string; HTTPClient *http.Client }`
- Implements: `summarizer.Invoker` (`Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)`)

- [ ] **Step 1: Write the failing tests in `internal/summarizer/gemini_test.go`**

```go
package summarizer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeminiAPIInvoker_Success(t *testing.T) {
	expectedReply := `[{"hall":"fact","content":"Use standard Go net/http"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("x-goog-api-key") != "test-gemini-key" {
			t.Errorf("unexpected api key: %s", r.Header.Get("x-goog-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{
							map[string]any{"text": expectedReply},
						},
						"role": "model",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	inv := GeminiAPIInvoker{
		APIKey:     "test-gemini-key",
		Model:      "gemini-2.5-flash",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	res, err := inv.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if res != expectedReply {
		t.Errorf("expected %q, got %q", expectedReply, res)
	}
}

func TestGeminiAPIInvoker_Errors(t *testing.T) {
	t.Run("429 Rate Limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"message":"Resource exhausted"}}`, http.StatusTooManyRequests)
		}))
		defer server.Close()

		inv := GeminiAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error on 429, got nil")
		}
	})

	t.Run("Context Timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		inv := GeminiAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(ctx, "prompt")
		if err == nil {
			t.Fatal("expected context timeout error, got nil")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/summarizer -run TestGeminiAPIInvoker`
Expected: FAIL (types not defined)

- [ ] **Step 3: Implement `GeminiAPIInvoker` in `internal/summarizer/gemini.go`**

```go
package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com"

// GeminiAPIInvoker calls the Google Gemini REST API directly using net/http.
type GeminiAPIInvoker struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type geminiSchemaProperty struct {
	Type string   `json:"type"`
	Enum []string `json:"enum,omitempty"`
}

type geminiItemsSchema struct {
	Type       string                          `json:"type"`
	Properties map[string]geminiSchemaProperty `json:"properties"`
	Required   []string                        `json:"required"`
}

type geminiResponseSchema struct {
	Type  string            `json:"type"`
	Items geminiItemsSchema `json:"items"`
}

type geminiGenerationConfig struct {
	ResponseMIMEType string               `json:"responseMimeType,omitempty"`
	ResponseSchema   geminiResponseSchema `json:"responseSchema,omitempty"`
}

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiCandidate struct {
	Content struct {
		Parts []geminiPart `json:"parts"`
		Role  string       `json:"role"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// Invoke calls the Gemini generateContent endpoint.
func (inv GeminiAPIInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	if inv.APIKey == "" {
		return "", fmt.Errorf("summarizer: gemini API key is required")
	}
	model := inv.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}
	baseURL := strings.TrimRight(inv.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultGeminiBaseURL
	}

	reqBody := geminiRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{{Text: prompt}},
			},
		},
		GenerationConfig: geminiGenerationConfig{
			ResponseMIMEType: "application/json",
			ResponseSchema: geminiResponseSchema{
				Type: "ARRAY",
				Items: geminiItemsSchema{
					Type: "OBJECT",
					Properties: map[string]geminiSchemaProperty{
						"hall": {
							Type: "STRING",
							Enum: []string{"fact", "discovery", "preference", "advice"},
						},
						"content": {
							Type: "STRING",
						},
					},
					Required: []string{"hall", "content"},
				},
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("summarizer: marshal gemini request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", baseURL, model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("summarizer: create gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", inv.APIKey)

	client := inv.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("summarizer: gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("summarizer: read gemini response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("summarizer: gemini API error (HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var gResp geminiResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return "", fmt.Errorf("summarizer: parse gemini response: %w", err)
	}

	if gResp.Error != nil {
		return "", fmt.Errorf("summarizer: gemini API returned error: %s (%s)", gResp.Error.Message, gResp.Error.Status)
	}

	if len(gResp.Candidates) == 0 || len(gResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("summarizer: gemini API returned empty candidate parts")
	}

	return gResp.Candidates[0].Content.Parts[0].Text, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/summarizer -run TestGeminiAPIInvoker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/gemini.go internal/summarizer/gemini_test.go
git commit -m "feat(summarizer): add GeminiAPIInvoker with structured response schema"
```

---

### Task 2: Implement `AnthropicAPIInvoker` with Unit Tests

**Files:**
- Create: `internal/summarizer/anthropic.go`
- Test: `internal/summarizer/anthropic_test.go`

**Interfaces:**
- Produces: `type AnthropicAPIInvoker struct { APIKey, Model, BaseURL string; HTTPClient *http.Client }`
- Implements: `summarizer.Invoker` (`Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)`)

- [ ] **Step 1: Write the failing tests in `internal/summarizer/anthropic_test.go`**

```go
package summarizer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicAPIInvoker_StructuredOutputSuccess(t *testing.T) {
	expectedItems := `[{"hall":"discovery","content":"Found root cause"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "test-anthropic-key" {
			t.Errorf("unexpected api key: %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected anthropic-version: %s", r.Header.Get("anthropic-version"))
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": expectedItems,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	inv := AnthropicAPIInvoker{
		APIKey:     "test-anthropic-key",
		Model:      "claude-3-5-haiku-20241022",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	res, err := inv.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if res != expectedItems {
		t.Errorf("expected %q, got %q", expectedItems, res)
	}
}

func TestAnthropicAPIInvoker_Errors(t *testing.T) {
	t.Run("401 Unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"message":"Invalid API key"}}`, http.StatusUnauthorized)
		}))
		defer server.Close()

		inv := AnthropicAPIInvoker{APIKey: "bad-key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error on 401, got nil")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/summarizer -run TestAnthropicAPIInvoker`
Expected: FAIL (types not defined)

- [ ] **Step 3: Implement `AnthropicAPIInvoker` in `internal/summarizer/anthropic.go`**

```go
package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultAnthropicBaseURL = "https://api.anthropic.com"

// AnthropicAPIInvoker calls Anthropic Messages REST API directly.
type AnthropicAPIInvoker struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
}

type anthropicMessageParam struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

type anthropicOutputConfig struct {
	Format anthropicFormat `json:"format"`
}

type anthropicRequest struct {
	Model        string                  `json:"model"`
	MaxTokens    int                     `json:"max_tokens"`
	Messages     []anthropicMessageParam `json:"messages"`
	OutputConfig anthropicOutputConfig   `json:"output_config"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Invoke calls Anthropic Messages API with output_config.format Structured Outputs.
func (inv AnthropicAPIInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	if inv.APIKey == "" {
		return "", fmt.Errorf("summarizer: anthropic API key is required")
	}
	model := inv.Model
	if model == "" {
		model = "claude-3-5-haiku-20241022"
	}
	baseURL := strings.TrimRight(inv.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 4096,
		Messages: []anthropicMessageParam{
			{Role: "user", Content: prompt},
		},
		OutputConfig: anthropicOutputConfig{
			Format: anthropicFormat{
				Type: "json_schema",
				Schema: map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"hall": map[string]any{
								"type": "string",
								"enum": []string{"fact", "discovery", "preference", "advice"},
							},
							"content": map[string]any{
								"type": "string",
							},
						},
						"required":             []string{"hall", "content"},
						"additionalProperties": false,
					},
				},
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("summarizer: marshal anthropic request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/messages", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("summarizer: create anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", inv.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := inv.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("summarizer: anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("summarizer: read anthropic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("summarizer: anthropic API error (HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var aResp anthropicResponse
	if err := json.Unmarshal(body, &aResp); err != nil {
		return "", fmt.Errorf("summarizer: parse anthropic response: %w", err)
	}

	if aResp.Error != nil {
		return "", fmt.Errorf("summarizer: anthropic API returned error: %s", aResp.Error.Message)
	}

	for _, block := range aResp.Content {
		if block.Type == "text" && block.Text != "" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("summarizer: anthropic API returned empty content")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/summarizer -run TestAnthropicAPIInvoker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/anthropic.go internal/summarizer/anthropic_test.go
git commit -m "feat(summarizer): add AnthropicAPIInvoker with forced tool_choice schema enforcement"
```

---

### Task 3: Update `SummarizerConfig` and Environment Overrides

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Extends: `SummarizerConfig` with `GeminiAPIKey`, `GeminiModel`, `AnthropicAPIKey`, `AnthropicModel`.

- [ ] **Step 1: Write failing config tests in `internal/config/config_test.go`**

```go
func TestConfig_APIKeyOverrides(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-gemini-key")
	t.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
	t.Setenv("MEMREMARK_GEMINI_MODEL", "gemini-2.5-pro")
	t.Setenv("MEMREMARK_ANTHROPIC_MODEL", "claude-3-haiku-20240307")

	cfg, err := LoadFromFile("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Summarizer.GeminiAPIKey != "env-gemini-key" {
		t.Errorf("expected GeminiAPIKey 'env-gemini-key', got '%s'", cfg.Summarizer.GeminiAPIKey)
	}
	if cfg.Summarizer.AnthropicAPIKey != "env-anthropic-key" {
		t.Errorf("expected AnthropicAPIKey 'env-anthropic-key', got '%s'", cfg.Summarizer.AnthropicAPIKey)
	}
	if cfg.Summarizer.GeminiModel != "gemini-2.5-pro" {
		t.Errorf("expected GeminiModel 'gemini-2.5-pro', got '%s'", cfg.Summarizer.GeminiModel)
	}
	if cfg.Summarizer.AnthropicModel != "claude-3-haiku-20240307" {
		t.Errorf("expected AnthropicModel 'claude-3-haiku-20240307', got '%s'", cfg.Summarizer.AnthropicModel)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/config -run TestConfig_APIKeyOverrides`
Expected: FAIL (fields do not exist)

- [ ] **Step 3: Update `internal/config/config.go`**

Add constants:
```go
const (
	DefaultGeminiModel    = "gemini-2.5-flash"
	DefaultAnthropicModel = "claude-3-5-haiku-20241022"
)
```

Extend `SummarizerConfig`:
```go
type SummarizerConfig struct {
	Provider          string `json:"provider"`
	GeminiAPIKey      string `json:"gemini_api_key"`
	GeminiModel       string `json:"gemini_model"`
	AnthropicAPIKey   string `json:"anthropic_api_key"`
	AnthropicModel    string `json:"anthropic_model"`
	ClaudeModel       string `json:"claude_model"`
	AntigravityModel  string `json:"antigravity_model"`
	AntigravityEffort string `json:"antigravity_effort"`
}
```

Update `DefaultConfig()` and environment parsing in `LoadFromFile`:
```go
if env := os.Getenv("MEMREMARK_GEMINI_API_KEY"); env != "" {
	cfg.Summarizer.GeminiAPIKey = env
} else if env := os.Getenv("GEMINI_API_KEY"); env != "" {
	cfg.Summarizer.GeminiAPIKey = env
}

if env := os.Getenv("MEMREMARK_GEMINI_MODEL"); env != "" {
	cfg.Summarizer.GeminiModel = env
}

if env := os.Getenv("MEMREMARK_ANTHROPIC_API_KEY"); env != "" {
	cfg.Summarizer.AnthropicAPIKey = env
} else if env := os.Getenv("ANTHROPIC_API_KEY"); env != "" {
	cfg.Summarizer.AnthropicAPIKey = env
}

if env := os.Getenv("MEMREMARK_ANTHROPIC_MODEL"); env != "" {
	cfg.Summarizer.AnthropicModel = env
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/config/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add Gemini and Anthropic API keys and models configuration"
```

---

### Task 4: Wire up Resolution Chain in `cmd/memremarkd/main.go`

**Files:**
- Modify: `cmd/memremarkd/main.go`
- Test: `cmd/memremarkd/main_test.go`

- [ ] **Step 1: Write test for `resolveInvokers` in `cmd/memremarkd/main_test.go`**

```go
package main

import (
	"errors"
	"testing"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/summarizer"
)

func TestResolveInvokers_GeminiAPIKeyPriority(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Summarizer.GeminiAPIKey = "test-gemini-key"

	lookPath := func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}

	setup := resolveInvokers(cfg, lookPath)
	if _, ok := setup.ClaudeInvoker.(summarizer.FallbackInvoker); !ok {
		t.Fatalf("expected FallbackInvoker, got %T", setup.ClaudeInvoker)
	}
}

func TestResolveInvokers_ExplicitProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Summarizer.Provider = "gemini"
	cfg.Summarizer.GeminiAPIKey = "key"

	lookPath := func(file string) (string, error) {
		return "", errors.New("not found")
	}

	setup := resolveInvokers(cfg, lookPath)
	fb, ok := setup.ClaudeInvoker.(summarizer.FallbackInvoker)
	if !ok {
		t.Fatalf("expected FallbackInvoker, got %T", setup.ClaudeInvoker)
	}
	if _, ok := fb.Primary.(summarizer.GeminiAPIInvoker); !ok {
		t.Errorf("expected primary GeminiAPIInvoker, got %T", fb.Primary)
	}
}
```

- [ ] **Step 2: Run test to verify it fails or needs updating**

Run: `go test -v ./cmd/memremarkd/...`

- [ ] **Step 3: Update `resolveInvokers` in `cmd/memremarkd/main.go`**

Implement hierarchy:
1. Construct API invokers if API keys are set:
   - `geminiInvoker := summarizer.GeminiAPIInvoker{APIKey: cfg.Summarizer.GeminiAPIKey, Model: cfg.Summarizer.GeminiModel}`
   - `anthropicInvoker := summarizer.AnthropicAPIInvoker{APIKey: cfg.Summarizer.AnthropicAPIKey, Model: cfg.Summarizer.AnthropicModel}`
2. Construct CLI invokers:
   - `claudeCLI := summarizer.ClaudeCodeInvoker{Model: cfg.Summarizer.ClaudeModel}`
   - `agyCLI := summarizer.AntigravityInvoker{Model: cfg.Summarizer.AntigravityModel, Effort: cfg.Summarizer.AntigravityEffort}`
3. Assemble resolution matrix handling `"gemini"`, `"anthropic"`, `"antigravity"`, `"claude"`, and `"auto"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./cmd/memremarkd/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/memremarkd/main.go cmd/memremarkd/main_test.go
git commit -m "feat(daemon): wire up Direct API Invokers with graceful CLI fallback in resolveInvokers"
```

---

### Task 5: End-to-End Verification & Documentation Update

**Files:**
- Modify: `config.schema.json`
- Modify: `README.md`
- Modify: `README_vi.md`

- [ ] **Step 1: Update `config.schema.json`**

Add JSON schema properties for `gemini_api_key`, `gemini_model`, `anthropic_api_key`, `anthropic_model`.

- [ ] **Step 2: Update `README.md` and `README_vi.md`**

Document Direct API configuration, performance benefits (near 0MB RSS, <1s latency), and fallback mechanisms.

- [ ] **Step 3: Run entire test suite & build check**

```bash
go build ./...
go test -v -race ./...
```

- [ ] **Step 4: Commit and tag**

```bash
git add config.schema.json README.md README_vi.md
git commit -m "docs: update config schema and documentation for direct API invokers"
```
