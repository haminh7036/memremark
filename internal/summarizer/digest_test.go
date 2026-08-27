package summarizer_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

type stubDigestInvoker struct {
	response       string
	err            error
	recordedPrompt string
	recordedOpts   []summarizer.InvokerOptions
}

func (s *stubDigestInvoker) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	s.recordedPrompt = prompt
	s.recordedOpts = append(s.recordedOpts, opts...)
	return s.response, s.err
}

func TestSynthesizeSessionDigest(t *testing.T) {
	jsonReply := `{
		"request": "Implement SQLite caching",
		"investigated": "Looked into memory vs disk trade-offs",
		"learned": "Busy timeout avoids locking errors",
		"completed": "Added timeout and WAL mode",
		"next_steps": "Write benchmark tests",
		"notes": "Ensure clean shutdown"
	}`

	invoker := &stubDigestInvoker{response: jsonReply}
	summaries := []storage.Drawer{
		{
			Hall:      "fact",
			Content:   "SQLite requires busy_timeout",
			Narrative: "Setting busy_timeout prevents SQLITE_BUSY error under concurrency",
			CreatedAt: time.Now(),
		},
	}

	result, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		summaries,
		nil,
		locale.TargetLanguage{Code: "en", Name: "English"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil digest result")
	}

	if result.Request != "Implement SQLite caching" || result.Completed != "Added timeout and WAL mode" {
		t.Errorf("unexpected digest result: %+v", result)
	}
	if result.Investigated != "Looked into memory vs disk trade-offs" || result.Learned != "Busy timeout avoids locking errors" {
		t.Errorf("unexpected digest result fields: %+v", result)
	}
	if result.NextSteps != "Write benchmark tests" || result.Notes != "Ensure clean shutdown" {
		t.Errorf("unexpected digest result fields: %+v", result)
	}
}

func TestSynthesizeSessionDigest_WithFallbackVerbatim(t *testing.T) {
	jsonReply := `{"request":"Fix build","investigated":"Go compiler errors","learned":"Missing import","completed":"Added import","next_steps":"Run tests","notes":""}`
	invoker := &stubDigestInvoker{response: jsonReply}

	fallbackVerbatim := []storage.Drawer{
		{
			ToolName:  "Bash",
			Content:   "go build ./...",
			CreatedAt: time.Now(),
		},
	}

	result, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		nil,
		fallbackVerbatim,
		locale.TargetLanguage{Code: "vi", Name: "Vietnamese"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Request != "Fix build" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(invoker.recordedPrompt, "go build ./...") {
		t.Errorf("expected prompt to contain verbatim tool observation, got: %s", invoker.recordedPrompt)
	}
	if !strings.Contains(invoker.recordedPrompt, "Vietnamese") {
		t.Errorf("expected prompt to mention target language Vietnamese, got: %s", invoker.recordedPrompt)
	}
}

func TestSynthesizeSessionDigest_EmptyInputs(t *testing.T) {
	invoker := &stubDigestInvoker{err: errors.New("invoker should not be called")}

	result, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		nil,
		nil,
		locale.TargetLanguage{Code: "en", Name: "English"},
	)
	if err != nil {
		t.Fatalf("expected no error on empty input, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result on empty input, got: %+v", result)
	}
}

func TestSynthesizeSessionDigest_InvokerError(t *testing.T) {
	invoker := &stubDigestInvoker{err: errors.New("model offline")}
	summaries := []storage.Drawer{{Content: "item", CreatedAt: time.Now()}}

	_, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		summaries,
		nil,
		locale.TargetLanguage{Code: "en", Name: "English"},
	)
	if err == nil {
		t.Fatal("expected error when invoker fails, got nil")
	}
}

func TestSynthesizeSessionDigest_MarkdownAndProseParsing(t *testing.T) {
	wrappedReply := "Here is the synthesized digest:\n```json\n" +
		`{"request":"Refactor auth","investigated":"OAuth2 flows","learned":"Token refresh needed","completed":"Implemented refresh token","next_steps":"Add unit tests","notes":"None"}` +
		"\n```\nLet me know if you need more details."

	invoker := &stubDigestInvoker{response: wrappedReply}
	summaries := []storage.Drawer{{Content: "item", CreatedAt: time.Now()}}

	res, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		summaries,
		nil,
		locale.TargetLanguage{Code: "en", Name: "English"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Request != "Refactor auth" || res.Completed != "Implemented refresh token" {
		t.Errorf("failed to parse wrapped JSON: %+v", res)
	}
}

func TestSynthesizeSessionDigest_ForwardsOptions(t *testing.T) {
	jsonReply := `{"request":"req","investigated":"inv","learned":"lea","completed":"comp","next_steps":"nxt","notes":"not"}`
	invoker := &stubDigestInvoker{response: jsonReply}
	summaries := []storage.Drawer{{Content: "item", CreatedAt: time.Now()}}

	opts := summarizer.InvokerOptions{SessionID: "sess-abc", WorkDir: "/tmp/proj"}
	_, err := summarizer.SynthesizeSessionDigest(
		context.Background(),
		invoker,
		summaries,
		nil,
		locale.TargetLanguage{Code: "en", Name: "English"},
		opts,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invoker.recordedOpts) != 1 || invoker.recordedOpts[0].SessionID != "sess-abc" {
		t.Errorf("options not forwarded correctly: %+v", invoker.recordedOpts)
	}
}
