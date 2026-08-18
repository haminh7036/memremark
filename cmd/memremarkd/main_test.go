package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// blockingInvoker mimics exec.CommandContext's real behavior for a hung
// headless CLI call: it never returns on its own, only when the context
// passed to it is done (as if the subprocess had just been killed by the
// context's deadline).
type blockingInvoker struct{}

func (blockingInvoker) Invoke(ctx context.Context, prompt string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// TestPollBoundsAHungInvokerToTheGivenTimeout is the regression test for
// Critical 1: before the fix, main.go passed one context.Background() (which
// never times out) into every PollOnce call for the process's whole
// lifetime, so a single hung `claude -p`/`agy -p` call would wedge the
// daemon forever. poll() must derive a fresh timeout context per call instead
// -- this test proves a poll cycle returns within a bounded time even when
// given an invoker that would otherwise block forever.
func TestPollBoundsAHungInvokerToTheGivenTimeout(t *testing.T) {
	dir := t.TempDir()
	projectsRoot := filepath.Join(dir, "claude-projects")
	projectDir := filepath.Join(projectsRoot, "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	transcript := `{"type":"assistant","sessionId":"sess-1","cwd":"/tmp/project","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","sessionId":"sess-1","cwd":"/tmp/project","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"README.md","is_error":false}]}}
`
	if err := os.WriteFile(filepath.Join(projectDir, "sess-1.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	// Never created: pollAntigravity must tolerate this (Important 4) rather
	// than error, so it isn't what's timing out this test.
	missingAntigravityDB := filepath.Join(dir, "antigravity", "conversation_summaries.db")

	d := daemon.New(store, projectsRoot, missingAntigravityDB, blockingInvoker{}, blockingInvoker{})

	base := time.Now()
	if err := poll(context.Background(), d, base, time.Minute); err != nil {
		t.Fatalf("first poll (capture verbatim): %v", err)
	}

	const shortTimeout = 100 * time.Millisecond
	start := time.Now()
	// Session is now idle: this tick's summarization attempt invokes the
	// blocking invoker. Without a per-tick timeout this call never returns.
	if err := poll(context.Background(), d, base.Add(10*time.Second), shortTimeout); err != nil {
		t.Fatalf("second poll (summarize, hung invoker): %v", err)
	}
	elapsed := time.Since(start)

	const bound = 5 * time.Second // generous margin over shortTimeout; would be ~forever pre-fix
	if elapsed > bound {
		t.Fatalf("poll took %v, expected it to return within %v of the %v timeout", elapsed, bound, shortTimeout)
	}
}

func TestConfigWiringAndInvokerSetup(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".memremark")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	customConfig := `{
		"summarizer": {
			"claude_model": "claude-3-5-haiku-custom",
			"antigravity_model": "gemini-custom",
			"antigravity_effort": "medium"
		}
	}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(customConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	if cfg.Summarizer.ClaudeModel != "claude-3-5-haiku-custom" {
		t.Errorf("expected claude_model custom, got %s", cfg.Summarizer.ClaudeModel)
	}
	if cfg.Summarizer.AntigravityModel != "gemini-custom" {
		t.Errorf("expected antigravity_model custom, got %s", cfg.Summarizer.AntigravityModel)
	}
	if cfg.Summarizer.AntigravityEffort != "medium" {
		t.Errorf("expected antigravity_effort medium, got %s", cfg.Summarizer.AntigravityEffort)
	}

	store, err := storage.Open(filepath.Join(dir, "memremark.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	claudePrimary := summarizer.ClaudeCodeInvoker{
		Model: cfg.Summarizer.ClaudeModel,
	}
	antigravityPrimary := summarizer.AntigravityInvoker{
		Model:  cfg.Summarizer.AntigravityModel,
		Effort: cfg.Summarizer.AntigravityEffort,
	}

	claudeInvoker := summarizer.FallbackInvoker{
		Primary:  claudePrimary,
		Fallback: antigravityPrimary,
	}

	antigravityInvoker := summarizer.FallbackInvoker{
		Primary:  antigravityPrimary,
		Fallback: claudePrimary,
	}

	d := daemon.New(store, filepath.Join(dir, "claude"), filepath.Join(dir, "agy.db"), claudeInvoker, antigravityInvoker)
	if d == nil {
		t.Fatal("expected non-nil daemon instance")
	}
}

func TestResolveInvokers_Matrix(t *testing.T) {
	cfg := config.DefaultConfig()

	tests := []struct {
		name         string
		hasClaude    bool
		hasAgy       bool
		wantSummary  string
		checkInvoker func(t *testing.T, setup InvokerSetup)
	}{
		{
			name:        "both claude and agy available",
			hasClaude:   true,
			hasAgy:      true,
			wantSummary: "active invokers: claude (primary/fallback) + agy (primary/fallback)",
			checkInvoker: func(t *testing.T, setup InvokerSetup) {
				_, ok1 := setup.ClaudeInvoker.(summarizer.FallbackInvoker)
				_, ok2 := setup.AntigravityInvoker.(summarizer.FallbackInvoker)
				if !ok1 || !ok2 {
					t.Fatalf("expected FallbackInvokers for both, got %T, %T", setup.ClaudeInvoker, setup.AntigravityInvoker)
				}
			},
		},
		{
			name:        "claude only available",
			hasClaude:   true,
			hasAgy:      false,
			wantSummary: "active invokers: claude only (agy not found in PATH)",
			checkInvoker: func(t *testing.T, setup InvokerSetup) {
				_, ok1 := setup.ClaudeInvoker.(summarizer.ClaudeCodeInvoker)
				_, ok2 := setup.AntigravityInvoker.(summarizer.ClaudeCodeInvoker)
				if !ok1 || !ok2 {
					t.Fatalf("expected ClaudeCodeInvoker for both, got %T, %T", setup.ClaudeInvoker, setup.AntigravityInvoker)
				}
			},
		},
		{
			name:        "agy only available",
			hasClaude:   false,
			hasAgy:      true,
			wantSummary: "active invokers: agy only (claude not found in PATH)",
			checkInvoker: func(t *testing.T, setup InvokerSetup) {
				_, ok1 := setup.ClaudeInvoker.(summarizer.AntigravityInvoker)
				_, ok2 := setup.AntigravityInvoker.(summarizer.AntigravityInvoker)
				if !ok1 || !ok2 {
					t.Fatalf("expected AntigravityInvoker for both, got %T, %T", setup.ClaudeInvoker, setup.AntigravityInvoker)
				}
			},
		},
		{
			name:        "neither CLI available",
			hasClaude:   false,
			hasAgy:      false,
			wantSummary: "warning: neither claude nor agy found in PATH; summarization disabled",
			checkInvoker: func(t *testing.T, setup InvokerSetup) {
				_, ok1 := setup.ClaudeInvoker.(summarizer.NopInvoker)
				_, ok2 := setup.AntigravityInvoker.(summarizer.NopInvoker)
				if !ok1 || !ok2 {
					t.Fatalf("expected NopInvoker for both, got %T, %T", setup.ClaudeInvoker, setup.AntigravityInvoker)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLookPath := func(file string) (string, error) {
				if file == "claude" && tt.hasClaude {
					return "/usr/bin/claude", nil
				}
				if file == "agy" && tt.hasAgy {
					return "/usr/bin/agy", nil
				}
				return "", os.ErrNotExist
			}

			setup := resolveInvokers(cfg, mockLookPath)
			if setup.Summary != tt.wantSummary {
				t.Errorf("got summary %q, want %q", setup.Summary, tt.wantSummary)
			}
			tt.checkInvoker(t, setup)
		})
	}
}

