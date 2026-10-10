// Package alerting implements threshold-based alerting, node offline detection,
// alert state tracking with cooldown/flapping protection, and webhook dispatching.
package alerting

import (
	"context"
	"time"
)

// Settings holds user-configurable thresholds and webhook endpoints.
type Settings struct {
	WebhookURL      string  `json:"webhook_url"`
	CPUThreshold    float64 `json:"cpu_threshold"`
	MemoryThreshold float64 `json:"memory_threshold"`
	DiskThreshold   float64 `json:"disk_threshold"`
	Enabled         bool    `json:"enabled"`
}

// NodeStatus carries the minimum host status needed for offline alert evaluation.
type NodeStatus struct {
	NodeID   string
	Hostname string
	LastSeen time.Time
	Status   string
}

// IngestMetrics carries current metric levels for threshold evaluation.
type IngestMetrics struct {
	NodeID      string
	Hostname    string
	CPUPercent  float64
	MemPercent  float64
	DiskPercent float64
}

// AlertStore is the persistence interface needed by the alerting engine.
type AlertStore interface {
	GetAlertSettings(ctx context.Context) (Settings, error)
	SaveAlertSettings(ctx context.Context, s Settings) error
	GetNodeStatuses(ctx context.Context, offlineAfter time.Duration) ([]NodeStatus, error)
}

// AlertEvent represents a single transition (firing or resolved) to be notified.
type AlertEvent struct {
	NodeID       string
	Hostname     string
	Metric       string // "CPU", "Memory", "Disk", "Node Status"
	CurrentValue string // e.g. "94.2%", "offline"
	Threshold    string // e.g. "90.0%", "30s"
	IsResolved   bool
	Timestamp    time.Time
}
