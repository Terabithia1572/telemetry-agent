// Package transport defines the Sender interface used by the orchestrator to
// deliver assembled TelemetryReports to an upstream hub.
//
// Concrete implementations live in separate files within this package
// (http_sender.go, etc.) and are selected by the caller at wiring time.
package transport

import (
	"context"

	"github.com/telemetry-agent/internal/model"
)

// Sender is the contract that all transport backends must satisfy.
// Send must honour context cancellation and must be safe to call from a
// single goroutine (the orchestrator tick).  Implementations are responsible
// for their own retry logic.
type Sender interface {
	// Send transmits report to the upstream hub.  It returns nil on success,
	// or a descriptive error when the delivery ultimately fails after all
	// retries are exhausted.
	Send(ctx context.Context, report *model.TelemetryReport) error

	// Close releases any persistent resources held by the sender (e.g. HTTP
	// keep-alive connections, WebSocket sessions).  It must be safe to call
	// even if Send has never been called.
	Close() error
}
