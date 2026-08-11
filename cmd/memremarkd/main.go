package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/haminh7036/memremark/internal/daemon"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/summarizer"
)

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

	ctx := context.Background()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	log.Println("memremarkd: started, polling every 3s")
	for range ticker.C {
		if err := d.PollOnce(ctx, time.Now()); err != nil {
			log.Printf("memremarkd: poll error: %v", err)
		}
	}
}
