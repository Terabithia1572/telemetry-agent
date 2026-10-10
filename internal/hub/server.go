// Package hub – server.go wires together the HTTP mux, embedded static assets,
// structured logging middleware, CORS headers, and graceful shutdown.
package hub

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

// ServerConfig groups all parameters needed to construct and run the Hub HTTP
// server.
type ServerConfig struct {
	// ListenAddr is the TCP address to bind, e.g. ":8080" or "0.0.0.0:9000".
	ListenAddr string

	// DBPath is the filesystem path for the SQLite database file.
	// Use ":memory:" for ephemeral operation (testing / CI).
	DBPath string

	// AuthToken is the bearer token agents must supply.  Empty disables auth.
	AuthToken string

	// OfflineAfter is the duration without a heartbeat before a node is
	// considered offline.
	OfflineAfter time.Duration

	// StaticFS is the embedded filesystem rooted at the "web" directory
	// produced by go:embed in the hub entrypoint. When nil, the static file
	// server is disabled (useful in tests).
	StaticFS fs.FS

	// Logger is the structured logger; when nil slog.Default() is used.
	Logger *slog.Logger
}

// Server is the running HTTP server instance returned by [NewServer].
// Call [Server.Run] to start listening and [Server.Shutdown] to stop.
type Server struct {
	httpSrv *http.Server
	store   *Store
	log     *slog.Logger
}

// NewServer creates a [Server] from cfg but does not start listening.
// It opens the database and applies schema migrations.
func NewServer(cfg ServerConfig) (*Server, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	if cfg.OfflineAfter <= 0 {
		cfg.OfflineAfter = 90 * time.Second
	}

	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("hub: open store: %w", err)
	}

	api := NewAPI(store, cfg.AuthToken, cfg.OfflineAfter, log)

	mux := http.NewServeMux()

	// API routes.
	api.RegisterRoutes(mux)

	// Static web dashboard served from the embedded FS.
	if cfg.StaticFS != nil {
		mux.Handle("/", http.FileServer(http.FS(cfg.StaticFS)))
	}

	// Wrap mux with logging + CORS middleware.
	handler := corsMiddleware(loggingMiddleware(mux, log))

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second, // generous for SSE long-poll
		IdleTimeout:       120 * time.Second,
	}

	return &Server{
		httpSrv: httpSrv,
		store:   store,
		log:     log,
	}, nil
}

// Run starts the HTTP server and blocks until it returns (either from a call
// to [Shutdown] or a listener error).
func (s *Server) Run() error {
	s.log.Info("hub listening", "addr", s.httpSrv.Addr)
	if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("hub: listen: %w", err)
	}
	return nil
}

// Shutdown gracefully drains active connections within timeout and closes the
// database.
func (s *Server) Shutdown(ctx context.Context) {
	if err := s.httpSrv.Shutdown(ctx); err != nil {
		s.log.Warn("hub: shutdown error", "error", err)
	}
	if err := s.store.Close(); err != nil {
		s.log.Warn("hub: store close error", "error", err)
	}
	s.log.Info("hub stopped")
}

// ── Middleware ────────────────────────────────────────────────────────────────

// loggingMiddleware logs method, path, status, and latency for every request.
func loggingMiddleware(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Debug("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"latency", time.Since(start).String(),
			"remote", r.RemoteAddr,
		)
	})
}

// corsMiddleware adds permissive CORS headers so the dashboard can be served
// from a different origin during local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Node-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// responseRecorder wraps http.ResponseWriter to capture the status code.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
