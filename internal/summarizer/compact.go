package summarizer

import (
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

// MaxWorkspacePromptBytes limits the payload budget sent to the LLM to 70 KB,
// comfortably below Linux's 128 KiB MAX_ARG_STRLEN.
const MaxWorkspacePromptBytes = 70_000

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// CleanANSI removes ANSI terminal color and control escape sequences.
func CleanANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// TruncateContent limits content to maxLen characters by keeping the head
// and tail and placing a truncation marker in between.
func TruncateContent(content string, maxLen int) string {
	cleaned := CleanANSI(content)
	if len(cleaned) <= maxLen || maxLen < 200 {
		return cleaned
	}
	half := maxLen / 2
	for half > 0 && !utf8.RuneStart(cleaned[half]) {
		half--
	}
	tailStart := len(cleaned) - (maxLen / 2)
	for tailStart < len(cleaned) && !utf8.RuneStart(cleaned[tailStart]) {
		tailStart++
	}
	omitted := tailStart - half
	return fmt.Sprintf("%s\n... [truncated %d bytes] ...\n%s", cleaned[:half], omitted, cleaned[tailStart:])
}

// CompactAndBudget pre-compacts raw verbatim drawers and selects the longest
// chronological prefix that fits within maxBytes. It always includes at least
// the first drawer even if it exceeds the budget alone.
func CompactAndBudget(drawers []storage.Drawer, maxBytes int) ([]storage.Drawer, []observation.Observation) {
	if len(drawers) == 0 {
		return nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = MaxWorkspacePromptBytes
	}

	var batch []storage.Drawer
	var obs []observation.Observation
	totalBytes := 0

	for i, d := range drawers {
		compacted := TruncateContent(d.Content, 800)
		entrySize := len(compacted) + len(d.ToolName) + 32

		if i > 0 && totalBytes+entrySize > maxBytes {
			break
		}

		batch = append(batch, d)
		obs = append(obs, observation.Observation{
			SessionID: d.SessionID,
			ToolName:  d.ToolName,
			Content:   compacted,
			Timestamp: d.CreatedAt,
		})
		totalBytes += entrySize
	}

	return batch, obs
}
