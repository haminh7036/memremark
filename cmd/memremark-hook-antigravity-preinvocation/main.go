package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/haminh7036/memremark/internal/hookctx"
	"github.com/haminh7036/memremark/internal/storage"
)

type hookInput struct {
	InvocationNum  int      `json:"invocationNum"`
	WorkspacePaths []string `json:"workspacePaths"`
}

type hookOutput struct {
	InjectSteps []injectStep `json:"injectSteps,omitempty"`
}

type injectStep struct {
	EphemeralMessage string `json:"ephemeralMessage,omitempty"`
}

func main() {
	summaries, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-antigravity-preinvocation:", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(buildOutput(summaries)); err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-antigravity-preinvocation: failed to encode output:", err)
	}
	os.Exit(0)
}

func parseInput(data []byte) (hookInput, error) {
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return hookInput{}, fmt.Errorf("parse stdin: %w", err)
	}
	if len(in.WorkspacePaths) == 0 {
		return hookInput{}, fmt.Errorf("workspacePaths is empty")
	}
	return in, nil
}

func run() ([]storage.Drawer, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	in, err := parseInput(data)
	if err != nil {
		return nil, err
	}
	// Fast path: only inject context at session start (first model call).
	if in.InvocationNum != 0 {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return hookctx.GetSummaries(in.WorkspacePaths[0], home)
}

func buildOutput(summaries []storage.Drawer) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		InjectSteps: []injectStep{
			{EphemeralMessage: hookctx.FormatSummaries(summaries)},
		},
	}
}
