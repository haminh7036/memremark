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
type ClaudeCodeInvoker struct {
	Model string
}

func (inv ClaudeCodeInvoker) buildArgs() []string {
	args := []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""}
	model := inv.Model
	if model == "" {
		model = "haiku"
	}
	if model != "default" && model != "none" {
		args = append(args, "--model", model)
	}
	return args
}

// Invoke implements Invoker.
//
// ponytail: incident 2026-08-18 -- passing prompt as a positional arg hit
// Linux's per-argument MAX_ARG_STRLEN (32 pages, 131072 bytes) long before
// the much larger total ARG_MAX, so any backlog-derived prompt over ~128KB
// failed with "argument list too long" forever (nothing ever advanced the
// retry cursor). `claude -p` (empirically verified) reads the prompt from
// stdin when no positional prompt arg is given, which has no such limit.
func (inv ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", inv.buildArgs()...)
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.Output()
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
type AntigravityInvoker struct {
	Model  string
	Effort string
}

func (inv AntigravityInvoker) buildArgs(prompt string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--disable-slash-commands"}
	model := inv.Model
	if model == "" {
		model = "gemini-3.7-flash-low"
	}
	if model != "default" && model != "none" {
		args = append(args, "--model", model)
	}
	effort := inv.Effort
	if effort == "" {
		effort = "low"
	}
	if effort != "default" && effort != "none" {
		args = append(args, "--effort", effort)
	}
	return args
}

// Invoke implements Invoker.
func (inv AntigravityInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "agy", inv.buildArgs(prompt)...)
	out, err := cmd.Output()
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

// NopInvoker is a no-op invoker used when no supported LLM CLI is available in PATH.
type NopInvoker struct{}

// Invoke implements Invoker by returning a clear descriptive error.
func (NopInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	return "", fmt.Errorf("summarizer: no active LLM CLI available in PATH")
}

// FallbackInvoker wraps a Primary and a Fallback invoker.
// If Primary fails, it automatically delegates to Fallback unless the context
// was canceled or timed out.
type FallbackInvoker struct {
	Primary    Invoker
	Fallback   Invoker
	OnFallback func(primaryErr error)
}

// Invoke implements Invoker with automatic fallback.
func (f FallbackInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	if f.Primary == nil && f.Fallback == nil {
		return "", fmt.Errorf("summarizer: no invokers configured in FallbackInvoker")
	}
	if f.Primary == nil {
		return f.Fallback.Invoke(ctx, prompt)
	}

	res, err := f.Primary.Invoke(ctx, prompt)
	if err == nil {
		return res, nil
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if f.Fallback == nil {
		return "", err
	}

	if f.OnFallback != nil {
		f.OnFallback(err)
	}

	fallbackRes, fallbackErr := f.Fallback.Invoke(ctx, prompt)
	if fallbackErr != nil {
		return "", fmt.Errorf("summarizer: primary failed (%w); fallback failed (%w)", err, fallbackErr)
	}

	return fallbackRes, nil
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

	// Try each '[' position in the text. Prefer a valid non-empty array, but fall
	// back to an empty array if that's the only candidate. This correctly handles
	// prose with stray brackets (e.g. "see ref[1]" or "ref[2]") AND junk arrays
	// like [] or [{}] that appear before the real intended array.
	var emptyArrayCandidate []SummaryItem
	var lastValidationError error

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

		// Successfully decoded! Check if it's valid.
		if len(items) == 0 {
			// Empty array is a fallback candidate (legitimate "nothing to summarize")
			// but keep looking in case there's a non-empty valid array later
			if emptyArrayCandidate == nil {
				emptyArrayCandidate = items
			}
			continue
		}

		// Non-empty array: validate hall values
		validationErr := validateHallValues(items)
		if validationErr != nil {
			// This non-empty array has invalid hall values; keep looking
			lastValidationError = validationErr
			continue
		}

		// Non-empty array with all valid halls: this is what we want!
		return items, nil
	}

	// No valid non-empty array found. Prefer surfacing a validation error from
	// a non-empty candidate over silently falling back to "nothing to
	// summarize" -- there WAS real content, just with an invalid hall value,
	// and that needs attention rather than silent suppression. Only fall back
	// to the empty-array candidate when no other candidate existed at all.
	if lastValidationError != nil {
		return nil, lastValidationError
	}
	if emptyArrayCandidate != nil {
		return emptyArrayCandidate, nil
	}

	return nil, fmt.Errorf("summarizer: no JSON array found in model reply: %q", truncate(text, 200))
}

func validateHallValues(items []SummaryItem) error {
	for _, it := range items {
		if !storage.IsValidHall(it.Hall) {
			return fmt.Errorf("summarizer: model returned invalid hall %q", it.Hall)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
