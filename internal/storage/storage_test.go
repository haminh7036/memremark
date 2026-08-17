package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenCreatesSchemaAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "memremark.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
}

func TestOpenIsIdempotentOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memremark.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open on existing file: %v", err)
	}
	defer s2.Close()
}

// TestOpenSetsBusyTimeout confirms Open configures a non-zero busy_timeout
// so a concurrent writer waits briefly instead of failing immediately with
// "database is locked".
func TestOpenSetsBusyTimeout(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var got int
	if err := s.db.QueryRow("PRAGMA busy_timeout;").Scan(&got); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if got != 5000 {
		t.Fatalf("expected busy_timeout 5000, got %d", got)
	}
}

// TestOpenSetsRestrictivePermissions asserts the private-activity-log
// database ends up with a private directory (0700) and a private file
// (0600), not the driver/OS defaults.
func TestOpenSetsRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits don't apply on windows")
	}

	dir := filepath.Join(t.TempDir(), "sub")
	path := filepath.Join(dir, "memremark.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("expected dir mode 0700, got %o", perm)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", perm)
	}
}
