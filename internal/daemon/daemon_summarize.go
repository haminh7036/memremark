package daemon

import (
	"context"
	"time"

	"github.com/haminh7036/memremark/internal/observation"
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

func (d *Daemon) summarizeSession(ctx context.Context, sessionID string, now time.Time) error {
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
	if len(verbatim) == 0 {
		return nil
	}

	var obs []observation.Observation
	for _, v := range verbatim {
		obs = append(obs, observation.Observation{ToolName: v.ToolName, Content: v.Content})
	}

	items, err := summarizer.Summarize(ctx, invoker, obs)
	if err != nil {
		return err
	}

	coversFrom := verbatim[0].CreatedAt
	coversTo := verbatim[len(verbatim)-1].CreatedAt
	for _, item := range items {
		if err := d.Store.InsertSummaryDrawer(wingID, sessionID, item.Hall, item.Content, coversFrom, coversTo, now); err != nil {
			return err
		}
	}
	return nil
}
