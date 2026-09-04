package summarizer

import (
	"strings"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

func TestCleanANSI(t *testing.T) {
	input := "\x1b[31mRed Text\x1b[0m \x1b[1;32mGreen Bold\x1b[0m"
	expected := "Red Text Green Bold"
	got := CleanANSI(input)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestTruncateContent(t *testing.T) {
	short := "short text"
	if got := TruncateContent(short, 800); got != short {
		t.Fatalf("expected %q, got %q", short, got)
	}

	long := strings.Repeat("A", 500) + strings.Repeat("B", 500)
	got := TruncateContent(long, 800)
	if len(got) >= len(long) {
		t.Fatalf("expected truncated length < %d, got %d", len(long), len(got))
	}
	if !strings.HasPrefix(got, strings.Repeat("A", 400)) {
		t.Fatal("expected head to be preserved")
	}
	if !strings.HasSuffix(got, strings.Repeat("B", 400)) {
		t.Fatal("expected tail to be preserved")
	}
	if !strings.Contains(got, "[truncated") {
		t.Fatal("expected truncation indicator")
	}
}

func TestCompactAndBudget(t *testing.T) {
	now := time.Now()
	drawers := []storage.Drawer{
		{ID: 1, ToolName: "bash", Content: strings.Repeat("x", 1000), CreatedAt: now},
		{ID: 2, ToolName: "edit", Content: strings.Repeat("y", 1000), CreatedAt: now.Add(time.Second)},
		{ID: 3, ToolName: "read", Content: strings.Repeat("z", 1000), CreatedAt: now.Add(2 * time.Second)},
	}

	// Budget that fits only 2 truncated drawers (~800 chars each + metadata)
	batch, obs := CompactAndBudget(drawers, 2000)
	if len(batch) != 2 {
		t.Fatalf("expected 2 drawers in batch, got %d", len(batch))
	}
	if len(obs) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obs))
	}
	if batch[0].ID != 1 || batch[1].ID != 2 {
		t.Fatalf("unexpected batch IDs: %v", batch)
	}
	if len(obs[0].Content) > 850 {
		t.Fatalf("expected observation content to be truncated, got len %d", len(obs[0].Content))
	}
}

func TestCompactAndBudget_EdgeCases(t *testing.T) {
	// Empty drawers
	batch, obs := CompactAndBudget(nil, 2000)
	if batch != nil || obs != nil {
		t.Fatalf("expected nil batch and obs for empty input, got batch=%v, obs=%v", batch, obs)
	}

	batch, obs = CompactAndBudget([]storage.Drawer{}, 2000)
	if batch != nil || obs != nil {
		t.Fatalf("expected nil batch and obs for empty slice, got batch=%v, obs=%v", batch, obs)
	}

	// Single drawer exceeding budget alone must still be included
	singleHuge := []storage.Drawer{
		{ID: 10, ToolName: "bash", Content: strings.Repeat("h", 5000)},
	}
	batch, obs = CompactAndBudget(singleHuge, 100)
	if len(batch) != 1 || len(obs) != 1 {
		t.Fatalf("expected first drawer to always be included even if exceeding budget, got batch=%d obs=%d", len(batch), len(obs))
	}
	if batch[0].ID != 10 {
		t.Fatalf("expected drawer ID 10, got %d", batch[0].ID)
	}

	// maxBytes <= 0 defaults to MaxWorkspacePromptBytes
	batch, obs = CompactAndBudget(singleHuge, 0)
	if len(batch) != 1 || len(obs) != 1 {
		t.Fatalf("expected default budget to include single drawer, got batch=%d obs=%d", len(batch), len(obs))
	}

	batch, obs = CompactAndBudget(singleHuge, -1)
	if len(batch) != 1 || len(obs) != 1 {
		t.Fatalf("expected negative budget to default and include drawer, got batch=%d obs=%d", len(batch), len(obs))
	}
}

func TestTruncateContent_EdgeCases(t *testing.T) {
	// maxLen < 200 should return cleaned content without truncation
	content := strings.Repeat("A", 150)
	got := TruncateContent(content, 100)
	if got != content {
		t.Fatalf("expected content unchanged for maxLen < 200, got len %d", len(got))
	}

	// ANSI stripping in TruncateContent
	ansiContent := "\x1b[33m" + strings.Repeat("A", 500) + "\x1b[0m" + strings.Repeat("B", 500)
	truncated := TruncateContent(ansiContent, 800)
	if strings.Contains(truncated, "\x1b") {
		t.Fatal("expected ANSI codes to be stripped in TruncateContent")
	}
	if !strings.HasPrefix(truncated, strings.Repeat("A", 400)) {
		t.Fatal("expected head to be preserved after ANSI stripping")
	}
	if !strings.HasSuffix(truncated, strings.Repeat("B", 400)) {
		t.Fatal("expected tail to be preserved after ANSI stripping")
	}
}
