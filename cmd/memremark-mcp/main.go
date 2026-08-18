package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/haminh7036/memremark/internal/mcp"
	"github.com/haminh7036/memremark/internal/storage"
)

func main() {
	// Direct all log output to stderr so stdout remains pure JSON-RPC
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memremark-mcp: resolve home dir: %v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(home, ".memremark", "memremark.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memremark-mcp: open storage: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	server := mcp.NewServer(store, os.Stdin, os.Stdout)
	if err := server.Serve(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "memremark-mcp: serve error: %v\n", err)
		os.Exit(1)
	}
}
