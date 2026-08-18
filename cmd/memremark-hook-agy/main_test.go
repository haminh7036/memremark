package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
)

func TestBuildOutput_WithSummaries(t *testing.T) {
	summaries := []storage.Drawer{
		{Hall: "fact", Content: "minimum Go version is 1.26"},
		{Hall: "discovery", Content: "WAL mode needed"},
	}
	out := buildOutput(summaries, locale.TargetLanguage{Code: "vi", Name: "Vietnamese"})
	if len(out.InjectSteps) != 1 {
		t.Fatalf("expected 1 inject step, got %d", len(out.InjectSteps))
	}
	if out.InjectSteps[0].EphemeralMessage == "" {
		t.Fatal("expected non-empty ephemeral message")
	}
}

func TestBuildOutput_Empty(t *testing.T) {
	out := buildOutput(nil, locale.TargetLanguage{})
	if out.InjectSteps != nil {
		t.Fatalf("expected nil InjectSteps, got %v", out.InjectSteps)
	}

	b, _ := json.Marshal(out)
	if string(b) != "{}" {
		t.Fatalf("expected {}, got %s", string(b))
	}
}

func TestParseInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNum int
		wantWS  string
		wantErr bool
	}{
		{
			name:    "valid first invocation",
			input:   `{"invocationNum":0,"workspacePaths":["/workspace/project"]}`,
			wantNum: 0,
			wantWS:  "/workspace/project",
		},
		{
			name:    "later invocation",
			input:   `{"invocationNum":3,"workspacePaths":["/workspace/project"]}`,
			wantNum: 3,
			wantWS:  "/workspace/project",
		},
		{
			name:    "empty workspace paths",
			input:   `{"invocationNum":0,"workspacePaths":[]}`,
			wantErr: true,
		},
		{
			name:    "malformed json",
			input:   `{invalid`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := parseInput([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if in.InvocationNum != tt.wantNum {
				t.Fatalf("invocationNum: want %d, got %d", tt.wantNum, in.InvocationNum)
			}
			if in.WorkspacePaths[0] != tt.wantWS {
				t.Fatalf("workspacePaths[0]: want %s, got %s", tt.wantWS, in.WorkspacePaths[0])
			}
		})
	}
}

func TestRun_SkipsNonZeroInvocation(t *testing.T) {
	// run() with invocationNum > 0 should return nil summaries without touching DB
	old := os.Stdin
	defer func() { os.Stdin = old }()

	r, w, _ := os.Pipe()
	w.Write([]byte(`{"invocationNum":5,"workspacePaths":["/tmp/nonexistent"]}`))
	w.Close()
	os.Stdin = r

	summaries, _, err := run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summaries != nil {
		t.Fatalf("expected nil summaries for invocationNum > 0, got %v", summaries)
	}
}
