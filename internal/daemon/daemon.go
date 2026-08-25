package daemon

import (
	"context"
	"log"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/antigravity"
	"github.com/haminh7036/memremark/internal/adapter/claudecode"
	"github.com/haminh7036/memremark/internal/debounce"
	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// idleWindow is how long a session must go quiet before it's summarized.
//
// ponytail: fixed and untuned -- see spec §10. Adjust based on real usage
// once this is running day to day.
const idleWindow = 5 * time.Second

type dbMeta struct {
	modTime time.Time
	size    int64
}

// Daemon ties transcript reading, storage, debounce, and summarization
// together into one repeatable poll cycle.
type Daemon struct {
	Store          *storage.Store
	Tracker        *debounce.Tracker
	TargetLanguage locale.TargetLanguage

	claudeProjectsRoot string
	claudeTailer       *claudecode.Tailer
	claudeParsers      map[string]*claudecode.Parser

	antigravitySummariesDB      string
	antigravitySummariesModTime time.Time
	antigravitySummariesSize    int64
	antigravityConvs            []antigravity.ConversationInfo
	antigravityDBMeta           map[string]dbMeta
	antigravityLastIdx          map[string]int64

	sessionWing    map[string]int64
	sessionInvoker map[string]summarizer.Invoker

	claudeInvoker      summarizer.Invoker
	antigravityInvoker summarizer.Invoker
}

// New builds a Daemon ready to poll. claudeProjectsRoot is typically
// $HOME/.claude/projects; antigravitySummariesDB is typically
// $HOME/.gemini/antigravity-cli/conversation_summaries.db.
func New(store *storage.Store, claudeProjectsRoot, antigravitySummariesDB string, claudeInvoker, antigravityInvoker summarizer.Invoker, targetLang ...locale.TargetLanguage) *Daemon {
	var lang locale.TargetLanguage
	if len(targetLang) > 0 {
		lang = targetLang[0]
	}
	return &Daemon{
		Store:                  store,
		Tracker:                debounce.NewTracker(),
		TargetLanguage:         lang,
		claudeProjectsRoot:     claudeProjectsRoot,
		claudeTailer:           claudecode.NewTailer(),
		claudeParsers:          make(map[string]*claudecode.Parser),
		antigravitySummariesDB: antigravitySummariesDB,
		antigravityDBMeta:      make(map[string]dbMeta),
		antigravityLastIdx:     make(map[string]int64),
		sessionWing:            make(map[string]int64),
		sessionInvoker:         make(map[string]summarizer.Invoker),
		claudeInvoker:          claudeInvoker,
		antigravityInvoker:     antigravityInvoker,
	}
}

// Warmup seeds in-memory session tracking from any verbatim backlog left
// over from a previous daemon process -- e.g. a session that went idle and
// finished before the daemon restarted. Without this, such a session would
// never produce a new observation to re-arm its debounce clock (transcript
// byte-offsets ARE persisted across restarts in poll_state, so no new
// lines means no new Touch), leaving its verbatim rows orphaned forever
// even though summarizeSessionWithBatchSize's prune logic is correct.
//
// The recovered invoker always defaults to d.claudeInvoker: resolveInvokers
// (cmd/memremarkd/main.go) constructs ClaudeInvoker and AntigravityInvoker
// as either the same value (only one CLI installed) or each other's
// FallbackInvoker.Fallback (both installed), so there is no configuration
// where this default produces a session that fails to summarize.
//
// Call this once, right after New, before the poll loop starts. It is
// idempotent and safe to call again later: sessions already known in
// sessionWing are skipped so a live session's debounce clock is never
// clobbered back to the epoch.
func (d *Daemon) Warmup() error {
	refs, err := d.Store.OrphanedVerbatimSessions()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, tracked := d.sessionWing[ref.SessionID]; tracked {
			continue
		}
		d.sessionWing[ref.SessionID] = ref.WingID
		d.sessionInvoker[ref.SessionID] = d.claudeInvoker
		// Epoch guarantees Due() fires on the very first check, regardless
		// of idleWindow, since now.Sub(epoch) is always far past it.
		d.Tracker.Touch(ref.SessionID, time.Unix(0, 0))
	}
	return nil
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
