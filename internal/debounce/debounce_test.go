package debounce

import (
	"testing"
	"time"
)

func TestDueFiresOnceAfterIdleWindow(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("session-1", base)

	if due := tr.Due(base.Add(1*time.Second), 5*time.Second); len(due) != 0 {
		t.Fatalf("expected no due sessions before idle window elapses, got %v", due)
	}

	due := tr.Due(base.Add(6*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "session-1" {
		t.Fatalf("expected [session-1] due, got %v", due)
	}

	due2 := tr.Due(base.Add(20*time.Second), 5*time.Second)
	if len(due2) != 0 {
		t.Fatalf("expected no repeat firing without a new Touch, got %v", due2)
	}
}

func TestTouchResetsFiredState(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("session-1", base)
	tr.Due(base.Add(6*time.Second), 5*time.Second)

	tr.Touch("session-1", base.Add(10*time.Second))
	due := tr.Due(base.Add(16*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "session-1" {
		t.Fatalf("expected session-1 due again after new activity, got %v", due)
	}
}

func TestDueTracksMultipleSessionsIndependently(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("a", base)
	tr.Touch("b", base.Add(3*time.Second))

	due := tr.Due(base.Add(6*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "a" {
		t.Fatalf("expected only session a due, got %v", due)
	}
}
