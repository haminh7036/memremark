package daemon

import (
	"os"
	"path/filepath"
)

// DefaultPauseFilePath returns $HOME/.memremark/paused.
func DefaultPauseFilePath() string {
	if env := os.Getenv("MEMREMARK_PAUSE_FILE"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".memremark_paused"
	}
	return filepath.Join(home, ".memremark", "paused")
}

// SetPauseFile overrides the default pause file path (useful for testing).
func (d *Daemon) SetPauseFile(path string) {
	d.pauseMu.Lock()
	defer d.pauseMu.Unlock()
	d.pauseFilePath = path
}

func (d *Daemon) getPauseFile() string {
	d.pauseMu.Lock()
	defer d.pauseMu.Unlock()
	if d.pauseFilePath == "" {
		d.pauseFilePath = DefaultPauseFilePath()
	}
	return d.pauseFilePath
}

// IsPaused returns true if the pause marker file exists on disk.
func (d *Daemon) IsPaused() bool {
	path := d.getPauseFile()
	_, err := os.Stat(path)
	return err == nil
}

// SetPaused creates or deletes the pause file and cancels in-flight work if pausing.
func (d *Daemon) SetPaused(paused bool) error {
	path := d.getPauseFile()
	if paused {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_ = f.Close()
		d.CancelActive()
		return nil
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RegisterActiveCancel records the cancel func of an in-flight operation.
func (d *Daemon) RegisterActiveCancel(cancel func()) {
	d.activeCancelMu.Lock()
	defer d.activeCancelMu.Unlock()
	d.activeCancel = cancel
}

// ClearActiveCancel unsets the cancel func once the operation finishes.
func (d *Daemon) ClearActiveCancel() {
	d.activeCancelMu.Lock()
	defer d.activeCancelMu.Unlock()
	d.activeCancel = nil
}

// CancelActive invokes the active cancel func if one is registered.
func (d *Daemon) CancelActive() {
	d.activeCancelMu.Lock()
	cancel := d.activeCancel
	d.activeCancel = nil
	d.activeCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
