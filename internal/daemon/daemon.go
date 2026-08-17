package daemon

import (
	"context"
	"log"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/claudecode"
	"github.com/haminh7036/memremark/internal/debounce"
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
			// Do NOT consume the session on failure -- leave it due so the
			// very next poll tick retries it (a few seconds later, not a
			// whole new idle window). Matches the watermark-on-error
			// tolerance already used in pollAntigravity: just retry next
			// tick, no backoff, no dead-lettering.
			log.Printf("daemon: summarize session %s failed: %v", sessionID, err)
			continue
		}
		d.Tracker.Consume(sessionID)
	}
	return nil
}
