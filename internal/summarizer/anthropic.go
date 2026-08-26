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
