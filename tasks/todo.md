Goal: fix orphaned-verbatim bug — sessions that finished before a daemon
restart never get summarized/pruned because session tracking (Tracker
lastSeen/fired, sessionWing, sessionInvoker) is pure in-memory state with
no startup backlog sweep, while transcript byte-offsets ARE persisted
(poll_state table) so those sessions never produce a new observation to
re-Touch them. Root cause confirmed empirically: DB still has 89.6MB/21,828
verbatim rows across 220 sessions after v0.1.4 (prune feature) deployed;
many sessions show MIN(created_at) == MAX(created_at) (single burst, then
daemon restarted, never touched again).

Acceptance:
- On daemon startup, any session with existing verbatim rows but no live
  in-memory tracking gets seeded so the next PollOnce tick summarizes and
  prunes it (no data loss, no duplicate processing of live sessions).
- Invoker choice for recovered sessions defaults to d.claudeInvoker --
  verified safe because resolveInvokers (cmd/memremarkd/main.go) always
  makes ClaudeInvoker and AntigravityInvoker either equal (single-CLI case)
  or each other's Fallback (dual-CLI case), so no schema/migration needed
  to record which adapter originally wrote a session.
- go build ./... && go test ./... pass.
- Regression test proves: verbatim inserted with no recordObservation call
  in the current process (simulating pre-restart backlog) still gets
  summarized + deleted after Warmup() + one PollOnce tick past idleWindow.

[ ] Storage: add `Store.OrphanedVerbatimSessions() ([]SessionRef, error)`
    -- SELECT DISTINCT wing_id, session_id FROM drawers WHERE type='verbatim'
    -- SessionRef{WingID int64, SessionID string}
[ ] Storage test: seed verbatim rows across 2 sessions (+1 summary-only
    session that must NOT appear), assert exact distinct set returned
[ ] Daemon: add `Daemon.Warmup() error`
    -- call Store.OrphanedVerbatimSessions()
    -- for each ref not already in d.sessionWing: set sessionWing,
       sessionInvoker = d.claudeInvoker, Tracker.Touch(sessionID, time.Unix(0,0))
       (epoch timestamp guarantees Due() fires on the very next check
       regardless of idleWindow)
    -- skip refs already in sessionWing (don't clobber a live session's
       debounce clock)
[ ] Daemon test: insert verbatim directly via Store (no recordObservation),
    construct fresh Daemon, call Warmup(), then PollOnce(now) once ->
    assert summary drawer created + verbatim rows for that session gone
[ ] Daemon test: a session already Touch'd this process (live/active) is
    left untouched by Warmup (its lastSeen isn't reset to epoch)
[ ] Wire up: cmd/memremarkd/main.go calls d.Warmup() once right after
    daemon.New(...), before the ticker loop; log recovered session count
[ ] go build ./... && go test ./... green
[ ] Manual verify: run against real ~/.memremark/memremark.db copy (or
    the real one, since deletes only fire after successful summarize) and
    confirm verbatim MB drops on next daemon start+poll
[ ] Summary: what changed + how verified
