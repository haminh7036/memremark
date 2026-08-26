package summarizer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
)

type stubInvoker struct {
	reply string
	err   error
}

func (s stubInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	return s.reply, s.err
}

func TestSummarizeParsesValidJSONReply(t *testing.T) {
	stub := stubInvoker{reply: "Here you go:\n" +
		`[{"hall":"fact","content":"chose SQLite for v1"},{"hall":"discovery","content":"agy hooks don't execute"}]` +
		"\nHope that helps!"}

	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	if items[0].Hall != "fact" || items[0].Content != "chose SQLite for v1" {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
}

func TestSummarizeOnEmptyObservationsSkipsInvocation(t *testing.T) {
	stub := stubInvoker{err: errors.New("should not be called")}
	items, err := Summarize(context.Background(), stub, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if items != nil {
		t.Fatalf("expected nil items, got %v", items)
	}
}

func TestSummarizeReturnsErrorWhenInvokerFails(t *testing.T) {
	stub := stubInvoker{err: errors.New("network down")}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	if _, err := Summarize(context.Background(), stub, obs); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestSummarizeRejectsInvalidHall(t *testing.T) {
	stub := stubInvoker{reply: `[{"hall":"not-a-real-hall","content":"x"}]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	if _, err := Summarize(context.Background(), stub, obs); err == nil {
		t.Fatalf("expected error for invalid hall, got nil")
	}
}

func TestSummarizeReturnsEmptyForEmptyJSONArray(t *testing.T) {
	stub := stubInvoker{reply: `[]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestBuildPromptIncludesObservationContent(t *testing.T) {
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls -la"}}
	prompt := buildPrompt(obs, locale.TargetLanguage{Code: "vi", Name: "Vietnamese"})
	if !strings.Contains(prompt, "ls -la") {
		t.Fatalf("expected prompt to include observation content, got %q", prompt)
	}
	if !strings.Contains(prompt, "Write the \"content\" field in Vietnamese") {
		t.Fatalf("expected prompt to include target language Vietnamese, got %q", prompt)
	}
	if !strings.Contains(prompt, "Strict code preservation") {
		t.Fatalf("expected prompt to include strict code preservation rule, got %q", prompt)
	}

	// Test Japanese prompt
	promptJa := buildPrompt(obs, locale.TargetLanguage{Code: "ja", Name: "Japanese"})
	if !strings.Contains(promptJa, "Write the \"content\" field in Japanese") {
		t.Fatalf("expected prompt to include Japanese, got %q", promptJa)
	}
}

func TestSummarizeHandlesLeadingProseWithBracket(t *testing.T) {
	// Leading prose contains a bracket (ref[1]) before the real JSON array
	stub := stubInvoker{reply: `As shown in ref[1], here's the array: [{"hall":"fact","content":"use TypeScript"}]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 1 || items[0].Content != "use TypeScript" {
		t.Fatalf("expected 1 item with content 'use TypeScript', got %+v", items)
	}
}

func TestSummarizeHandlesTrailingProseWithBracket(t *testing.T) {
	// Trailing prose contains a bracket (ref[2]) after the real JSON array closes
	stub := stubInvoker{reply: `[{"hall":"discovery","content":"found a bug"}]
Cool, right? See ref[2] for more details.`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 1 || items[0].Content != "found a bug" {
		t.Fatalf("expected 1 item with content 'found a bug', got %+v", items)
	}
}

func TestSummarizeSkipsEmptyArrayInProseBeforeRealArray(t *testing.T) {
	// Empty array [] in prose before the real non-empty array.
	// Must skip the empty array and return the real array, not silently return empty result.
	stub := stubInvoker{reply: `Previously I returned [] but now here's the real data: [{"hall":"fact","content":"real"}]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 1 || items[0].Content != "real" {
		t.Fatalf("expected 1 item with content 'real', got %+v", items)
	}
}

func TestSummarizeSkipsInvalidHallInProseBeforeRealArray(t *testing.T) {
	// Invalid-hall content [{}] in prose before the real valid array.
	// Must skip the invalid items and return the real array, not error on the junk.
	stub := stubInvoker{reply: `Noise [{}] before real: [{"hall":"fact","content":"real"}]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	items, err := Summarize(context.Background(), stub, obs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(items) != 1 || items[0].Content != "real" {
		t.Fatalf("expected 1 item with content 'real', got %+v", items)
	}
}

// TestSummarizeSurfacesValidationErrorOverEmptyFallback covers Minor 6: a
// reply with a stray empty array AND a non-empty-but-invalid-hall array, and
// no valid non-empty array anywhere. There WAS real content (just with a bad
// hall value), so the empty-array fallback must not silently win -- the
// validation error should be surfaced instead.
func TestSummarizeSurfacesValidationErrorOverEmptyFallback(t *testing.T) {
	stub := stubInvoker{reply: `[] and also [{"hall":"not-a-real-hall","content":"real"}]`}
	obs := []observation.Observation{{ToolName: "Bash", Content: "ls"}}
	_, err := Summarize(context.Background(), stub, obs)
	if err == nil {
		t.Fatalf("expected validation error to be surfaced instead of a silent empty result")
	}
}

// TestClaudeCodeInvokerSendsPromptViaStdinNotArgv is the regression test for
// the production incident where a prompt over Linux's per-argument
// MAX_ARG_STRLEN (131,072 bytes) made `claude -p <prompt>` fail with
// "argument list too long" forever. It stands in a fake `claude` executable
// on PATH that records the argv it received and the stdin it received, so
// this proves the real exec.Command wiring -- not just an internal helper.
func TestClaudeCodeInvokerSendsPromptViaStdinNotArgv(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.txt")
	stdinFile := filepath.Join(dir, "stdin.txt")
	fakeClaude := filepath.Join(dir, "claude")

	script := "#!/bin/sh\n" +
		"printf '%s' \"$*\" > " + argvFile + "\n" +
		"cat > " + stdinFile + "\n" +
		`echo '{"result":"ok","is_error":false}'` + "\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	prompt := strings.Repeat("x", 200_000) // well over MAX_ARG_STRLEN (131,072 bytes)
	result, err := ClaudeCodeInvoker{}.Invoke(context.Background(), prompt)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected result %q, got %q", "ok", result)
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv file: %v", err)
	}
	if strings.Contains(string(argvBytes), prompt) {
		t.Fatalf("prompt was passed as a positional argv element (len %d); it must go via stdin instead", len(prompt))
	}

	stdinBytes, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read stdin file: %v", err)
	}
	if string(stdinBytes) != prompt {
		t.Fatalf("expected the full prompt on stdin (len %d), got len %d", len(prompt), len(stdinBytes))
	}
}

func TestFallbackInvoker_PrimarySuccess(t *testing.T) {
	primary := &stubInvoker{reply: `[{"hall":"fact","content":"a"}]`}
	fallback := &stubInvoker{err: errors.New("fallback should not be called")}
	fallbackCalled := false
	onFallback := func(err error) { fallbackCalled = true }

	invoker := FallbackInvoker{
		Primary:    primary,
		Fallback:   fallback,
		OnFallback: onFallback,
	}

	got, err := invoker.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[{"hall":"fact","content":"a"}]` {
		t.Fatalf("unexpected reply: %q", got)
	}
	if fallbackCalled {
		t.Fatalf("expected OnFallback not to be called")
	}
}

func TestFallbackInvoker_PrimaryFails_FallbackSucceeds(t *testing.T) {
	primary := &stubInvoker{err: errors.New("exit status 1: session limit")}
	fallback := &stubInvoker{reply: `[{"hall":"fact","content":"fallback"}]`}
	var capturedPrimaryErr error
	onFallback := func(err error) { capturedPrimaryErr = err }

	invoker := FallbackInvoker{
		Primary:    primary,
		Fallback:   fallback,
		OnFallback: onFallback,
	}

	got, err := invoker.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[{"hall":"fact","content":"fallback"}]` {
		t.Fatalf("unexpected reply: %q", got)
	}
	if capturedPrimaryErr == nil || !strings.Contains(capturedPrimaryErr.Error(), "session limit") {
		t.Fatalf("expected OnFallback to receive primary error, got: %v", capturedPrimaryErr)
	}
}

func TestFallbackInvoker_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	primary := &stubInvoker{err: errors.New("primary error")}
	fallback := &stubInvoker{err: errors.New("fallback should not be called")}

	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	_, err := invoker.Invoke(ctx, "test prompt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestFallbackInvoker_ContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond) // ensure deadline passed

	primary := &stubInvoker{err: errors.New("primary error")}
	fallback := &stubInvoker{err: errors.New("fallback should not be called")}

	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	_, err := invoker.Invoke(ctx, "test prompt")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestFallbackInvoker_BothFail_ReturnsCombinedError(t *testing.T) {
	primary := &stubInvoker{err: errors.New("quota limit 429")}
	fallback := &stubInvoker{err: errors.New("model timeout")}

	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	_, err := invoker.Invoke(context.Background(), "test prompt")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "quota limit 429") || !strings.Contains(err.Error(), "model timeout") {
		t.Fatalf("expected error to mention both failures, got: %v", err)
	}
}

func TestFallbackInvoker_NilPrimary(t *testing.T) {
	fallback := &stubInvoker{reply: `[{"hall":"fact","content":"from fallback"}]`}
	invoker := FallbackInvoker{
		Primary:  nil,
		Fallback: fallback,
	}

	got, err := invoker.Invoke(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `[{"hall":"fact","content":"from fallback"}]` {
		t.Fatalf("unexpected reply: %q", got)
	}
}

func TestFallbackInvoker_NilFallback(t *testing.T) {
	primary := &stubInvoker{err: errors.New("primary failed")}
	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: nil,
	}

	_, err := invoker.Invoke(context.Background(), "test prompt")
	if err == nil || !strings.Contains(err.Error(), "primary failed") {
		t.Fatalf("expected primary error, got: %v", err)
	}
}

func TestFallbackInvoker_BothNil(t *testing.T) {
	invoker := FallbackInvoker{
		Primary:  nil,
		Fallback: nil,
	}

	_, err := invoker.Invoke(context.Background(), "test prompt")
	if err == nil || !strings.Contains(err.Error(), "no invokers configured") {
		t.Fatalf("expected error about no invokers configured, got: %v", err)
	}
}

func TestFallbackInvoker_ConcurrentRace(t *testing.T) {
	primary := &stubInvoker{err: errors.New("fail")}
	fallback := &stubInvoker{reply: `[]`}
	invoker := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := invoker.Invoke(context.Background(), "test")
			if err != nil {
				t.Errorf("concurrent invocation failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestClaudeCodeInvoker_BuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		invoker  ClaudeCodeInvoker
		wantArgs []string
	}{
		{
			name:     "default empty uses default model haiku",
			invoker:  ClaudeCodeInvoker{},
			wantArgs: []string{"-p", "--output-format", "json", "--safe-mode", "--tools", "", "--model", "haiku"},
		},
		{
			name:     "custom model specifies model flag",
			invoker:  ClaudeCodeInvoker{Model: "claude-3-5-sonnet"},
			wantArgs: []string{"-p", "--output-format", "json", "--safe-mode", "--tools", "", "--model", "claude-3-5-sonnet"},
		},
		{
			name:     "keyword 'default' omits model flag",
			invoker:  ClaudeCodeInvoker{Model: "default"},
			wantArgs: []string{"-p", "--output-format", "json", "--safe-mode", "--tools", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.invoker.buildArgs("")
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("got args %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestAntigravityInvoker_BuildArgs(t *testing.T) {
	tests := []struct {
		name      string
		invoker   AntigravityInvoker
		wantArgs  []string
	}{
		{
			name:     "default empty uses default model and low effort",
			invoker:  AntigravityInvoker{},
			wantArgs: []string{"--output-format", "json", "--disable-slash-commands", "--model", "gemini-3.7-flash-low", "--effort", "low"},
		},
		{
			name:     "custom model and effort",
			invoker:  AntigravityInvoker{Model: "gemini-3.5-flash-low", Effort: "medium"},
			wantArgs: []string{"--output-format", "json", "--disable-slash-commands", "--model", "gemini-3.5-flash-low", "--effort", "medium"},
		},
		{
			name:     "keyword 'default' omits model and effort",
			invoker:  AntigravityInvoker{Model: "default", Effort: "default"},
			wantArgs: []string{"--output-format", "json", "--disable-slash-commands"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.invoker.buildArgs("")
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("got args %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestNopInvoker(t *testing.T) {
	var inv NopInvoker
	_, err := inv.Invoke(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error from NopInvoker, got nil")
	}
	if !strings.Contains(err.Error(), "no active LLM CLI available in PATH") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestClaudeCodeInvoker_BuildArgs_WithSessionID(t *testing.T) {
	inv := ClaudeCodeInvoker{Model: "haiku"}
	args := inv.buildArgs("sess-uuid-1234")
	hasSessionFlag := false
	for i, a := range args {
		if a == "--session-id" && i+1 < len(args) && args[i+1] == "sess-uuid-1234" {
			hasSessionFlag = true
			break
		}
	}
	if !hasSessionFlag {
		t.Fatalf("expected --session-id sess-uuid-1234 in args, got: %v", args)
	}
}

func TestAntigravityInvoker_BuildArgs_WithConversationID(t *testing.T) {
	inv := AntigravityInvoker{Model: "gemini-3.7-flash-low", Effort: "low"}
	args := inv.buildArgs("conv-uuid-5678")
	hasConvFlag := false
	for i, a := range args {
		if a == "--conversation" && i+1 < len(args) && args[i+1] == "conv-uuid-5678" {
			hasConvFlag = true
			break
		}
	}
	if !hasConvFlag {
		t.Fatalf("expected --conversation conv-uuid-5678 in args, got: %v", args)
	}
}

func TestClaudeCodeInvoker_Invoke_WithOptions(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	argvFile := filepath.Join(dir, "argv.txt")
	pwdFile := filepath.Join(dir, "pwd.txt")
	fakeClaude := filepath.Join(dir, "claude")

	script := "#!/bin/sh\n" +
		"printf '%s' \"$*\" > " + argvFile + "\n" +
		"pwd > " + pwdFile + "\n" +
		`echo '{"result":"scoped-ok","is_error":false}'` + "\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	opts := InvokerOptions{
		SessionID: "sess-claude-999",
		WorkDir:   workDir,
	}
	res, err := ClaudeCodeInvoker{}.Invoke(context.Background(), "test prompt", opts)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res != "scoped-ok" {
		t.Fatalf("unexpected result: %q", res)
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	if !strings.Contains(string(argvBytes), "--session-id sess-claude-999") {
		t.Fatalf("expected --session-id in argv, got %s", string(argvBytes))
	}

	pwdBytes, err := os.ReadFile(pwdFile)
	if err != nil {
		t.Fatalf("read pwd: %v", err)
	}
	if strings.TrimSpace(string(pwdBytes)) != workDir {
		t.Fatalf("expected pwd %q, got %q", workDir, strings.TrimSpace(string(pwdBytes)))
	}
}

func TestAntigravityInvoker_Invoke_WithOptions(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	argvFile := filepath.Join(dir, "argv.txt")
	pwdFile := filepath.Join(dir, "pwd.txt")
	fakeAgy := filepath.Join(dir, "agy")

	script := "#!/bin/sh\n" +
		"printf '%s' \"$*\" > " + argvFile + "\n" +
		"pwd > " + pwdFile + "\n" +
		`echo '{"status":"SUCCESS","response":"scoped-agy-ok"}'` + "\n"
	if err := os.WriteFile(fakeAgy, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	opts := InvokerOptions{
		SessionID: "conv-agy-888",
		WorkDir:   workDir,
	}
	res, err := AntigravityInvoker{}.Invoke(context.Background(), "test prompt", opts)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res != "scoped-agy-ok" {
		t.Fatalf("unexpected result: %q", res)
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	if !strings.Contains(string(argvBytes), "--conversation conv-agy-888") {
		t.Fatalf("expected --conversation in argv, got %s", string(argvBytes))
	}

	pwdBytes, err := os.ReadFile(pwdFile)
	if err != nil {
		t.Fatalf("read pwd: %v", err)
	}
	if strings.TrimSpace(string(pwdBytes)) != workDir {
		t.Fatalf("expected pwd %q, got %q", workDir, strings.TrimSpace(string(pwdBytes)))
	}
}

type optionsRecordingInvoker struct {
	recordedOpts []InvokerOptions
	reply        string
	err          error
}

func (o *optionsRecordingInvoker) Invoke(ctx context.Context, prompt string, opts ...InvokerOptions) (string, error) {
	o.recordedOpts = append(o.recordedOpts, opts...)
	return o.reply, o.err
}

func TestSummarizeWithOptions_ForwardsOptions(t *testing.T) {
	stub := &optionsRecordingInvoker{
		reply: `[{"hall":"fact","content":"options forwarded"}]`,
	}
	obs := []observation.Observation{{ToolName: "Bash", Content: "echo 1"}}
	opts := InvokerOptions{SessionID: "sess-123", WorkDir: "/path/to/project"}

	items, err := SummarizeWithOptions(context.Background(), stub, obs, locale.TargetLanguage{Code: "en", Name: "English"}, opts)
	if err != nil {
		t.Fatalf("SummarizeWithOptions: %v", err)
	}
	if len(items) != 1 || items[0].Content != "options forwarded" {
		t.Fatalf("unexpected items: %+v", items)
	}
	if len(stub.recordedOpts) != 1 {
		t.Fatalf("expected 1 recorded option, got %d", len(stub.recordedOpts))
	}
	if stub.recordedOpts[0].SessionID != "sess-123" || stub.recordedOpts[0].WorkDir != "/path/to/project" {
		t.Fatalf("unexpected recorded option: %+v", stub.recordedOpts[0])
	}
}

func TestFallbackInvoker_ForwardsOptions(t *testing.T) {
	primary := &optionsRecordingInvoker{err: errors.New("primary failed")}
	fallback := &optionsRecordingInvoker{reply: `[{"hall":"fact","content":"ok"}]`}
	fb := FallbackInvoker{
		Primary:  primary,
		Fallback: fallback,
	}

	opts := InvokerOptions{SessionID: "sess-fb", WorkDir: "/work/dir"}
	res, err := fb.Invoke(context.Background(), "test prompt", opts)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res != `[{"hall":"fact","content":"ok"}]` {
		t.Fatalf("unexpected reply: %q", res)
	}
	if len(primary.recordedOpts) != 1 || primary.recordedOpts[0].SessionID != "sess-fb" || primary.recordedOpts[0].WorkDir != "/work/dir" {
		t.Fatalf("primary did not receive opts: %+v", primary.recordedOpts)
	}
	if len(fallback.recordedOpts) != 1 || fallback.recordedOpts[0].SessionID != "sess-fb" || fallback.recordedOpts[0].WorkDir != "/work/dir" {
		t.Fatalf("fallback did not receive opts: %+v", fallback.recordedOpts)
	}
}



