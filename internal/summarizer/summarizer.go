package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

// Invoker runs one headless prompt through a CLI's own non-interactive
// mode and returns the model's plain-text reply, already unwrapped from
// that CLI's own JSON envelope. Concrete implementations shell out to
// `claude -p` or `agy -p`; tests use a stub.
type Invoker interface {
	Invoke(ctx context.Context, prompt string) (string, error)
}

type claudeCodeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// ClaudeCodeInvoker runs prompts through `claude -p --output-format json`.
type ClaudeCodeInvoker struct{}

// Invoke implements Invoker.
func (ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	out, err := exec.CommandContext(ctx, "claude", "-p", prompt, "--output-format", "json").Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: claude -p failed: %w", err)
	}
	var res claudeCodeResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse claude -p output: %w", err)
	}
	if res.IsError {
		return "", fmt.Errorf("summarizer: claude -p reported an error result")
	}
	return res.Result, nil
}

type antigravityResult struct {
	Status   string `json:"status"`
	Response string `json:"response"`
	Error    string `json:"error"`
}

// AntigravityInvoker runs prompts through `agy -p --output-format json`.
type AntigravityInvoker struct{}

// Invoke implements Invoker.
func (AntigravityInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	out, err := exec.CommandContext(ctx, "agy", "-p", prompt, "--output-format", "json").Output()
	if err != nil {
		return "", fmt.Errorf("summarizer: agy -p failed: %w", err)
	}
	var res antigravityResult
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse agy -p output: %w", err)
	}
	if res.Status != "SUCCESS" {
		return "", fmt.Errorf("summarizer: agy -p returned status %q: %s", res.Status, res.Error)
	}
	return res.Response, nil
}

// SummaryItem is one distilled piece of knowledge the model extracted
// from a batch of verbatim observations.
type SummaryItem struct {
	Hall    string `json:"hall"`
	Content string `json:"content"`
}

// Summarize asks invoker to distill observations into hall-classified
// SummaryItems. It returns (nil, nil) without invoking anything if
// observations is empty.
func Summarize(ctx context.Context, invoker Invoker, observations []observation.Observation) ([]SummaryItem, error) {
	if len(observations) == 0 {
		return nil, nil
	}
	text, err := invoker.Invoke(ctx, buildPrompt(observations))
	if err != nil {
		return nil, err
	}
	return parseSummaryItems(text)
}

func buildPrompt(observations []observation.Observation) string {
	var sb strings.Builder
	sb.WriteString("Dưới đây là các quan sát thô (tool call) từ một phiên làm việc. ")
	sb.WriteString("Đúc kết chúng thành các mục tri thức ngắn gọn, mỗi mục thuộc 1 trong 4 loại: ")
	sb.WriteString("fact (quyết định đã chốt), discovery (phát hiện mới), preference (thói quen/sở thích), advice (khuyến nghị/giải pháp). ")
	sb.WriteString(`Trả lời DUY NHẤT bằng 1 JSON array, mỗi phần tử có dạng {"hall":"...","content":"..."}. `)
	sb.WriteString("Nếu không có gì đáng đúc kết, trả về [].\n\nQuan sát:\n")
	for _, o := range observations {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", o.ToolName, o.Content))
	}
	return sb.String()
}

func parseSummaryItems(modelText string) ([]SummaryItem, error) {
	text := strings.TrimSpace(modelText)

	// Try each '[' position in the text. The first one that successfully
	// decodes a valid JSON array is the real array; this handles prose with
	// stray brackets (e.g. "see ref[1]" or "ref[2]") before or after the array.
	for i := 0; i < len(text); i++ {
		if text[i] != '[' {
			continue
		}
		var items []SummaryItem
		decoder := json.NewDecoder(strings.NewReader(text[i:]))
		if err := decoder.Decode(&items); err != nil {
			// This '[' position didn't decode successfully, try the next one
			continue
		}
		// Successfully decoded! Validate hall values and return.
		for _, it := range items {
			if !isValidHallForSummarizer(it.Hall) {
				return nil, fmt.Errorf("summarizer: model returned invalid hall %q", it.Hall)
			}
		}
		return items, nil
	}

	return nil, fmt.Errorf("summarizer: no JSON array found in model reply: %q", truncate(text, 200))
}

func isValidHallForSummarizer(hall string) bool {
	switch hall {
	case storage.HallFact, storage.HallDiscovery, storage.HallPreference, storage.HallAdvice:
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
