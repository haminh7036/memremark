package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "memremark-hook-claude-sessionstart:", err)
		// Never fail the hook's exit code -- a broken memory feature must
		// not block the user from starting a Claude Code session.
	}
	os.Exit(0)
}

func run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing(cwd)
	if err != nil {
		return err
	}
	summaries, err := store.RecentSummaries(wingID, 10)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(buildOutput(summaries))
}

func buildOutput(summaries []storage.Drawer) hookOutput {
	if len(summaries) == 0 {
		return hookOutput{}
	}
	return hookOutput{
		HookSpecificOutput: &hookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: formatSummaries(summaries),
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
