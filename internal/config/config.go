// Package config handles all agent configuration sourced from command-line
// flags and environment variables. Environment variables take lower priority
// than flags so that container deployments (which inject env vars) can still
// be overridden at the CLI for local debugging.
//
// Configuration is parsed exactly once at startup; the resulting [Config]
// struct is read-only during the lifetime of the agent.
package config

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds the complete, validated runtime configuration for the agent.
// All fields are exported so callers can pattern-match on them without
// importing unexported symbols.
type Config struct {
	// HubURL is the base URL of the telemetry hub, e.g.
	// "https://hub.example.com". The agent will POST to
	// <HubURL>/api/v1/metrics.
	HubURL string

	// AuthToken is the bearer token used to authenticate with the hub.
	// It is transmitted in the "Authorization: Bearer <token>" header.
	// An empty token disables authentication (useful for local testing).
	AuthToken string

	// NodeID is the stable identifier for this host sent in every payload.
	// Defaults to the OS hostname when unset.
	NodeID string

	// Interval is the delay between successive collection ticks.
	// Must be at least 5 s to avoid overwhelming the hub or the OS
	// metrics subsystem.
	Interval time.Duration

	// LogLevel controls the minimum severity level written to stdout.
	// Accepted values (case-insensitive): "debug", "info", "warn", "error".
	LogLevel slog.Level

	// RequestTimeout is the maximum time the HTTP transport will wait for
	// a single POST to complete before treating it as a failure.
	RequestTimeout time.Duration

	// MaxRetries is the maximum number of send attempts before the agent
	// drops a payload and waits for the next tick.
	MaxRetries int

	// RetryBaseDelay is the initial backoff delay before the first retry.
	// Subsequent retries double this value, capped at RetryMaxDelay.
	RetryBaseDelay time.Duration

	// RetryMaxDelay is the ceiling for the exponential backoff delay.
	RetryMaxDelay time.Duration
}

// Load parses flags and environment variables, validates the result, and
// returns an immutable [Config]. It calls [os.Exit](1) on fatal
// validation errors so callers do not need to handle the error path.
func Load() Config {
	var (
		hubURL         = flag.String("hub-url", envOr("HUB_URL", "http://localhost:8080"), "Base URL of the telemetry hub")
		authToken      = flag.String("auth-token", envOr("AUTH_TOKEN", ""), "Bearer token for hub authentication")
		nodeID         = flag.String("node-id", envOr("NODE_ID", ""), "Stable node identifier (default: OS hostname)")
		intervalStr    = flag.String("interval", envOr("INTERVAL", "15s"), "Metric collection interval (e.g. 15s, 1m)")
		logLevelStr    = flag.String("log-level", envOr("LOG_LEVEL", "info"), "Log level: debug|info|warn|error")
		reqTimeout     = flag.Duration("request-timeout", 10*time.Second, "HTTP request timeout")
		maxRetries     = flag.Int("max-retries", 5, "Maximum send retries per tick")
		retryBaseDelay = flag.Duration("retry-base-delay", 500*time.Millisecond, "Initial backoff delay before first retry")
		retryMaxDelay  = flag.Duration("retry-max-delay", 30*time.Second, "Maximum backoff delay cap")
	)

	flag.Parse()

	interval, err := time.ParseDuration(*intervalStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid interval %q: %v\n", *intervalStr, err)
		os.Exit(1)
	}
	if interval < 5*time.Second {
		fmt.Fprintf(os.Stderr, "config: interval %v is too short; minimum is 5s\n", interval)
		os.Exit(1)
	}

	logLevel, err := parseLogLevel(*logLevelStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	resolvedNodeID := *nodeID
	if resolvedNodeID == "" {
		hostname, herr := os.Hostname()
		if herr != nil {
			hostname = "unknown"
		}
		resolvedNodeID = hostname
	}

	if *hubURL == "" {
		fmt.Fprintln(os.Stderr, "config: --hub-url / HUB_URL must not be empty")
		os.Exit(1)
	}

	return Config{
		HubURL:         strings.TrimRight(*hubURL, "/"),
		AuthToken:      *authToken,
		NodeID:         resolvedNodeID,
		Interval:       interval,
		LogLevel:       logLevel,
		RequestTimeout: *reqTimeout,
		MaxRetries:     *maxRetries,
		RetryBaseDelay: *retryBaseDelay,
		RetryMaxDelay:  *retryMaxDelay,
	}
}

// envOr returns the value of the environment variable named key, or fallback
// when the variable is unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseLogLevel maps a human-readable level name to a [slog.Level].
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unrecognised log level %q; use debug|info|warn|error", s)
	}
}
