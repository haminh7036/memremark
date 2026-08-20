package daemon

import (
	"context"
	"time"

	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

func (d *Daemon) recordObservation(obs observation.Observation, invoker summarizer.Invoker, now time.Time) error {
	wingID, err := d.Store.GetOrCreateWing(obs.WingPath)
	if err != nil {
		return err
	}
	// Prefer the real per-event timestamp from the transcript; fall back to the
	// poll time only when it's missing/unparseable (zero value). A zero
	// time.Time sorts before the epoch and would otherwise break VerbatimSince's
	// created_at > since filter, but discarding real timestamps unconditionally
	// would collapse chronology for any backlog processed after daemon downtime.
	createdAt := obs.Timestamp
	if createdAt.IsZero() {
		createdAt = now
	}
	if err := d.Store.InsertVerbatimDrawer(wingID, obs.SessionID, obs.ToolName, obs.Content, createdAt); err != nil {
		return err
	}
	d.sessionWing[obs.SessionID] = wingID
	d.sessionInvoker[obs.SessionID] = invoker
	d.Tracker.Touch(obs.SessionID, now)
	return nil
}

// maxSummarizeBatchBytes bounds how much verbatim content one Summarize call
// may cover.
//
// ponytail: incident 2026-08-18 -- a session whose summarization kept
// failing accumulated 1,315 verbatim rows (~3.5MB) with no cap, and
// summarizeSession passed the whole backlog as one exec.Command argv
// element. The real, tighter limit that bit is Linux's per-argument
// MAX_ARG_STRLEN (32 pages = 131,072 bytes), not the much larger total
// ARG_MAX (2,097,152 bytes) -- confirmed by direct repro (a 140,000-byte
// single argv already fails). ClaudeCodeInvoker now sends the prompt via
// stdin instead (no such limit), but AntigravityInvoker (agy -p) has no
// verified stdin support, so this cap stays well under 131,072 bytes to
// keep that path safe regardless of backlog size.
const maxSummarizeBatchBytes = 100_000

func (d *Daemon) summarizeSession(ctx context.Context, sessionID string, now time.Time) error {
	return d.summarizeSessionWithBatchSize(ctx, sessionID, now, maxSummarizeBatchBytes)
}

func (d *Daemon) summarizeSessionWithBatchSize(ctx context.Context, sessionID string, now time.Time, maxBatchBytes int) error {
	wingID, ok := d.sessionWing[sessionID]
	if !ok {
		return nil // never recorded an observation for this session; nothing to summarize
	}
	invoker := d.sessionInvoker[sessionID]

	since, hasPrev, err := d.Store.LastSummaryCoversTo(wingID, sessionID)
	if err != nil {
		return err
	}
	if !hasPrev {
		since = time.Unix(0, 0)
	}

	verbatim, err := d.Store.VerbatimSince(wingID, sessionID, since)
	if err != nil {
		return err
	}

	pruned := false
	for len(verbatim) > 0 {
		batch := takeBatch(verbatim, maxBatchBytes)
		verbatim = verbatim[len(batch):]

		var obs []observation.Observation
		for _, v := range batch {
			obs = append(obs, observation.Observation{ToolName: v.ToolName, Content: v.Content})
		}

		items, err := summarizer.Summarize(ctx, invoker, obs, d.TargetLanguage)
		if err != nil {
			return err
		}

		coversFrom := batch[0].CreatedAt
		coversTo := batch[len(batch)-1].CreatedAt
		for _, item := range items {
			if err := d.Store.InsertSummaryDrawer(wingID, sessionID, item.Hall, item.Content, coversFrom, coversTo, now); err != nil {
				return err
			}
		}

		// The batch is now fully distilled into summary drawers above -- the
		// raw rows have done their job and can go, so the DB doesn't grow
		// unbounded forever (see incident: 101MB DB, 89.9MB of it verbatim).
		ids := make([]int64, len(batch))
		for i, v := range batch {
			ids[i] = v.ID
		}
		if err := d.Store.DeleteDrawers(ids); err != nil {
			return err
		}
		pruned = true
	}
	if pruned {
		if err := d.Store.IncrementalVacuum(); err != nil {
			return err
		}
	}
	return nil
}

// takeBatch returns the longest prefix of verbatim whose combined Content
// length stays within maxBytes, always including at least the first row
// even if that single row alone exceeds the budget.
func takeBatch(verbatim []storage.Drawer, maxBytes int) []storage.Drawer {
	if len(verbatim) == 0 {
		return nil
	}
	total := len(verbatim[0].Content)
	end := 1
	for end < len(verbatim) {
		next := total + len(verbatim[end].Content)
		if next > maxBytes {
			break
		}
		total = next
		end++
	}
	return verbatim[:end]
}
