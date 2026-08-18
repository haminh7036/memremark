// Package hookctx builds the "prior session context" text both memremark
// hook binaries (Claude Code's SessionStart, Antigravity CLI's
// PreInvocation) inject into a fresh session/invocation. The two hooks
// differ only in how they receive the workspace path and how they wrap
// this text into their CLI's own hook-output shape.
package hookctx

import (
	"fmt"
	"path/filepath"

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
// injects into a new session/invocation.
func FormatSummaries(summaries []storage.Drawer) string {
	out := "Bối cảnh từ các phiên làm việc trước (memremark):\n"
	for _, d := range summaries {
		out += fmt.Sprintf("- [%s] %s\n", d.Hall, d.Content)
	}
	return out
}
