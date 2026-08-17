package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/storage"
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
