package summarizer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
)

// SessionDigestResult represents the synthesized high-level digest of a coding session.
type SessionDigestResult struct {
	Request      string `json:"request"`
	Investigated string `json:"investigated"`
	Learned      string `json:"learned"`
	Completed    string `json:"completed"`
	NextSteps    string `json:"next_steps"`
	Notes        string `json:"notes"`
}

// SynthesizeSessionDigest asks invoker to synthesize a structured session digest from
// distilled summary items and optional fallback verbatim observations.
// Returns (nil, nil) if both summaries and fallbackVerbatim are empty.
func SynthesizeSessionDigest(
	ctx context.Context,
	invoker Invoker,
	summaries []storage.Drawer,
	fallbackVerbatim []storage.Drawer,
	lang locale.TargetLanguage,
	opts ...InvokerOptions,
) (*SessionDigestResult, error) {
	if len(summaries) == 0 && len(fallbackVerbatim) == 0 {
		return nil, nil
	}

	prompt := buildDigestPrompt(summaries, fallbackVerbatim, lang)
	text, err := invoker.Invoke(ctx, prompt, opts...)
	if err != nil {
		return nil, fmt.Errorf("summarizer: synthesize session digest: %w", err)
	}

	return parseSessionDigestResult(text)
}

func buildDigestPrompt(summaries []storage.Drawer, fallbackVerbatim []storage.Drawer, lang locale.TargetLanguage) string {
	targetLangName := lang.Name
	if targetLangName == "" {
		targetLangName = "English"
	}

	var sb strings.Builder
	sb.WriteString("Given the following memory items and observations from a coding session, synthesize a high-level structured session digest.\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString(fmt.Sprintf("1. Output language: Write all digest fields in %s. Use natural technical terminology appropriate for %s.\n", targetLangName, targetLangName))
	sb.WriteString("2. Strict code preservation: ALWAYS keep code identifiers, file paths, tool/command names, CLI flags, package names, and symbols in their exact original form (e.g. `main.go`, `go test -race`, `SQLite`, `memremarkd`).\n")
	sb.WriteString("3. Format: Respond ONLY with a valid JSON object with these exact keys:\n")
	sb.WriteString("   - \"request\": The user's prompt or core objective of the session.\n")
	sb.WriteString("   - \"investigated\": Root causes analyzed, alternatives evaluated, or files inspected.\n")
	sb.WriteString("   - \"learned\": Key findings, domain discoveries, gotchas, or lessons learned.\n")
	sb.WriteString("   - \"completed\": Concrete changes, fixes, implementations, or features completed.\n")
	sb.WriteString("   - \"next_steps\": Actionable next steps, follow-up items, or pending work.\n")
	sb.WriteString("   - \"notes\": Important context, edge cases, caveats, or environment details.\n\n")

	if len(summaries) > 0 {
		sb.WriteString("Distilled Memory Items:\n")
		for _, s := range summaries {
			if s.Narrative != "" {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n  Narrative: %s\n", s.Hall, s.Content, s.Narrative))
			} else {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", s.Hall, s.Content))
			}
		}
		sb.WriteString("\n")
	}

	if len(fallbackVerbatim) > 0 {
		sb.WriteString("Tool Observations:\n")
		for _, v := range fallbackVerbatim {
			tool := v.ToolName
			if tool == "" {
				tool = "observation"
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", tool, v.Content))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func parseSessionDigestResult(modelText string) (*SessionDigestResult, error) {
	text := strings.TrimSpace(modelText)

	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		var res SessionDigestResult
		decoder := json.NewDecoder(strings.NewReader(text[i:]))
		if err := decoder.Decode(&res); err != nil {
			continue
		}
		return &res, nil
	}

	return nil, fmt.Errorf("summarizer: no JSON object found in model reply: %q", truncate(text, 200))
}
