package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/hookctx"
	"github.com/haminh7036/memremark/internal/locale"
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
	summaries, targetLang, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-agy:", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(buildOutput(summaries, targetLang)); err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-agy: failed to encode output:", err)
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

func run() ([]storage.Drawer, locale.TargetLanguage, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, locale.TargetLanguage{}, err
	}
	in, err := parseInput(data)
	if err != nil {
		return nil, locale.TargetLanguage{}, err
	}
	// Fast path: only inject context at session start (first model call).
	if in.InvocationNum != 0 {
		return nil, locale.TargetLanguage{}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, locale.TargetLanguage{}, err
	}
	cfg, _ := config.Load(home)
	targetLang := locale.DetectLanguage(cfg.Language)
	summaries, err := hookctx.GetSummaries(in.WorkspacePaths[0], home)
	return summaries, targetLang, err
}

func buildOutput(summaries []storage.Drawer, targetLang locale.TargetLanguage) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		InjectSteps: []injectStep{
			{EphemeralMessage: hookctx.FormatSummaries(summaries, targetLang)},
		},
	}
}
