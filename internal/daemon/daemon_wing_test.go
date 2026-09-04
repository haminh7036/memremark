package daemon

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
)

func TestDaemonRecordObservationTouchesWing(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	d := New(store, tempDir, filepath.Join(tempDir, "conv.db"), nil, nil, locale.TargetLanguage{Code: "en"})

	now := time.Now()
	obs1 := observation.Observation{WingPath: tempDir, SessionID: "sess-1", ToolName: "bash", Content: "echo 1", Timestamp: now}
	obs2 := observation.Observation{WingPath: tempDir, SessionID: "sess-2", ToolName: "edit", Content: "edit 2", Timestamp: now.Add(time.Second)}

	_ = d.recordObservation(obs1, nil, now)
	_ = d.recordObservation(obs2, nil, now.Add(time.Second))

	wingID, _ := store.GetOrCreateWing(tempDir)
	wingKey := strconv.FormatInt(wingID, 10)

	// Since both sessions share the wing, only the wing should be due
	due := d.Tracker.Due(now.Add(50*time.Second), idleWindow)
	if len(due) != 1 || due[0] != wingKey {
		t.Fatalf("expected 1 due wing (%s), got %v", wingKey, due)
	}
}
