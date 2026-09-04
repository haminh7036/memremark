package summarizer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupTermination(t *testing.T) {
	// A mock command that creates a background child and sleeps
	// We verify that cancelling the context terminates the entire process group
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 10 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	start := time.Now()
	_ = cmd.Run()
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Fatalf("process group was not killed promptly on timeout, took %v", elapsed)
	}
}

func TestClaudeCodeInvoker_CancellationTerminatesProcessGroup(t *testing.T) {
	dir := t.TempDir()
	fakeClaude := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nsleep 10 &\nwait\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := ClaudeCodeInvoker{}.Invoke(ctx, "test")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("process group was not killed promptly on timeout, took %v", elapsed)
	}
}

func TestAntigravityInvoker_CancellationTerminatesProcessGroup(t *testing.T) {
	dir := t.TempDir()
	fakeAgy := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nsleep 10 &\nwait\n"
	if err := os.WriteFile(fakeAgy, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := AntigravityInvoker{}.Invoke(ctx, "test")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("process group was not killed promptly on timeout, took %v", elapsed)
	}
}
