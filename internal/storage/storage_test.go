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

// TestOpenSetsIncrementalAutoVacuum confirms Open enables
// auto_vacuum=INCREMENTAL (mode 2) on a fresh database, so pruned drawer
// rows (see daemon_summarize.go) can actually shrink the file via
// IncrementalVacuum instead of leaving unreclaimed free pages behind.
func TestOpenSetsIncrementalAutoVacuum(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var mode int
	if err := s.db.QueryRow("PRAGMA auto_vacuum;").Scan(&mode); err != nil {
		t.Fatalf("query auto_vacuum: %v", err)
	}
	if mode != 2 {
		t.Fatalf("expected auto_vacuum mode 2 (incremental), got %d", mode)
	}
}

// TestOpenMigratesExistingDBToIncrementalAutoVacuum confirms a database
// created before this migration (auto_vacuum mode 0, the SQLite default)
// gets upgraded to incremental on the next Open, not just databases created
// fresh after this change shipped.
func TestOpenMigratesExistingDBToIncrementalAutoVacuum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memremark.db")

	legacy, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := legacy.db.Exec("PRAGMA auto_vacuum = NONE; VACUUM;"); err != nil {
		t.Fatalf("force legacy auto_vacuum=NONE: %v", err)
	}
	var mode int
	if err := legacy.db.QueryRow("PRAGMA auto_vacuum;").Scan(&mode); err != nil {
		t.Fatalf("query auto_vacuum: %v", err)
	}
	if mode != 0 {
		t.Fatalf("test setup: expected auto_vacuum 0 before migration, got %d", mode)
	}
	legacy.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (should migrate): %v", err)
	}
	defer s.Close()

	if err := s.db.QueryRow("PRAGMA auto_vacuum;").Scan(&mode); err != nil {
		t.Fatalf("query auto_vacuum after migration: %v", err)
	}
	if mode != 2 {
		t.Fatalf("expected auto_vacuum mode 2 (incremental) after migration, got %d", mode)
	}
}

// TestIncrementalVacuumRuns confirms the wrapper executes without error --
// it's a thin PRAGMA wrapper, so this just guards against a typo'd pragma
// name or a broken connection breaking every summarize call.
func TestIncrementalVacuumRuns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.IncrementalVacuum(); err != nil {
		t.Fatalf("IncrementalVacuum: %v", err)
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
