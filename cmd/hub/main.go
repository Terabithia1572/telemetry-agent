// Command hub is the entrypoint for the Telemetry Hub server.
//
// It reads configuration from flags / environment variables, opens the SQLite
// database, starts the HTTP server with the embedded web dashboard, and waits
// for SIGINT / SIGTERM to shut down cleanly.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/telemetry-agent/internal/hub"
	"github.com/telemetry-agent/webui"
)

// version is injected at build time via -ldflags "-X main.version=<semver>".
var version = "dev"

func main() {
	var (
		listenAddr   = flag.String("listen", envOr("HUB_LISTEN", ":8080"), "TCP address to listen on")
		dbPath       = flag.String("db", envOr("HUB_DB", "telemetry.db"), "SQLite database file path (use :memory: for ephemeral)")
		authToken    = flag.String("auth-token", envOr("AUTH_TOKEN", ""), "Bearer token agents must supply (empty = no auth)")
		offlineAfter = flag.Duration("offline-after", 90*time.Second, "Duration without heartbeat before a node is marked offline")
		logLevelStr  = flag.String("log-level", envOr("LOG_LEVEL", "info"), "Log level: debug|info|warn|error")
	)
	flag.Parse()

	// ── Logging ───────────────────────────────────────────────────────────────
	logLevel := parseLevel(*logLevelStr)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	logger.Info("telemetry hub starting", "version", version, "listen", *listenAddr, "db", *dbPath)

	// ── Strip the "web" prefix from the embedded FS so "/" serves index.html.
	// webui.FS is rooted at the module root and contains the "web/" subdirectory.
	stripped, err := fs.Sub(webui.FS, "web")
	if err != nil {
		logger.Error("failed to sub embedded FS", "error", err)
		os.Exit(1)
	}

	// ── Build server ──────────────────────────────────────────────────────────
	srv, err := hub.NewServer(hub.ServerConfig{
		ListenAddr:   *listenAddr,
		DBPath:       *dbPath,
		AuthToken:    *authToken,
		OfflineAfter: *offlineAfter,
		StaticFS:     stripped,
		Logger:       logger,
	})
	if err != nil {
		logger.Error("failed to create hub server", "error", err)
		os.Exit(1)
	}

	// ── Signal handling ───────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// init sets a helpful flag.Usage message.
func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "telemetry-hub %s\n\nUsage:\n", version)
		flag.PrintDefaults()
	}
}
