// Package actions provides a strictly whitelisted remote action execution engine
// and background polling worker for managed nodes.
//
// Security Design:
// Arbitrary shell command execution is strictly forbidden. Only predefined,
// parameter-validated diagnostic and remediation actions are allowed.
package actions

import (
	"context"
	"time"
)

// Whitelisted action identifier constants.
const (
	ActionFlushDNS        = "flush_dns"
	ActionCleanTemp       = "clean_temp"
	ActionPingGateway     = "ping_gateway"
	ActionRestartDocker   = "restart_docker_container"
)

// AllowedActions maps all permitted action identifiers for fast whitelist lookup.
var AllowedActions = map[string]bool{
	ActionFlushDNS:      true,
	ActionCleanTemp:     true,
	ActionPingGateway:   true,
	ActionRestartDocker: true,
}

// IsAllowed returns true if the action name is in the strict whitelist.
func IsAllowed(action string) bool {
	return AllowedActions[action]
}

// ActionRequest represents a command enqueued by an administrator for a node.
type ActionRequest struct {
	ID        string            `json:"id"`
	NodeID    string            `json:"node_id"`
	Action    string            `json:"action"`
	Params    map[string]string `json:"params,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// ActionResult captures the execution output and status of an action.
type ActionResult struct {
	ActionID   string    `json:"action_id"`
	NodeID     string    `json:"node_id"`
	Action     string    `json:"action"`
	Status     string    `json:"status"` // "success" | "failed"
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	ExitCode   int       `json:"exit_code"`
	DurationMs int64     `json:"duration_ms"`
	ExecutedAt time.Time `json:"executed_at"`
}

// ActionClient defines the transport interface for the agent's action worker.
type ActionClient interface {
	FetchPendingActions(ctx context.Context, nodeID string) ([]ActionRequest, error)
	SubmitActionResult(ctx context.Context, res ActionResult) error
}
