package daemon

import (
	"context"
	"strconv"
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
	if invoker != nil {
		d.wingInvoker[wingID] = invoker
	}
	d.sessionWing[obs.SessionID] = wingID
	d.sessionInvoker[obs.SessionID] = invoker
	d.Tracker.Touch(strconv.FormatInt(wingID, 10), now)
	return nil
}

func (d *Daemon) summarizeWing(ctx context.Context, wingID int64, now time.Time) error {
	d.cliMutex.Lock()
	defer d.cliMutex.Unlock()

	if d.IsPaused() {
		return nil
	}

	wing, err := d.Store.GetWingByID(wingID)
	if err != nil || wing == nil {
		return err
	}

	verbatim, err := d.Store.UnsummarizedVerbatimByWing(wingID, 500)
	if err != nil || len(verbatim) == 0 {
		return err
	}

	batch, obs := summarizer.CompactAndBudget(verbatim, summarizer.MaxWorkspacePromptBytes)
	if len(batch) == 0 {
		return nil
	}

	invoker := d.wingInvoker[wingID]
	if invoker == nil {
		invoker = d.claudeInvoker
	}
	if invoker == nil {
		invoker = d.antigravityInvoker
	}

	opts := summarizer.InvokerOptions{
		SessionID: storage.WingSummarySessionID(wing.Path),
		WorkDir:   wing.Path,
	}

	callCtx, cancel := context.WithCancel(ctx)
	d.RegisterActiveCancel(cancel)
	items, err := summarizer.SummarizeWithOptions(callCtx, invoker, obs, d.TargetLanguage, opts)
	d.ClearActiveCancel()
	cancel()
	if err != nil {
		return err
	}

	coversFrom := batch[0].CreatedAt
	coversTo := batch[len(batch)-1].CreatedAt
	summarySessionID := storage.WingSummarySessionID(wing.Path)

	for _, item := range items {
		if err := d.Store.InsertSummaryDrawer(wingID, summarySessionID, item.Hall, item.Content, item.Narrative, coversFrom, coversTo, now); err != nil {
			return err
		}
	}

	ids := make([]int64, len(batch))
	for i, v := range batch {
		ids[i] = v.ID
	}
	if err := d.Store.DeleteDrawers(ids); err != nil {
		return err
	}

	return d.Store.IncrementalVacuum()
}

func (d *Daemon) synthesizeSessionDigest(ctx context.Context, sessionID string, wingID int64, invoker summarizer.Invoker, now time.Time) error {
	if d.Store == nil || invoker == nil {
		return nil
	}

	summaries, err := d.Store.GetDrawersBySession(wingID, sessionID, "summary")
	if err != nil {
		return err
	}

	var fallbackVerbatim []storage.Drawer
	if len(summaries) == 0 {
		fallbackVerbatim, err = d.Store.GetDrawersBySession(wingID, sessionID, "verbatim")
		if err != nil {
			return err
		}
	}

	if len(summaries) == 0 && len(fallbackVerbatim) == 0 {
		return nil
	}

	var opts summarizer.InvokerOptions
	if wing, err := d.Store.GetWingByID(wingID); err == nil && wing != nil {
		opts = summarizer.InvokerOptions{
			SessionID: storage.WingSummarySessionID(wing.Path),
			WorkDir:   wing.Path,
		}
	}

	callCtx, cancel := context.WithCancel(ctx)
	d.RegisterActiveCancel(cancel)
	res, err := summarizer.SynthesizeSessionDigest(callCtx, invoker, summaries, fallbackVerbatim, d.TargetLanguage, opts)
	d.ClearActiveCancel()
	cancel()
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}

	digest := storage.SessionDigest{
		WingID:       wingID,
		SessionID:    sessionID,
		Request:      res.Request,
		Investigated: res.Investigated,
		Learned:      res.Learned,
		Completed:    res.Completed,
		NextSteps:    res.NextSteps,
		Notes:        res.Notes,
		CreatedAt:    now,
	}
	return d.Store.UpsertSessionDigest(digest)
}

