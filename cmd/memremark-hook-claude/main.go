package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/haminh7036/memremark/internal/hookctx"
	"github.com/haminh7036/memremark/internal/storage"
)

type hookOutput struct {
	HookSpecificOutput *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

func main() {
	summaries, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-claude:", err)
		// Never fail the hook's exit code -- a broken memory feature must
		// not block the user from starting a Claude Code session.
	}
	if err := json.NewEncoder(os.Stdout).Encode(buildOutput(summaries)); err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-claude: failed to encode output:", err)
	}
	os.Exit(0)
}

func run() ([]storage.Drawer, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return hookctx.GetSummaries(cwd, home)
}

func buildOutput(summaries []storage.Drawer) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		HookSpecificOutput: &hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: hookctx.FormatSummaries(summaries),
		},
	}
}
