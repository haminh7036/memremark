package debounce

import (
	"testing"
	"time"
)

func TestDueFiresOnceAfterIdleWindowThenConsumed(t *testing.T) {
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
	tr.Consume("session-1")

	due2 := tr.Due(base.Add(20*time.Second), 5*time.Second)
	if len(due2) != 0 {
		t.Fatalf("expected no repeat firing without a new Touch, got %v", due2)
	}
}

// TestDueWithoutConsumeStaysDueOnNextCall proves the retry contract Critical
// 2 depends on: a caller that fails to handle a due session (and so never
// calls Consume) must see it reported due again on the very next Due call,
// not lose it until a whole new idle window elapses.
func TestDueWithoutConsumeStaysDueOnNextCall(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("session-1", base)
	due := tr.Due(base.Add(6*time.Second), 5*time.Second)
	if len(due) != 1 || due[0] != "session-1" {
		t.Fatalf("expected [session-1] due, got %v", due)
	}
	// No Consume call here -- simulates a failed handling attempt.

	due2 := tr.Due(base.Add(7*time.Second), 5*time.Second)
	if len(due2) != 1 || due2[0] != "session-1" {
		t.Fatalf("expected session-1 still due without Consume, got %v", due2)
	}
}

func TestTouchResetsFiredState(t *testing.T) {
	tr := NewTracker()
	base := time.Now()

	tr.Touch("session-1", base)
	tr.Due(base.Add(6*time.Second), 5*time.Second)
	tr.Consume("session-1")

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
