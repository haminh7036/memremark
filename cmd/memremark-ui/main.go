package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/haminh7036/memremark/internal/config"
	"github.com/haminh7036/memremark/internal/storage"
	"github.com/haminh7036/memremark/internal/ui"
)

// Options holds the resolved runtime settings for the web dashboard.
type Options struct {
	Host     string
	Port     int
	NoOpen   bool
	DBPath   string
	AutoOpen bool
}

func browserCommand(goos, url string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("cmd", "/c", "start", url)
	default:
		return exec.Command("xdg-open", url)
	}
}

func openBrowser(url string) {
	cmd := browserCommand(runtime.GOOS, url)
	if cmd != nil {
		_ = cmd.Start()
	}
}

func formatDashboardURL(host string, port int) string {
	if host == "" || host == "127.0.0.1" || host == "localhost" || host == "0.0.0.0" {
		return fmt.Sprintf("http://localhost:%d", port)
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func resolveOptions(args []string, cfg config.Config, defaultDB string) (Options, error) {
	fs := flag.NewFlagSet("memremark-ui", flag.ContinueOnError)
	hostFlag := fs.String("host", "", "Host address to listen on (default: 127.0.0.1)")
	portFlag := fs.Int("port", 0, "Port to listen on (default: 8765)")
	fs.IntVar(portFlag, "p", 0, "Port to listen on (shorthand)")
	noOpenFlag := fs.Bool("no-open", false, "Do not open browser automatically")
	dbFlag := fs.String("db", "", "SQLite database path")

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}

	opts := Options{
		Host:     cfg.UI.Host,
		Port:     cfg.UI.Port,
		AutoOpen: cfg.UI.AutoOpen,
		DBPath:   defaultDB,
	}

	if opts.Host == "" {
		opts.Host = config.DefaultUIHost
	}
	if opts.Port <= 0 {
		opts.Port = config.DefaultUIPort
	}

	if *hostFlag != "" {
		opts.Host = *hostFlag
	}
	if *portFlag > 0 {
		opts.Port = *portFlag
	}
	if *dbFlag != "" {
		opts.DBPath = *dbFlag
	}
	if *noOpenFlag {
		opts.AutoOpen = false
	}
	opts.NoOpen = *noOpenFlag

	return opts, nil
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("memremark-ui: resolve home dir: %v", err)
	}

	cfg, err := config.Load(home)
	if err != nil {
		log.Printf("memremark-ui: warning: failed to load config (%v), using defaults", err)
		cfg = config.DefaultConfig()
	}

	defaultDB := filepath.Join(home, ".memremark", "memremark.db")
	opts, err := resolveOptions(os.Args[1:], cfg, defaultDB)
	if err != nil {
		os.Exit(2)
	}

	store, err := storage.Open(opts.DBPath)
	if err != nil {
		log.Fatalf("memremark-ui: open database %s: %v", opts.DBPath, err)
	}
	defer store.Close()

	assets, err := ui.Assets()
	if err != nil {
		log.Fatalf("memremark-ui: load static assets: %v", err)
	}

	server := ui.NewServer(store, assets)
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("memremark-ui: failed to listen on %s: %v", addr, err)
	}

	httpServer := &http.Server{
		Handler: server.Handler(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	dashboardURL := formatDashboardURL(opts.Host, opts.Port)
	log.Printf("MemRemark Dashboard running at %s (listening on %s)", dashboardURL, addr)

	if opts.AutoOpen {
		go func() {
			time.Sleep(100 * time.Millisecond)
			openBrowser(dashboardURL)
		}()
	}

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("memremark-ui: server error: %v", err)
	}
	log.Println("MemRemark Dashboard stopped.")
}
