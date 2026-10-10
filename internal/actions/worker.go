package actions

import (
	"context"
	"log/slog"
	"time"
)

// Worker polls the Telemetry Hub for pending actions, executes them safely,
// and transmits execution results back to the hub.
type Worker struct {
	client   ActionClient
	runner   *Runner
	log      *slog.Logger
	nodeID   string
	interval time.Duration
}

// NewWorker creates an initialized Worker.
func NewWorker(client ActionClient, runner *Runner, log *slog.Logger, nodeID string, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		client:   client,
		runner:   runner,
		log:      log,
		nodeID:   nodeID,
		interval: interval,
	}
}

// Run starts the action polling loop until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.log.Info("action worker started", "node_id", w.nodeID, "poll_interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("action worker stopped")
			return
		case <-ticker.C:
			w.pollAndExecute(ctx)
		}
	}
}

// pollAndExecute checks for pending actions and dispatches them.
func (w *Worker) pollAndExecute(ctx context.Context) {
	pending, err := w.client.FetchPendingActions(ctx, w.nodeID)
	if err != nil {
		// Log debug so we don't spam if hub is briefly unreachable
		w.log.Debug("failed to fetch pending actions", "error", err)
		return
	}

	for _, req := range pending {
		if ctx.Err() != nil {
			return
		}

		w.log.Info("executing remote action", "action_id", req.ID, "action", req.Action, "node_id", req.NodeID)
		res := w.runner.Execute(ctx, req)

		w.log.Info("action execution finished",
			"action_id", res.ActionID,
			"action", res.Action,
			"status", res.Status,
			"exit_code", res.ExitCode,
			"duration_ms", res.DurationMs,
		)

		if err := w.client.SubmitActionResult(ctx, res); err != nil {
			w.log.Warn("failed to submit action result to hub", "action_id", res.ActionID, "error", err)
		}
	}
}
