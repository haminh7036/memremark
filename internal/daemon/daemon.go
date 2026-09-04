package daemon

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/haminh7036/memremark/internal/adapter/antigravity"
	"github.com/haminh7036/memremark/internal/adapter/claudecode"
	"github.com/haminh7036/memremark/internal/debounce"
	"github.com/haminh7036/memremark/internal/locale"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// idleWindow is how long a session (or workspace) must go quiet before it's summarized.
// Increased to 45s to avoid aggressive continuous CLI spawning on short pauses.
const idleWindow = 45 * time.Second

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

	pauseMu        sync.Mutex
	pauseFilePath  string
	activeCancelMu sync.Mutex
	activeCancel   func()
	cliMutex       sync.Mutex

	claudeProjectsRoot string
	claudeTailer       *claudecode.Tailer
	claudeParsers      map[string]*claudecode.Parser

	antigravitySummariesDB      string
	antigravitySummariesModTime time.Time
	antigravitySummariesSize    int64
	antigravityConvs            []antigravity.ConversationInfo
	antigravityConvWing         map[string]string
	antigravityDBMeta           map[string]dbMeta
	antigravityLastIdx          map[string]int64

	sessionWing    map[string]int64
	sessionInvoker map[string]summarizer.Invoker
	wingInvoker    map[int64]summarizer.Invoker

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
		antigravityConvWing:    make(map[string]string),
		antigravityDBMeta:      make(map[string]dbMeta),
		antigravityLastIdx:     make(map[string]int64),
		sessionWing:            make(map[string]int64),
		sessionInvoker:         make(map[string]summarizer.Invoker),
		wingInvoker:            make(map[int64]summarizer.Invoker),
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
	wingIDs, err := d.Store.OrphanedVerbatimWings()
	if err != nil {
		return err
	}
	now := time.Now()
	for i, wingID := range wingIDs {
		if _, tracked := d.wingInvoker[wingID]; tracked {
			continue
		}
		tracked := false
		for _, wID := range d.sessionWing {
			if wID == wingID {
				tracked = true
				break
			}
		}
		if tracked {
			continue
		}

		wingKey := strconv.FormatInt(wingID, 10)
		d.wingInvoker[wingID] = d.claudeInvoker
		touchTime := now.Add(-idleWindow - time.Duration(i+1)*time.Second)
		d.Tracker.Touch(wingKey, touchTime)
	}
	return nil
}

// maxSessionsPerTick caps how many sessions PollOnce summarizes in a single
// poll cycle.  Each agy invocation spawns a full MCP stack (~10 Node.js
// children, ~200–400 MB each), so serializing them and capping at a small
// number keeps RAM in check.  Remaining sessions stay Due and are processed
// on subsequent ticks.
const maxSessionsPerTick = 2

// PollOnce runs one capture pass over both CLIs' transcripts, then
// triggers summarization for any session that has gone idle.
func (d *Daemon) PollOnce(ctx context.Context, now time.Time) error {
	if d.IsPaused() {
		return nil
	}

	if err := d.pollClaudeCode(now); err != nil {
		log.Printf("daemon: claude code poll error: %v", err)
	}
	if err := d.pollAntigravity(now); err != nil {
		log.Printf("daemon: antigravity poll error: %v", err)
	}

	processed := 0
	for _, dueKey := range d.Tracker.Due(now, idleWindow) {
		if ctx.Err() != nil {
			break
		}
		if processed >= maxSessionsPerTick {
			break
		}

		if wingID, err := strconv.ParseInt(dueKey, 10, 64); err == nil {
			sessions := make(map[string]bool)
			for sID, wID := range d.sessionWing {
				if wID == wingID {
					sessions[sID] = true
				}
			}
			if refs, err := d.Store.OrphanedVerbatimSessions(); err == nil {
				for _, ref := range refs {
					if ref.WingID == wingID {
						d.sessionWing[ref.SessionID] = wingID
						d.sessionInvoker[ref.SessionID] = d.claudeInvoker
						sessions[ref.SessionID] = true
					}
				}
			}
			failed := false
			for sID := range sessions {
				if err := d.summarizeSession(ctx, sID, now); err != nil {
					if ctx.Err() != nil {
						failed = true
						break
					}
					log.Printf("daemon: summarize session %s failed: %v", sID, err)
					failed = true
					break
				}
			}
			if failed {
				continue
			}
			d.Tracker.Consume(dueKey)
			processed++
			continue
		}

		if err := d.summarizeSession(ctx, dueKey, now); err != nil {
			if ctx.Err() != nil {
				break
			}
			// Do NOT consume on failure -- retry next tick.
			log.Printf("daemon: summarize session %s failed: %v", dueKey, err)
			continue
		}
		d.Tracker.Consume(dueKey)
		processed++
	}
	return nil
}

// isSummarySession checks whether sessionID matches the deterministic
// summary session UUID for any known wing. Dedicated summarizer sessions
// are excluded from observation polling to prevent feedback loops.
func (d *Daemon) isSummarySession(sessionID string) bool {
	if d.Store == nil || sessionID == "" {
		return false
	}
	wings, err := d.Store.ListWings()
	if err != nil {
		log.Printf("daemon: isSummarySession: list wings: %v", err)
		return false
	}
	for _, w := range wings {
		if storage.WingSummarySessionID(w.Path) == sessionID {
			return true
		}
	}
	return false
}

