package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/antigravity"
	"github.com/haminh7036/memremark/internal/adapter/claudecode"
	"github.com/haminh7036/memremark/internal/debounce"
	"github.com/haminh7036/memremark/internal/observation"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// idleWindow is how long a session must go quiet before it's summarized.
//
// ponytail: fixed and untuned -- see spec §10. Adjust based on real usage
// once this is running day to day.
const idleWindow = 5 * time.Second

// Daemon ties transcript reading, storage, debounce, and summarization
// together into one repeatable poll cycle.
type Daemon struct {
	Store   *storage.Store
	Tracker *debounce.Tracker

	claudeProjectsRoot string
	claudeTailer       *claudecode.Tailer
	claudeParsers      map[string]*claudecode.Parser

	antigravitySummariesDB string
	antigravityLastIdx     map[string]int64

	sessionWing    map[string]int64
	sessionInvoker map[string]summarizer.Invoker

	claudeInvoker      summarizer.Invoker
	antigravityInvoker summarizer.Invoker
}

// New builds a Daemon ready to poll. claudeProjectsRoot is typically
// $HOME/.claude/projects; antigravitySummariesDB is typically
// $HOME/.gemini/antigravity-cli/conversation_summaries.db.
func New(store *storage.Store, claudeProjectsRoot, antigravitySummariesDB string, claudeInvoker, antigravityInvoker summarizer.Invoker) *Daemon {
	return &Daemon{
		Store:                  store,
		Tracker:                debounce.NewTracker(),
		claudeProjectsRoot:     claudeProjectsRoot,
		claudeTailer:           claudecode.NewTailer(),
		claudeParsers:          make(map[string]*claudecode.Parser),
		antigravitySummariesDB: antigravitySummariesDB,
		antigravityLastIdx:     make(map[string]int64),
		sessionWing:            make(map[string]int64),
		sessionInvoker:         make(map[string]summarizer.Invoker),
		claudeInvoker:          claudeInvoker,
		antigravityInvoker:     antigravityInvoker,
	}
}

// PollOnce runs one capture pass over both CLIs' transcripts, then
// triggers summarization for any session that has gone idle.
func (d *Daemon) PollOnce(ctx context.Context, now time.Time) error {
	if err := d.pollClaudeCode(now); err != nil {
		log.Printf("daemon: claude code poll error: %v", err)
	}
	if err := d.pollAntigravity(now); err != nil {
		log.Printf("daemon: antigravity poll error: %v", err)
	}
	for _, sessionID := range d.Tracker.Due(now, idleWindow) {
		if err := d.summarizeSession(ctx, sessionID, now); err != nil {
			log.Printf("daemon: summarize session %s failed: %v", sessionID, err)
		}
	}
	return nil
}

func (d *Daemon) pollClaudeCode(now time.Time) error {
	files, err := claudecode.DiscoverTranscriptFiles(d.claudeProjectsRoot)
	if err != nil {
		return err
	}
	for _, file := range files {
		parser, ok := d.claudeParsers[file]
		if !ok {
			parser = claudecode.NewParser()
			d.claudeParsers[file] = parser
		}
		lines, err := d.claudeTailer.ReadNewLines(file)
		if err != nil {
			log.Printf("daemon: read %s: %v", file, err)
			continue
		}
		for _, line := range lines {
			obs, ok, err := parser.Feed(line)
			if err != nil {
				log.Printf("daemon: parse %s: %v", file, err)
				continue
			}
			if !ok {
				continue
			}
			if err := d.recordObservation(obs, d.claudeInvoker, now); err != nil {
				log.Printf("daemon: record observation: %v", err)
			}
		}
	}
	return nil
}

func (d *Daemon) pollAntigravity(now time.Time) error {
	convs, err := antigravity.ListConversations(d.antigravitySummariesDB)
	if err != nil {
		return err
	}
	for _, conv := range convs {
		if conv.WorkspaceURIs == "" {
			continue
		}
		dbPath := filepath.Join(filepath.Dir(d.antigravitySummariesDB), "conversations", conv.ID+".db")
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}
		sinceIdx, ok := d.antigravityLastIdx[conv.ID]
		if !ok {
			sinceIdx = -1
		}
		obs, maxIdx, err := antigravity.ReadObservations(dbPath, conv.WorkspaceURIs, conv.ID, conv.LastModified, sinceIdx)
		if err != nil {
			// Task 8's code review flagged this: on a mid-scan error, maxIdx may
			// already be advanced past rows that weren't returned in obs. Do NOT
			// persist it here -- keep sinceIdx as-is so the next poll retries from
			// the last known-good position instead of silently skipping rows.
			log.Printf("daemon: read antigravity conversation %s: %v", conv.ID, err)
			continue
		}
		d.antigravityLastIdx[conv.ID] = maxIdx
		for _, o := range obs {
			if err := d.recordObservation(o, d.antigravityInvoker, now); err != nil {
				log.Printf("daemon: record observation: %v", err)
			}
		}
	}
	return nil
}

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

	since, hasPrev, err := d.Store.LastSummaryTime(wingID, sessionID)
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
