package ui

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/haminh7036/memremark/internal/storage"
)

// Server handles UI HTTP routes and static assets.
type Server struct {
	store  *storage.Store
	assets fs.FS
	mux    *http.ServeMux
}

// NewServer creates a new Server instance with configured routes.
func NewServer(store *storage.Store, assets fs.FS) *Server {
	s := &Server{
		store:  store,
		assets: assets,
		mux:    http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/wings", s.handleWings)
	s.mux.HandleFunc("GET /api/timeline", s.handleTimeline)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)

	// Static asset server with SPA fallback
	fileServer := http.FileServer(http.FS(s.assets))
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(s.assets, path); err != nil {
			// Fallback to index.html for SPA routing
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Serve starts listening on addr until context cancellation.
func (s *Server) Serve(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler: s.mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
