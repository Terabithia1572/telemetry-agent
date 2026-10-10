package alerting

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// alertState tracks the firing status and timestamp for a specific condition.
type alertState struct {
	firing       bool
	lastFired    time.Time
	lastResolved time.Time
}

// Engine evaluates metrics thresholds and offline status, handles flapping protection/cooldown,
// and orchestrates alert dispatching.
type Engine struct {
	store      AlertStore
	dispatcher *Dispatcher
	log        *slog.Logger
	cooldown   time.Duration

	mu       sync.RWMutex
	settings Settings
	states   map[string]*alertState // key: "nodeID:alertType"
}

// NewEngine creates an alert Engine with default 10-minute cooldown window.
func NewEngine(store AlertStore, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}

	return &Engine{
		store:      store,
		dispatcher: NewDispatcher(),
		log:        log,
		cooldown:   10 * time.Minute,
		states:     make(map[string]*alertState),
	}
}

// Start loads initial settings and launches the background loop for offline node monitoring.
func (e *Engine) Start(ctx context.Context) {
	// Load initial settings from store
	if s, err := e.store.GetAlertSettings(ctx); err == nil {
		e.mu.Lock()
		e.settings = s
		e.mu.Unlock()
		e.log.Info("alert engine initialized", "enabled", s.Enabled, "webhook", s.WebhookURL != "")
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.log.Info("alert engine stopped")
			return
		case <-ticker.C:
			e.checkOfflineNodes(ctx)
		}
	}
}

// UpdateSettings updates the in-memory cached alert settings.
func (e *Engine) UpdateSettings(s Settings) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settings = s
}

// GetSettings returns current in-memory cached alert settings.
func (e *Engine) GetSettings() Settings {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.settings
}

// TestWebhook sends a test notification using the specified or stored webhook URL.
func (e *Engine) TestWebhook(ctx context.Context, webhookURL string) error {
	if webhookURL == "" {
		e.mu.RLock()
		webhookURL = e.settings.WebhookURL
		e.mu.RUnlock()
	}
	return e.dispatcher.TestWebhook(ctx, webhookURL)
}

// EvaluateIngest evaluates CPU, Memory, and Disk metrics from an ingested report.
func (e *Engine) EvaluateIngest(ctx context.Context, p IngestMetrics) {
	e.mu.RLock()
	s := e.settings
	e.mu.RUnlock()

	if !s.Enabled || s.WebhookURL == "" {
		return
	}

	now := time.Now().UTC()

	// 1. CPU Alert
	e.evaluateMetric(ctx, p.NodeID, p.Hostname, "CPU", p.CPUPercent, s.CPUThreshold, "%", now)

	// 2. Memory Alert
	e.evaluateMetric(ctx, p.NodeID, p.Hostname, "Memory", p.MemPercent, s.MemoryThreshold, "%", now)

	// 3. Disk Alert
	e.evaluateMetric(ctx, p.NodeID, p.Hostname, "Disk", p.DiskPercent, s.DiskThreshold, "%", now)

	// 4. Also resolve offline alert if node had fired offline and is now back online
	e.resolveCondition(ctx, p.NodeID, p.Hostname, "Node Status", "online", "30s", now)
}

// evaluateMetric checks if value exceeds threshold and transitions firing/resolved states.
func (e *Engine) evaluateMetric(
	_ context.Context,
	nodeID, hostname, metric string,
	value, threshold float64,
	unit string,
	now time.Time,
) {
	key := fmt.Sprintf("%s:%s", nodeID, metric)
	valStr := fmt.Sprintf("%.1f%s", value, unit)
	threshStr := fmt.Sprintf("%.1f%s", threshold, unit)

	e.mu.Lock()
	enabled := e.settings.Enabled
	webhookURL := e.settings.WebhookURL
	if !enabled || webhookURL == "" {
		e.mu.Unlock()
		return
	}

	st, exists := e.states[key]
	if !exists {
		st = &alertState{}
		e.states[key] = st
	}

	var toDispatch *AlertEvent

	if value > threshold {
		// Condition met: FIRING
		shouldNotify := false
		if !st.firing {
			shouldNotify = true
			st.firing = true
			st.lastFired = now
		} else if now.Sub(st.lastFired) >= e.cooldown {
			// Cooldown window passed, send reminder
			shouldNotify = true
			st.lastFired = now
		}

		if shouldNotify {
			toDispatch = &AlertEvent{
				NodeID:       nodeID,
				Hostname:     hostname,
				Metric:       metric,
				CurrentValue: valStr,
				Threshold:    threshStr,
				IsResolved:   false,
				Timestamp:    now,
			}
		}
	} else {
		// Condition not met: RESOLVED
		if st.firing {
			st.firing = false
			st.lastResolved = now
			toDispatch = &AlertEvent{
				NodeID:       nodeID,
				Hostname:     hostname,
				Metric:       metric,
				CurrentValue: valStr,
				Threshold:    threshStr,
				IsResolved:   true,
				Timestamp:    now,
			}
		}
	}
	e.mu.Unlock()

	if toDispatch != nil {
		event := *toDispatch
		go func() {
			if err := e.dispatcher.Send(context.Background(), webhookURL, event); err != nil {
				e.log.Warn("failed to send alert", "metric", metric, "node", hostname, "error", err)
			} else {
				e.log.Info("alert dispatched", "metric", metric, "node", hostname, "value", valStr, "resolved", event.IsResolved)
			}
		}()
	}
}

// resolveCondition manually resolves a condition if it was previously firing.
func (e *Engine) resolveCondition(
	_ context.Context,
	nodeID, hostname, metric, currentVal, threshStr string,
	now time.Time,
) {
	key := fmt.Sprintf("%s:%s", nodeID, metric)

	e.mu.Lock()
	enabled := e.settings.Enabled
	webhookURL := e.settings.WebhookURL
	st, exists := e.states[key]
	if !enabled || webhookURL == "" || !exists || !st.firing {
		e.mu.Unlock()
		return
	}

	st.firing = false
	st.lastResolved = now
	e.mu.Unlock()

	event := AlertEvent{
		NodeID:       nodeID,
		Hostname:     hostname,
		Metric:       metric,
		CurrentValue: currentVal,
		Threshold:    threshStr,
		IsResolved:   true,
		Timestamp:    now,
	}

	go func() {
		if err := e.dispatcher.Send(context.Background(), webhookURL, event); err != nil {
			e.log.Warn("failed to send resolved alert", "metric", metric, "node", hostname, "error", err)
		} else {
			e.log.Info("resolved alert dispatched", "metric", metric, "node", hostname, "value", currentVal)
		}
	}()
}

// checkOfflineNodes inspects all nodes and fires alerts for any node silent for >30 seconds.
func (e *Engine) checkOfflineNodes(ctx context.Context) {
	e.mu.RLock()
	s := e.settings
	e.mu.RUnlock()

	if !s.Enabled || s.WebhookURL == "" {
		return
	}

	nodes, err := e.store.GetNodeStatuses(ctx, 30*time.Second)
	if err != nil {
		return
	}

	now := time.Now().UTC()

	for _, n := range nodes {
		isOffline := n.Status == "offline" || now.Sub(n.LastSeen) > 30*time.Second
		key := fmt.Sprintf("%s:Node Status", n.NodeID)

		e.mu.Lock()
		st, exists := e.states[key]
		if !exists {
			st = &alertState{}
			e.states[key] = st
		}
		webhookURL := e.settings.WebhookURL

		var toDispatch *AlertEvent
		if isOffline {
			shouldNotify := false
			if !st.firing {
				shouldNotify = true
				st.firing = true
				st.lastFired = now
			} else if now.Sub(st.lastFired) >= e.cooldown {
				shouldNotify = true
				st.lastFired = now
			}

			if shouldNotify {
				toDispatch = &AlertEvent{
					NodeID:       n.NodeID,
					Hostname:     n.Hostname,
					Metric:       "Node Status",
					CurrentValue: fmt.Sprintf("offline (silent for %ds)", int(now.Sub(n.LastSeen).Seconds())),
					Threshold:    "30s",
					IsResolved:   false,
					Timestamp:    now,
				}
			}
		} else {
			if st.firing {
				st.firing = false
				st.lastResolved = now
				toDispatch = &AlertEvent{
					NodeID:       n.NodeID,
					Hostname:     n.Hostname,
					Metric:       "Node Status",
					CurrentValue: "online",
					Threshold:    "30s",
					IsResolved:   true,
					Timestamp:    now,
				}
			}
		}
		e.mu.Unlock()

		if toDispatch != nil {
			event := *toDispatch
			go func() {
				if err := e.dispatcher.Send(context.Background(), webhookURL, event); err != nil {
					e.log.Warn("failed to send offline alert", "node", event.Hostname, "error", err)
				} else {
					e.log.Info("offline alert dispatched", "node", event.Hostname, "resolved", event.IsResolved)
				}
			}()
		}
	}
}
