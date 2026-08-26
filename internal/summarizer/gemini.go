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

var defaultGeminiHTTPClient = &http.Client{Timeout: 45 * time.Second}

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
		client = defaultGeminiHTTPClient
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
