// Package collector defines the Collector interface and the orchestration
// ticker that drives periodic metric collection across all registered
// subsystem collectors.
//
// Design goals:
//   - The orchestrator owns a single time.Ticker; individual collectors do not
//     create their own goroutines.
//   - All collection operations are bounded by the context passed to [Run], so
//     the caller retains full lifecycle control.
//   - If a single subsystem collector returns an error, the orchestrator logs
//     the failure and continues with the partial payload rather than dropping
//     the whole report.
package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/telemetry-agent/internal/model"
)

// Collector is the contract implemented by every subsystem collector
// (CPU, memory, disk, network, host).  Collect must be safe to call
// concurrently and must honour context cancellation.
type Collector interface {
	// Name returns a short, stable human-readable identifier used in log
	// messages, e.g. "cpu", "memory", "disk".
	Name() string

	// Collect gathers a fresh snapshot of the subsystem metrics and writes
	// the results into report. The method must return promptly when ctx is
	// cancelled; partial writes to report are acceptable in that case.
	Collect(ctx context.Context, report *model.TelemetryReport) error
}

// Orchestrator drives the periodic collection loop and fans out each tick
// across the registered set of [Collector] implementations.
type Orchestrator struct {
	collectors []Collector
	interval   time.Duration
	nodeID     string
	version    string
	log        *slog.Logger
	onReport   func(context.Context, *model.TelemetryReport) error
}

// OrchestratorOption is a functional option for [NewOrchestrator].
type OrchestratorOption func(*Orchestrator)

// WithCollectors registers subsystem collectors with the orchestrator.
func WithCollectors(cc ...Collector) OrchestratorOption {
	return func(o *Orchestrator) {
		o.collectors = append(o.collectors, cc...)
	}
}

// NewOrchestrator constructs an [Orchestrator] that ticks at the given interval,
// calls all registered collectors, and passes the assembled report to onReport.
// onReport is typically the transport sender.
func NewOrchestrator(
	interval time.Duration,
	nodeID, version string,
	logger *slog.Logger,
	onReport func(context.Context, *model.TelemetryReport) error,
	opts ...OrchestratorOption,
) *Orchestrator {
	o := &Orchestrator{
		interval: interval,
		nodeID:   nodeID,
		version:  version,
		log:      logger,
		onReport: onReport,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Run starts the orchestration loop and blocks until ctx is cancelled.
// It fires an immediate tick on startup so the hub receives an initial
// payload without waiting for the first interval to elapse.
func (o *Orchestrator) Run(ctx context.Context) {
	o.log.Info("orchestrator started", "interval", o.interval, "collectors", len(o.collectors))

	// Fire immediately so the hub gets baseline data at agent startup.
	o.tick(ctx)

	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.log.Info("orchestrator stopping", "reason", ctx.Err())
			return
		case <-ticker.C:
			o.tick(ctx)
		}
	}
}

// tick executes a single collection + send cycle.
func (o *Orchestrator) tick(ctx context.Context) {
	report := &model.TelemetryReport{
		NodeID:       o.nodeID,
		AgentVersion: o.version,
		CollectedAt:  time.Now().UTC(),
	}

	for _, c := range o.collectors {
		if ctx.Err() != nil {
			return
		}
		if err := c.Collect(ctx, report); err != nil {
			o.log.Warn("collector error", "collector", c.Name(), "error", err)
		}
	}

	if ctx.Err() != nil {
		return
	}

	if err := o.onReport(ctx, report); err != nil {
		o.log.Error("failed to send report", "error", err)
	}
}
