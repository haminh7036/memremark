package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
)

func TestDaemonPauseResume(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	defer store.Close()

	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), nil, nil, locale.TargetLanguage{Code: "en"})
	pauseFile := filepath.Join(tempDir, "paused")
	d.SetPauseFile(pauseFile)

	if d.IsPaused() {
		t.Fatal("expected daemon not to be paused initially")
	}

	if err := d.SetPaused(true); err != nil {
		t.Fatalf("failed to pause daemon: %v", err)
	}

	if !d.IsPaused() {
		t.Fatal("expected daemon to be paused after SetPaused(true)")
	}

	if _, err := os.Stat(pauseFile); os.IsNotExist(err) {
		t.Fatal("expected pause file to exist")
	}

	// In PollOnce, should immediately return nil without action
	now := time.Now()
	if err := d.PollOnce(context.Background(), now); err != nil {
		t.Fatalf("PollOnce when paused returned error: %v", err)
	}

	if err := d.SetPaused(false); err != nil {
		t.Fatalf("failed to resume daemon: %v", err)
	}

	if d.IsPaused() {
		t.Fatal("expected daemon not to be paused after SetPaused(false)")
	}
}

func TestDaemonActiveCancel(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	defer store.Close()

	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), nil, nil, locale.TargetLanguage{Code: "en"})
	pauseFile := filepath.Join(tempDir, "paused")
	d.SetPauseFile(pauseFile)

	cancelled := false
	cancelFunc := func() {
		cancelled = true
	}

	d.RegisterActiveCancel(cancelFunc)
	d.CancelActive()

	if !cancelled {
		t.Fatal("expected CancelActive to invoke registered cancel func")
	}

	// Calling CancelActive again should be a no-op (already cleared)
	cancelled = false
	d.CancelActive()
	if cancelled {
		t.Fatal("expected CancelActive to be a no-op when no cancel func is registered")
	}

	// Test ClearActiveCancel
	cancelled = false
	d.RegisterActiveCancel(cancelFunc)
	d.ClearActiveCancel()
	d.CancelActive()
	if cancelled {
		t.Fatal("expected ClearActiveCancel to unset the active cancel func")
	}

	// Test SetPaused(true) triggers CancelActive
	cancelled = false
	d.RegisterActiveCancel(cancelFunc)
	if err := d.SetPaused(true); err != nil {
		t.Fatalf("SetPaused(true) failed: %v", err)
	}
	if !cancelled {
		t.Fatal("expected SetPaused(true) to cancel active operation")
	}
}

func TestDefaultPauseFilePath_EnvOverride(t *testing.T) {
	customPath := "/tmp/custom-memremark-paused"
	t.Setenv("MEMREMARK_PAUSE_FILE", customPath)
	if got := DefaultPauseFilePath(); got != customPath {
		t.Fatalf("expected %q, got %q", customPath, got)
	}
}

func TestDaemonStartPauseWatcher(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	defer store.Close()

	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), nil, nil, locale.TargetLanguage{Code: "en"})
	pauseFile := filepath.Join(tempDir, "paused")
	d.SetPauseFile(pauseFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.StartPauseWatcher(ctx, 10*time.Millisecond)

	var cancelled atomic.Bool
	d.RegisterActiveCancel(func() {
		cancelled.Store(true)
	})

	// Simulate external CLI creating the pause file directly on disk
	f, err := os.Create(pauseFile)
	if err != nil {
		t.Fatalf("create pause file: %v", err)
	}
	_ = f.Close()

	// Wait up to 500ms for the watcher to trigger active cancel
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cancelled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !cancelled.Load() {
		t.Fatal("expected StartPauseWatcher to trigger active cancel when pause file is created externally")
	}
}


