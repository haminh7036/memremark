package debounce

import (
	"sync"
	"time"
)

// Tracker decides when a session has gone idle long enough to trigger
// summarization, coalescing rapid bursts of activity into one trigger.
type Tracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	fired    map[string]bool
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{
		lastSeen: make(map[string]time.Time),
		fired:    make(map[string]bool),
	}
}

// Touch records activity for a session, resetting its idle clock so it
// can be reported due again after the next idle window elapses.
func (t *Tracker) Touch(sessionID string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastSeen[sessionID] = at
	t.fired[sessionID] = false
}

// Due returns the sessions that have been idle for at least idleWindow
// since their last Touch. It does not mark them as fired -- a session is
// only consumed once the caller has actually handled it successfully, so
// call Consume explicitly after that. This means a failed handling attempt
// leaves the session due again on the very next call, instead of being
// silently and permanently dropped.
func (t *Tracker) Due(now time.Time, idleWindow time.Duration) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var due []string
	for sessionID, last := range t.lastSeen {
		if t.fired[sessionID] {
			continue
		}
		if now.Sub(last) >= idleWindow {
			due = append(due, sessionID)
		}
	}
	return due
}

// Consume marks a session as fired so Due won't return it again until its
// next Touch. Callers must only call this after successfully handling the
// due session (e.g. summarization succeeded) -- see Daemon.PollOnce.
func (t *Tracker) Consume(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fired[sessionID] = true
}
