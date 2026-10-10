// Command agent is the entrypoint for the telemetry agent.
//
// It wires together configuration, structured logging, subsystem collectors,
// and the HTTP sender, then runs the orchestration loop until it receives an
// OS signal (SIGINT or SIGTERM) or an unrecoverable error.
//
// Shutdown sequence:
//  1. Signal handler cancels the root context.
//  2. The orchestrator's Run() loop detects ctx.Done() and returns.
//  3. main() calls sender.Close() to drain idle HTTP connections.
//  4. The process exits with code 0.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/telemetry-agent/internal/collector"
	"github.com/telemetry-agent/internal/config"
	"github.com/telemetry-agent/internal/transport"
)

// version is injected at build time via -ldflags="-X main.version=<semver>".
// It falls back to "dev" when the binary is built without the flag.
var version = "dev"

func main() {
	cfg := config.Load()

	// ── Structured logging ────────────────────────────────────────────────────
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.Info("telemetry agent starting",
		"version", version,
		"node_id", cfg.NodeID,
		"hub_url", cfg.HubURL,
		"interval", cfg.Interval,
		"log_level", cfg.LogLevel.String(),
	)

	// ── Root context with OS signal cancellation ──────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Transport ─────────────────────────────────────────────────────────────
	sender, err := transport.NewHTTPSender(transport.HTTPSenderConfig{
		HubURL:         cfg.HubURL,
		AuthToken:      cfg.AuthToken,
		NodeID:         cfg.NodeID,
		RequestTimeout: cfg.RequestTimeout,
		MaxRetries:     cfg.MaxRetries,
		RetryBaseDelay: cfg.RetryBaseDelay,
		RetryMaxDelay:  cfg.RetryMaxDelay,
		Logger:         logger,
	})
	if err != nil {
		logger.Error("failed to initialise HTTP sender", "error", err)
		os.Exit(1)
	}
	defer func() {
		if cerr := sender.Close(); cerr != nil {
			logger.Warn("sender close error", "error", cerr)
		}
	}()

	// ── Subsystem collectors ──────────────────────────────────────────────────
	orchestrator := collector.NewOrchestrator(
		cfg.Interval,
		cfg.NodeID,
		version,
		logger,
		sender.Send,
		collector.WithCollectors(
			collector.NewHostCollector(),
			collector.NewCPUCollector(0), // 0 → default 500 ms sample window
			collector.NewMemoryCollector(),
			collector.NewDiskCollector(),
			collector.NewNetworkCollector(),
			collector.NewProcessCollector(10),
		),
	)

	// ── Run until signal or error ─────────────────────────────────────────────
	orchestrator.Run(ctx)

	logger.Info("telemetry agent stopped gracefully")
}
