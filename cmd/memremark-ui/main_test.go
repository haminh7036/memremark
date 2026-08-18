package main

import (
	"reflect"
	"testing"

	"github.com/haminh7036/memremark/internal/config"
)

func TestResolveOptions_Defaults(t *testing.T) {
	cfg := config.DefaultConfig()
	defaultDB := "/tmp/memremark.db"

	opts, err := resolveOptions([]string{}, cfg, defaultDB)
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}

	if opts.Host != "127.0.0.1" {
		t.Errorf("expected default host 127.0.0.1, got %q", opts.Host)
	}
	if opts.Port != 8765 {
		t.Errorf("expected default port 8765, got %d", opts.Port)
	}
	if !opts.AutoOpen {
		t.Errorf("expected default auto_open true, got %v", opts.AutoOpen)
	}
	if opts.DBPath != defaultDB {
		t.Errorf("expected default DB %q, got %q", defaultDB, opts.DBPath)
	}
}

func TestResolveOptions_CLIFlagsOverrideConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UI.Host = "10.0.0.1"
	cfg.UI.Port = 9000
	cfg.UI.AutoOpen = true

	args := []string{"--host", "0.0.0.0", "-p", "3000", "--no-open", "--db", "/custom/path.db"}
	opts, err := resolveOptions(args, cfg, "/default/path.db")
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}

	if opts.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %q", opts.Host)
	}
	if opts.Port != 3000 {
		t.Errorf("expected port 3000, got %d", opts.Port)
	}
	if opts.AutoOpen != false {
		t.Errorf("expected auto_open false with --no-open, got %v", opts.AutoOpen)
	}
	if opts.DBPath != "/custom/path.db" {
		t.Errorf("expected DB /custom/path.db, got %q", opts.DBPath)
	}
}

func TestResolveOptions_LongPortFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	args := []string{"--port", "4000"}
	opts, err := resolveOptions(args, cfg, "/default/path.db")
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}

	if opts.Port != 4000 {
		t.Errorf("expected port 4000, got %d", opts.Port)
	}
}

func TestResolveOptions_ConfigAutoOpenFalse(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UI.AutoOpen = false

	opts, err := resolveOptions([]string{}, cfg, "/default/path.db")
	if err != nil {
		t.Fatalf("resolveOptions: %v", err)
	}

	if opts.AutoOpen != false {
		t.Errorf("expected auto_open false from config, got %v", opts.AutoOpen)
	}
}

func TestFormatDashboardURL(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 8765, "http://localhost:8765"},
		{"localhost", 8765, "http://localhost:8765"},
		{"0.0.0.0", 8765, "http://localhost:8765"},
		{"", 8765, "http://localhost:8765"},
		{"192.168.1.50", 8765, "http://192.168.1.50:8765"},
		{"memremark.local", 9090, "http://memremark.local:9090"},
	}

	for _, tt := range tests {
		got := formatDashboardURL(tt.host, tt.port)
		if got != tt.want {
			t.Errorf("formatDashboardURL(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}

func TestBrowserCommand(t *testing.T) {
	tests := []struct {
		goos string
		url  string
		want []string
	}{
		{"darwin", "http://localhost:8765", []string{"open", "http://localhost:8765"}},
		{"windows", "http://localhost:8765", []string{"cmd", "/c", "start", "http://localhost:8765"}},
		{"linux", "http://localhost:8765", []string{"xdg-open", "http://localhost:8765"}},
		{"freebsd", "http://localhost:8765", []string{"xdg-open", "http://localhost:8765"}},
	}

	for _, tt := range tests {
		cmd := browserCommand(tt.goos, tt.url)
		if !reflect.DeepEqual(cmd.Args, tt.want) {
			t.Errorf("browserCommand(%q, %q).Args = %v, want %v", tt.goos, tt.url, cmd.Args, tt.want)
		}
	}
}
