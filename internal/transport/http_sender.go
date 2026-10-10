// Package transport – http_sender.go provides an HTTP POST implementation of
// [Sender] with exponential backoff, per-request jitter, and full context
// propagation.
//
// Retry behaviour:
//
//	attempt 1: immediate
//	attempt 2: baseDelay * 2^0  + jitter
//	attempt 3: baseDelay * 2^1  + jitter
//	…
//	attempt N: min(baseDelay * 2^(N-2), maxDelay) + jitter
//
// jitter is a uniform random fraction in [0, 25 %] of the computed delay to
// spread retry storms across a fleet of agents.
//
// The sender uses a dedicated http.Client with explicit timeouts; it never
// touches http.DefaultClient.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/telemetry-agent/internal/actions"
	"github.com/telemetry-agent/internal/model"
)

// HTTPSender is a [Sender] that delivers payloads via HTTP POST.
type HTTPSender struct {
	client         *http.Client
	endpoint       string // e.g. "https://hub.example.com/api/v1/metrics"
	hubURL         string // base hub URL
	authToken      string
	nodeID         string
	maxRetries     int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration
	log            *slog.Logger
	rng            *rand.Rand
}

// HTTPSenderConfig groups the parameters needed to construct an [HTTPSender].
type HTTPSenderConfig struct {
	// HubURL is the base URL of the hub; the sender appends /api/v1/metrics.
	HubURL string

	// AuthToken is an optional bearer token.  An empty string omits the
	// Authorization header.
	AuthToken string

	// NodeID is embedded in the X-Node-ID header on every request.
	NodeID string

	// RequestTimeout is the end-to-end timeout for a single HTTP request
	// (not including retry delays).
	RequestTimeout time.Duration

	// MaxRetries is the maximum number of send attempts per payload.
	// A value of 0 means try once (no retries).
	MaxRetries int

	// RetryBaseDelay is the initial backoff delay before the first retry.
	RetryBaseDelay time.Duration

	// RetryMaxDelay is the ceiling for the computed backoff delay.
	RetryMaxDelay time.Duration

	// Logger is the structured logger; defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// NewHTTPSender constructs an [HTTPSender] ready to deliver payloads.
// It validates the configuration and returns an error for obviously invalid
// inputs so callers can fail fast at startup.
func NewHTTPSender(cfg HTTPSenderConfig) (*HTTPSender, error) {
	if cfg.HubURL == "" {
		return nil, fmt.Errorf("http_sender: HubURL must not be empty")
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 500 * time.Millisecond
	}
	if cfg.RetryMaxDelay <= 0 {
		cfg.RetryMaxDelay = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	transport := &http.Transport{
		// Keep-alive pool: reuse connections across ticks for efficiency.
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		// Conservative TLS handshake timeout.
		TLSHandshakeTimeout: 5 * time.Second,
		// Cap connection establishment time.
		ResponseHeaderTimeout: cfg.RequestTimeout,
	}

	return &HTTPSender{
		client: &http.Client{
			Timeout:   cfg.RequestTimeout,
			Transport: transport,
		},
		endpoint:       strings.TrimRight(cfg.HubURL, "/") + "/api/v1/metrics",
		hubURL:         strings.TrimRight(cfg.HubURL, "/"),
		authToken:      cfg.AuthToken,
		nodeID:         cfg.NodeID,
		maxRetries:     cfg.MaxRetries,
		retryBaseDelay: cfg.RetryBaseDelay,
		retryMaxDelay:  cfg.RetryMaxDelay,
		log:            logger,
		rng:            rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec
	}, nil
}

// Send implements [Sender].  It marshals report to JSON and POST-s it to the
// hub endpoint, retrying on transient failures with exponential backoff.
func (s *HTTPSender) Send(ctx context.Context, report *model.TelemetryReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		// Marshalling should never fail for our well-defined types, but guard
		// against future additions.
		return fmt.Errorf("http_sender: marshal failed: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		// Honour context cancellation before each attempt.
		if ctx.Err() != nil {
			return fmt.Errorf("http_sender: context cancelled before attempt %d: %w", attempt+1, ctx.Err())
		}

		if attempt > 0 {
			delay := s.backoffDelay(attempt)
			s.log.Debug("retrying send",
				"attempt", attempt+1,
				"max", s.maxRetries+1,
				"backoff", delay,
				"last_error", lastErr,
			)
			select {
			case <-ctx.Done():
				return fmt.Errorf("http_sender: context cancelled during backoff: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		lastErr = s.doPost(ctx, body, report.NodeID, report.CollectedAt.String())
		if lastErr == nil {
			if attempt > 0 {
				s.log.Info("send succeeded after retry", "attempt", attempt+1)
			}
			return nil
		}
		s.log.Warn("send attempt failed",
			"attempt", attempt+1,
			"max", s.maxRetries+1,
			"error", lastErr,
		)
	}

	return fmt.Errorf("http_sender: all %d attempts failed; last error: %w", s.maxRetries+1, lastErr)
}

// doPost performs a single HTTP POST with the provided body bytes.
func (s *HTTPSender) doPost(ctx context.Context, body []byte, nodeID, collectedAt string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Node-ID", nodeID)
	req.Header.Set("X-Collected-At", collectedAt)
	req.Header.Set("User-Agent", "telemetry-agent/1.0")

	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// backoffDelay computes the delay before attempt number n (1-indexed, where 1
// is the first retry).  The formula is:
//
//	delay = min(base * 2^(n-1), max) * (1 + jitter)
//
// where jitter ∈ [0, 0.25].
func (s *HTTPSender) backoffDelay(attempt int) time.Duration {
	shift := attempt - 1
	if shift > 30 {
		shift = 30 // prevent overflow for large retry counts
	}
	delay := s.retryBaseDelay * (1 << uint(shift))
	if delay > s.retryMaxDelay || delay <= 0 { // <= 0 catches overflow
		delay = s.retryMaxDelay
	}

	// Add up to 25 % jitter.
	jitterFrac := s.rng.Float64() * 0.25
	delay = time.Duration(float64(delay) * (1 + jitterFrac))

	if delay > s.retryMaxDelay {
		delay = s.retryMaxDelay
	}
	return delay
}

// Close implements [Sender].  It drains idle connections so the agent binary
// exits cleanly without hanging OS-level sockets.
func (s *HTTPSender) Close() error {
	if t, ok := s.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	return nil
}

// FetchPendingActions retrieves any queued commands dispatched to this node.
func (s *HTTPSender) FetchPendingActions(ctx context.Context, nodeID string) ([]actions.ActionRequest, error) {
	targetURL := fmt.Sprintf("%s/api/v1/nodes/%s/actions/pending", s.hubURL, url.PathEscape(nodeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build pending actions request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Node-ID", nodeID)
	req.Header.Set("User-Agent", "telemetry-agent/1.0")
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch pending actions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub returned HTTP %d on pending actions", resp.StatusCode)
	}

	var pending []actions.ActionRequest
	if err := json.NewDecoder(resp.Body).Decode(&pending); err != nil {
		return nil, fmt.Errorf("decode pending actions: %w", err)
	}
	return pending, nil
}

// SubmitActionResult delivers action execution stdout, stderr, and exit code to the hub.
func (s *HTTPSender) SubmitActionResult(ctx context.Context, res actions.ActionResult) error {
	targetURL := fmt.Sprintf("%s/api/v1/nodes/%s/actions/%s/result", s.hubURL, url.PathEscape(res.NodeID), url.PathEscape(res.ActionID))

	payload := map[string]interface{}{
		"status":      res.Status,
		"stdout":      res.Stdout,
		"stderr":      res.Stderr,
		"exit_code":   res.ExitCode,
		"duration_ms": res.DurationMs,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal action result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build action result request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", res.NodeID)
	req.Header.Set("User-Agent", "telemetry-agent/1.0")
	if s.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.authToken)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("submit action result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub returned HTTP %d on action result", resp.StatusCode)
	}
	return nil
}
