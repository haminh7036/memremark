package main

import (
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
