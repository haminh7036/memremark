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
// Previously capped at 100KB to stay under Linux's per-argument MAX_ARG_STRLEN
// (131,072 bytes) when the prompt was passed via `agy -p`.  Since agy now
// reads the prompt from stdin there is no OS-level size constraint.
//
// The effective limit is the LLM's context window.  agy uses Gemini with a
// ~1M-token context window.  1M tokens ≈ 3–4 MB of mixed code/text content
// (tool outputs tokenize at ~3–4 chars/token on average).  We cap at 3 MB
// to leave room for the system-prompt wrapper and the response, giving us
// ~30× larger batches than before and reducing API round-trips proportionally.
const maxSummarizeBatchBytes = 3_000_000

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

	var opts summarizer.InvokerOptions
	if d.Store != nil {
		if wing, err := d.Store.GetWingByID(wingID); err == nil && wing != nil {
			opts = summarizer.InvokerOptions{
				SessionID: storage.WingSummarySessionID(wing.Path),
				WorkDir:   wing.Path,
			}
		}
	}

	pruned := false
	for len(verbatim) > 0 {
		batch := takeBatch(verbatim, maxBatchBytes)
		verbatim = verbatim[len(batch):]

		var obs []observation.Observation
		for _, v := range batch {
			obs = append(obs, observation.Observation{ToolName: v.ToolName, Content: v.Content})
		}

		items, err := summarizer.SummarizeWithOptions(ctx, invoker, obs, d.TargetLanguage, opts)
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
