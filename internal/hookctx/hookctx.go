// Package hookctx builds the "prior session context" text both memremark
// hook binaries (Claude Code's SessionStart, Antigravity CLI's
// PreInvocation) inject into a fresh session/invocation. The two hooks
// differ only in how they receive the workspace path and how they wrap
// this text into their CLI's own hook-output shape.
package hookctx

import (
	"fmt"
	"path/filepath"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
)

// GetSummaries opens the memremark DB under home, resolves the wing for
// workspacePath, and returns its 10 most recent summary drawers.
func GetSummaries(workspacePath, home string) ([]storage.Drawer, error) {
	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		return nil, err
	}
	defer store.Close()

	wingID, err := store.GetOrCreateWing(workspacePath)
	if err != nil {
		return nil, err
	}
	return store.RecentSummaries(wingID, 10)
}

// FormatSummaries renders summaries as the ephemeral context text a hook
// injects into a new session/invocation, formatted according to the target language.
func FormatSummaries(summaries []storage.Drawer, lang ...locale.TargetLanguage) string {
	header := "Context from prior sessions (memremark):\n"
	if len(lang) > 0 {
		switch lang[0].Code {
		case "vi":
			header = "Bối cảnh từ các phiên làm việc trước (memremark):\n"
		case "ja":
			header = "過去のセッションからのコンテキスト (memremark):\n"
		case "zh":
			header = "来自先前会话的上下文 (memremark):\n"
		default:
			header = "Context from prior sessions (memremark):\n"
		}
	}
	out := header
	for _, d := range summaries {
		out += fmt.Sprintf("- [%s] %s\n", d.Hall, d.Content)
	}
	return out
}
