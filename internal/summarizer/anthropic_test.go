package summarizer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnthropicAPIInvoker_StructuredOutputSuccess(t *testing.T) {
	expectedItems := `[{"hall":"discovery","content":"Found root cause"}]`
	var capturedReq anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-anthropic-key" {
			t.Errorf("unexpected api key: %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected anthropic-version: %s", r.Header.Get("anthropic-version"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		if err := json.Unmarshal(bodyBytes, &capturedReq); err != nil {
			t.Fatalf("failed to unmarshal request body: %v", err)
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

	if capturedReq.Model != "claude-3-5-haiku-20241022" {
		t.Errorf("expected model claude-3-5-haiku-20241022, got %s", capturedReq.Model)
	}
	if capturedReq.MaxTokens != 4096 {
		t.Errorf("expected max_tokens 4096, got %d", capturedReq.MaxTokens)
	}
	if len(capturedReq.Messages) != 1 || capturedReq.Messages[0].Content != "test prompt" {
		t.Errorf("unexpected messages: %+v", capturedReq.Messages)
	}
	if capturedReq.OutputConfig.Format.Type != "json_schema" {
		t.Errorf("expected output_config json_schema, got %s", capturedReq.OutputConfig.Format.Type)
	}
}

func TestAnthropicAPIInvoker_Errors(t *testing.T) {
	t.Run("Missing API Key", func(t *testing.T) {
		inv := AnthropicAPIInvoker{APIKey: ""}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error when APIKey is empty, got nil")
		}
	})

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

	t.Run("429 Rate Limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"message":"Rate limit exceeded"}}`, http.StatusTooManyRequests)
		}))
		defer server.Close()

		inv := AnthropicAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
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

		inv := AnthropicAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(ctx, "prompt")
		if err == nil {
			t.Fatal("expected context timeout error, got nil")
		}
	})

	t.Run("API Error In Body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"type":    "invalid_request_error",
					"message": "Overloaded",
				},
			})
		}))
		defer server.Close()

		inv := AnthropicAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error when response contains error field, got nil")
		}
	})

	t.Run("Empty Content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []any{},
			})
		}))
		defer server.Close()

		inv := AnthropicAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error when content is empty, got nil")
		}
	})

	t.Run("Default Model and BaseURL Trailing Slash", func(t *testing.T) {
		var capturedModel string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req anthropicRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			capturedModel = req.Model

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "[]",
					},
				},
			})
		}))
		defer server.Close()

		inv := AnthropicAPIInvoker{
			APIKey:     "key",
			BaseURL:    server.URL + "/",
			HTTPClient: server.Client(),
		}
		res, err := inv.Invoke(context.Background(), "prompt")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if res != "[]" {
			t.Errorf("expected '[]', got %q", res)
		}
		if capturedModel != "claude-3-5-haiku-20241022" {
			t.Errorf("expected default model claude-3-5-haiku-20241022, got %s", capturedModel)
		}
	})
}
