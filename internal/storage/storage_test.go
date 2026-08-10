package storage

import (
	"path/filepath"
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
