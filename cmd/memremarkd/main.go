package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

// pollTimeout bounds one poll cycle so a hung headless CLI invocation
// (`claude -p` / `agy -p` stuck on an auth prompt, a network hang, a stuck
// subprocess) can never wedge the daemon forever: exec.CommandContext kills
// the subprocess once this context's deadline fires. A poll cycle may
// include several real CLI invocations if multiple sessions go idle in the
// same tick, so this is generous, not tight.
const pollTimeout = 2 * time.Minute

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("memremarkd: resolve home dir: %v", err)
	}

	store, err := storage.Open(filepath.Join(home, ".memremark", "memremark.db"))
	if err != nil {
		log.Fatalf("memremarkd: open storage: %v", err)
	}
	defer store.Close()

	claudeProjectsRoot := filepath.Join(home, ".claude", "projects")
	antigravitySummariesDB := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")

	d := daemon.New(store, claudeProjectsRoot, antigravitySummariesDB,
		summarizer.ClaudeCodeInvoker{}, summarizer.AntigravityInvoker{})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Println("memremarkd: started, polling every 3s")
	for {
		select {
		case <-ctx.Done():
			log.Println("memremarkd: shutting down")
			return
		case now := <-ticker.C:
			if err := poll(ctx, d, now, pollTimeout); err != nil {
				log.Printf("memremarkd: poll error: %v", err)
			}
		}
	}
}

// poll runs one bounded poll cycle: a fresh timeout context derived per
// tick, instead of one context.Background() reused for the process's whole
// lifetime, so a single hung subprocess call inside PollOnce can't wedge
// every future tick too.
func poll(ctx context.Context, d *daemon.Daemon, now time.Time, timeout time.Duration) error {
	tickCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.PollOnce(tickCtx, now)
}
