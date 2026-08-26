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
	t.Run("Missing API Key", func(t *testing.T) {
		inv := GeminiAPIInvoker{APIKey: ""}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error when APIKey is empty, got nil")
		}
	})

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

	t.Run("API Error In Body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Quota exceeded",
					"status":  "RESOURCE_EXHAUSTED",
				},
			})
		}))
		defer server.Close()

		inv := GeminiAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error when response contains error field, got nil")
		}
	})

	t.Run("Empty Candidates", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{},
			})
		}))
		defer server.Close()

		inv := GeminiAPIInvoker{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()}
		_, err := inv.Invoke(context.Background(), "prompt")
		if err == nil {
			t.Fatal("expected error when candidates is empty, got nil")
		}
	})

	t.Run("Default Model In URL", func(t *testing.T) {
		var requestedPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestedPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"candidates": []any{
					map[string]any{
						"content": map[string]any{
							"parts": []any{
								map[string]any{"text": "[]"},
							},
						},
					},
				},
			})
		}))
		defer server.Close()

		inv := GeminiAPIInvoker{APIKey: "key", BaseURL: server.URL + "/", HTTPClient: server.Client()}
		res, err := inv.Invoke(context.Background(), "prompt")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if res != "[]" {
			t.Errorf("expected '[]', got %q", res)
		}
		expectedPath := "/v1beta/models/gemini-2.5-flash:generateContent"
		if requestedPath != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, requestedPath)
		}
	})
}
