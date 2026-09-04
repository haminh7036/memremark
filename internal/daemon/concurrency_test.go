package daemon

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

type blockingInvoker struct {
	concurrentCount int32
	maxConcurrent   int32
}

func (b *blockingInvoker) Invoke(ctx context.Context, prompt string, opts ...summarizer.InvokerOptions) (string, error) {
	curr := atomic.AddInt32(&b.concurrentCount, 1)
	defer atomic.AddInt32(&b.concurrentCount, -1)

	for {
		max := atomic.LoadInt32(&b.maxConcurrent)
		if curr <= max || atomic.CompareAndSwapInt32(&b.maxConcurrent, max, curr) {
			break
		}
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return `[{"hall":"fact","content":"Test fact","narrative":"Test narrative"}]`, nil
	}
}

func TestCLIConcurrencySerialization(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	defer store.Close()

	inv := &blockingInvoker{}
	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), inv, inv, locale.TargetLanguage{Code: "en"})

	// Record observations for two wings
	now := time.Now()
	wing1Dir := filepath.Join(tempDir, "proj1")
	wing2Dir := filepath.Join(tempDir, "proj2")
	_ = d.recordObservation(observation.Observation{WingPath: wing1Dir, SessionID: "sess-1", ToolName: "test", Content: "c1", Timestamp: now}, inv, now)
	_ = d.recordObservation(observation.Observation{WingPath: wing2Dir, SessionID: "sess-2", ToolName: "test", Content: "c2", Timestamp: now}, inv, now)

	wingID1, _ := store.GetOrCreateWing(wing1Dir)
	wingID2, _ := store.GetOrCreateWing(wing2Dir)

	// Run summarize concurrently
	errCh := make(chan error, 2)
	go func() {
		errCh <- d.summarizeWing(context.Background(), wingID1, now.Add(time.Second))
	}()
	go func() {
		errCh <- d.summarizeWing(context.Background(), wingID2, now.Add(time.Second))
	}()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("summarizeWing failed: %v", err)
		}
	}

	if inv.maxConcurrent > 1 {
		t.Fatalf("expected max concurrency 1, got %d", inv.maxConcurrent)
	}
}
