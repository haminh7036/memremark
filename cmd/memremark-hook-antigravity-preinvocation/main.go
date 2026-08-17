package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	if summaries == nil {
		summaries = []storage.Drawer{}
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
	return getSummaries(in.WorkspacePaths[0], home)
}

func getSummaries(workspacePath, home string) ([]storage.Drawer, error) {
	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		return nil, err
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing(workspacePath)
	if err != nil {
		return nil, err
	}
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

func buildOutput(summaries []storage.Drawer) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		InjectSteps: []injectStep{
			{EphemeralMessage: formatSummaries(summaries)},
		},
	}
}

func formatSummaries(summaries []storage.Drawer) string {
	out := "Bối cảnh từ các phiên làm việc trước (memremark):\n"
	for _, d := range summaries {
		out += fmt.Sprintf("- [%s] %s\n", d.Hall, d.Content)
	}
	return out
}
