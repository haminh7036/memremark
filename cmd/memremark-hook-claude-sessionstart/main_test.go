package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestFormatSummariesIncludesHallAndContent(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: storage.HallFact, Content: "chose SQLite for v1"},
		{Hall: storage.HallDiscovery, Content: "agy hooks don't execute"},
	}
	out := formatSummaries(summaries)
	if !strings.Contains(out, "chose SQLite for v1") {
		t.Fatalf("expected output to include first summary, got %q", out)
	}
	if !strings.Contains(out, "agy hooks don't execute") {
		t.Fatalf("expected output to include second summary, got %q", out)
	}
}

func TestBuildOutputOmitsHookSpecificOutputWhenNoSummaries(t *testing.T) {
	out := buildOutput(nil)
	if out.HookSpecificOutput != nil {
		t.Fatalf("expected nil HookSpecificOutput for no summaries, got %+v", out.HookSpecificOutput)
	}
}

func TestBuildOutputSetsHookEventNameAndContext(t *testing.T) {
	out := buildOutput([]storage.Drawer{{Hall: storage.HallFact, Content: "x"}})
	if out.HookSpecificOutput == nil {
		t.Fatalf("expected non-nil HookSpecificOutput")
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("expected hookEventName SessionStart, got %q", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "x") {
		t.Fatalf("expected additionalContext to include the summary content, got %q", out.HookSpecificOutput.AdditionalContext)
	}
}

func TestBuildOutputNilOmitsHookSpecificOutputFromJSON(t *testing.T) {
	out := buildOutput(nil)
	bytes, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("buildOutput(nil) should produce valid JSON: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(bytes, &result); err != nil {
		t.Fatalf("JSON should be valid and unmarshallable: %v", err)
	}

	if _, has := result["hookSpecificOutput"]; has {
		t.Fatalf("hookSpecificOutput should be omitted from JSON when nil, but it's present in: %s", string(bytes))
	}
}

func TestGetSummariesReturnsNilOnStorageOpenError(t *testing.T) {
	// Pass a path where storage.Open will fail
	summaries, err := getSummaries(".", "/nonexistent/1234567890/memremark.db")
	if err == nil {
		t.Fatalf("expected error when storage path is broken, got nil")
	}
	if summaries != nil {
		t.Fatalf("expected nil summaries on error, got %v", summaries)
	}
}

func TestMainWritesValidJSONAndExitsZeroOnStorageFailure(t *testing.T) {
	// Build the binary to a temp file first
	tmpBinary := filepath.Join(t.TempDir(), "memremark-hook-test")
	buildCmd := exec.Command("go", "build", "-o", tmpBinary, ".")
	buildCmd.Dir = "."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build binary: %v", err)
	}

	// Force storage.Open's os.MkdirAll to fail regardless of the test
	// runner's privileges: pointing HOME at a merely-unwritable path (e.g.
	// under /) only fails for a non-root user -- under root (common in
	// container CI), MkdirAll would succeed and this test would pass for
	// the wrong reason (empty summaries, not a real storage failure).
	// Instead, put a regular *file* where a directory component of
	// ~/.memremark needs to be: creating a directory entry under a file is
	// an ENOTDIR error at the filesystem level, which no uid can bypass.
	blockerFile := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blockerFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	fakeHome := filepath.Join(blockerFile, "sub")

	// Run the actual binary with HOME set to a path that can never become a directory
	cmd := exec.Command(tmpBinary)
	env := os.Environ()
	env = append(env, "HOME="+fakeHome)
	cmd.Env = env

	output, err := cmd.Output()

	// Should exit with code 0 (the hook must not fail the session)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 0 {
				t.Fatalf("expected exit code 0 even on storage failure, got %d", exitErr.ExitCode())
			}
		} else {
			t.Fatalf("cmd.Output() failed unexpectedly: %v", err)
		}
	}

	// Verify stdout is valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("stdout should be valid JSON on storage failure, got %q (error: %v)", string(output), err)
	}

	// Verify hookSpecificOutput is absent (since no storage exists to read summaries)
	if _, has := result["hookSpecificOutput"]; has {
		t.Fatalf("expected hookSpecificOutput to be absent from JSON on storage failure, but found it in: %s", string(output))
	}
}
