package storage

import (
	"regexp"
	"testing"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestWingSummarySessionID_Determinism(t *testing.T) {
	path1 := "/home/user/project"
	path2 := "/home/user/project/" // trailing slash should normalize to same ID
	path3 := "/home/user/other-project"

	id1 := WingSummarySessionID(path1)
	id2 := WingSummarySessionID(path2)
	id3 := WingSummarySessionID(path3)

	if !uuidRegex.MatchString(id1) {
		t.Fatalf("id1 %q is not a valid UUIDv5", id1)
	}
	if id1 != id2 {
		t.Fatalf("expected normalized paths to yield identical ID: id1=%q, id2=%q", id1, id2)
	}
	if id1 == id3 {
		t.Fatalf("expected different paths to yield different IDs: id1=%q, id3=%q", id1, id3)
	}
}
