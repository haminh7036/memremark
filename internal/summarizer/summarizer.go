package summarizer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

// InvokerOptions configures invocation-specific parameters such as session scoping
// and working directory.
type InvokerOptions struct {
	SessionID string
	WorkDir   string
}

// Invoker runs one headless prompt through a CLI's own non-interactive
// mode and returns the model's plain-text reply, already unwrapped from
// that CLI's own JSON envelope. Concrete implementations shell out to
// `claude -p` or `agy -p`; tests use a stub.
type Invoker interface {
	Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error)
}

type claudeCodeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// ClaudeCodeInvoker runs prompts through `claude -p --output-format json`.
type ClaudeCodeInvoker struct {
	Model string
}

func (inv ClaudeCodeInvoker) buildArgs(sessionID string) []string {
	args := []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
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
func (inv ClaudeCodeInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	var opt InvokerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	cmd := exec.CommandContext(ctx, "claude", inv.buildArgs(opt.SessionID)...)
	if opt.WorkDir != "" {
		if info, err := os.Stat(opt.WorkDir); err == nil && info.IsDir() {
			cmd.Dir = opt.WorkDir
		}
	}
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

func (inv AntigravityInvoker) buildArgs(prompt, sessionID string) []string {
	args := []string{"--output-format", "json", "--disable-slash-commands"}
	if sessionID != "" {
		args = append(args, "--conversation", sessionID)
	}
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
	args = append(args, "-p", prompt)
	return args
}

// Invoke implements Invoker.
func (inv AntigravityInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	var opt InvokerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.SessionID != "" {
		ensureAntigravityConversationFile(opt.SessionID)
	}
	cmd := exec.CommandContext(ctx, "agy", inv.buildArgs(prompt, opt.SessionID)...)
	if opt.WorkDir != "" {
		if info, err := os.Stat(opt.WorkDir); err == nil && info.IsDir() {
			cmd.Dir = opt.WorkDir
		}
	}
	tmpFile, err := os.CreateTemp("", "memremark-agy-out-*.json")
	if err != nil {
		return "", fmt.Errorf("summarizer: create temp stdout file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	cmd.Stdout = tmpFile
	err = cmd.Run()
	_ = tmpFile.Close()
	if err != nil {
		return "", fmt.Errorf("summarizer: agy -p failed: %w", err)
	}

	out, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("summarizer: read agy -p output: %w", err)
	}

	trimmed := bytes.TrimSpace(out)
	if idx := bytes.IndexByte(trimmed, '{'); idx >= 0 {
		trimmed = trimmed[idx:]
	}
	var res antigravityResult
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return "", fmt.Errorf("summarizer: parse agy -p output: %w", err)
	}
	if res.Status != "SUCCESS" {
		return "", fmt.Errorf("summarizer: agy -p returned status %q: %s", res.Status, res.Error)
	}
	return res.Response, nil
}

func ensureAntigravityConversationFile(sessionID string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		return
	}
	dbPath := filepath.Join(convDir, sessionID+".db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		db, err := sql.Open("sqlite", dbPath)
		if err == nil {
			_, _ = db.Exec("CREATE TABLE IF NOT EXISTS steps (idx integer, step_payload blob);")
			_ = db.Close()
		}
	}
}

// NopInvoker is a no-op invoker used when no supported LLM CLI is available in PATH.
type NopInvoker struct{}

// Invoke implements Invoker by returning a clear descriptive error.
func (NopInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
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
func (f FallbackInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	if f.Primary == nil && f.Fallback == nil {
		return "", fmt.Errorf("summarizer: no invokers configured in FallbackInvoker")
	}
	if f.Primary == nil {
		return f.Fallback.Invoke(ctx, prompt, opts...)
	}

	res, err := f.Primary.Invoke(ctx, prompt, opts...)
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

	fallbackRes, fallbackErr := f.Fallback.Invoke(ctx, prompt, opts...)
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

// SummarizeWithOptions asks invoker to distill observations into hall-classified
// SummaryItems using the target language and invoker options. It returns (nil, nil)
// without invoking anything if observations is empty.
func SummarizeWithOptions(ctx context.Context, invoker Invoker, observations []observation.Observation, lang locale.TargetLanguage, opts ...InvokerOptions) ([]SummaryItem, error) {
	if len(observations) == 0 {
		return nil, nil
	}
	text, err := invoker.Invoke(ctx, buildPrompt(observations, lang), opts...)
	if err != nil {
		return nil, err
	}
	return parseSummaryItems(text)
}

// Summarize asks invoker to distill observations into hall-classified
// SummaryItems using the target language. It returns (nil, nil) without
// invoking anything if observations is empty.
func Summarize(ctx context.Context, invoker Invoker, observations []observation.Observation, lang ...locale.TargetLanguage) ([]SummaryItem, error) {
	var targetLang locale.TargetLanguage
	if len(lang) > 0 {
		targetLang = lang[0]
	}
	return SummarizeWithOptions(ctx, invoker, observations, targetLang)
}

func buildPrompt(observations []observation.Observation, lang locale.TargetLanguage) string {
	targetLangName := lang.Name
	if targetLangName == "" {
		targetLangName = "English"
	}

	var sb strings.Builder
	sb.WriteString("Given the following raw tool observations from a coding session, distill them into concise memory items.\n")
	sb.WriteString("Each item must belong to one of 4 halls:\n")
	sb.WriteString("- fact (settled architectural decisions, conventions, invariants)\n")
	sb.WriteString("- discovery (new findings, root causes, investigation results)\n")
	sb.WriteString("- preference (user habits, workflow choices, preferences)\n")
	sb.WriteString("- advice (actionable recommendations, solutions to pitfalls)\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString(fmt.Sprintf("1. Output language: Write the \"content\" field in %s. Use natural, standard technical terminology appropriate for %s (e.g. Katakana for Japanese, standard IT terms for Chinese, or common English terms where standard).\n", targetLangName, targetLangName))
	sb.WriteString("2. Strict code preservation: ALWAYS keep code identifiers, file paths, tool/command names, CLI flags, package names, and symbols in their exact original form (e.g., `main.go`, `go test -race`, `SQLite`, `memremarkd`).\n")
	sb.WriteString("3. Style: Write direct, concise, telegraphic bullet points. Avoid filler words.\n")
	sb.WriteString(`4. Format: Respond ONLY with a valid JSON array of objects: [{"hall":"...","content":"..."}]. If nothing is worth memorizing, return [].` + "\n\n")
	sb.WriteString("Observations:\n")
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
