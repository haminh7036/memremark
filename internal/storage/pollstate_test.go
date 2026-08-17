package storage

import (
	"path/filepath"
	"testing"
)

func TestGetPollStateReturnsFalseForNeverSetKey(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	value, ok, err := s.GetPollState("claudecode:file:/tmp/never-set.jsonl")
	if err != nil {
		t.Fatalf("GetPollState: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for a never-set key, got value %d", value)
	}
}

func TestSetPollStateThenGetPollStateRoundTrips(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.SetPollState("claudecode:file:/tmp/a.jsonl", 42); err != nil {
		t.Fatalf("SetPollState: %v", err)
	}

	value, ok, err := s.GetPollState("claudecode:file:/tmp/a.jsonl")
	if err != nil {
		t.Fatalf("GetPollState: %v", err)
	}
	if !ok || value != 42 {
		t.Fatalf("expected (42, true), got (%d, %v)", value, ok)
	}
}

func TestSetPollStateOnExistingKeyOverwrites(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memremark.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const key = "antigravity:conv:conv-1"
	if err := s.SetPollState(key, 5); err != nil {
		t.Fatalf("SetPollState (first): %v", err)
	}
	if err := s.SetPollState(key, 12); err != nil {
		t.Fatalf("SetPollState (overwrite): %v", err)
	}

	value, ok, err := s.GetPollState(key)
	if err != nil {
		t.Fatalf("GetPollState: %v", err)
	}
	if !ok || value != 12 {
		t.Fatalf("expected the overwritten value (12, true), got (%d, %v)", value, ok)
	}
}
