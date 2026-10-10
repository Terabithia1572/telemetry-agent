// Package hub – store.go implements the persistence layer for the telemetry hub.
//
// Schema
//
//	nodes            – one row per known agent, upserted on every ingest.
//	metrics_history  – append-only time-series rows pruned to a rolling window.
//
// The package uses the pure-Go modernc.org/sqlite driver so the hub binary
// can be compiled with CGO_ENABLED=0 just like the agent.
//
// SQLite datetime() returns strings in the format "YYYY-MM-DD HH:MM:SS"
// (no timezone suffix). The parseSQLiteTime helper tries multiple layouts so
// the store is robust to both the default format and RFC3339 variants.
package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver

	"github.com/telemetry-agent/internal/hub/alerting"
	"github.com/telemetry-agent/internal/model"
)

// Store is the single access point to the SQLite database.
// All methods are safe for concurrent use (WAL mode + single connection pool).
type Store struct {
	db *sql.DB
}

// NodeRow is a flattened view of the nodes table joined with the latest
// metrics row. It is used exclusively for list/read queries.
type NodeRow struct {
	NodeID        string              `json:"node_id"`
	Hostname      string              `json:"hostname"`
	OS            string              `json:"os"`
	Platform      string              `json:"platform"`
	KernelVersion string              `json:"kernel_version"`
	AgentVersion  string              `json:"agent_version"`
	Status        string              `json:"status"` // "online" | "offline"
	LastSeen      time.Time           `json:"last_seen"`
	CPUPercent    float64             `json:"cpu_percent"`
	MemPercent    float64             `json:"mem_percent"`
	DiskPercent   float64             `json:"disk_percent"`
	NetRxBytes    uint64              `json:"net_rx_bytes"`
	NetTxBytes    uint64                `json:"net_tx_bytes"`
	Processes     []model.ProcessInfo   `json:"processes"`
	Containers    []model.ContainerInfo `json:"containers"`
}

// MetricPoint is a single row from metrics_history, returned by history queries.
type MetricPoint struct {
	Timestamp   time.Time `json:"timestamp"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemPercent  float64   `json:"mem_percent"`
	DiskPercent float64   `json:"disk_percent"`
	NetRxBytes  uint64    `json:"net_rx_bytes"`
	NetTxBytes  uint64    `json:"net_tx_bytes"`
}

// OpenStore opens (or creates) the SQLite database at path and runs the schema
// migration. Use ":memory:" for ephemeral testing.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}

	// SQLite performs best with a single writer connection. WAL mode allows
	// concurrent readers while a write is in progress.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := applySchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// applySchema runs idempotent DDL statements to create the two tables and
// enable WAL journalling.
func applySchema(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS nodes (
			node_id        TEXT PRIMARY KEY,
			hostname       TEXT NOT NULL DEFAULT '',
			os             TEXT NOT NULL DEFAULT '',
			platform       TEXT NOT NULL DEFAULT '',
			kernel_version TEXT NOT NULL DEFAULT '',
			agent_version  TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL DEFAULT 'offline',
			last_seen      DATETIME NOT NULL DEFAULT (datetime('now')),
			processes      TEXT NOT NULL DEFAULT '[]',
			containers     TEXT NOT NULL DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS metrics_history (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp    DATETIME NOT NULL DEFAULT (datetime('now')),
			node_id      TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
			cpu_percent  REAL NOT NULL DEFAULT 0,
			mem_percent  REAL NOT NULL DEFAULT 0,
			disk_percent REAL NOT NULL DEFAULT 0,
			net_rx_bytes INTEGER NOT NULL DEFAULT 0,
			net_tx_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_node_ts ON metrics_history(node_id, timestamp DESC)`,
		`CREATE TABLE IF NOT EXISTS alert_settings (
			id               INTEGER PRIMARY KEY CHECK (id = 1),
			webhook_url      TEXT NOT NULL DEFAULT '',
			cpu_threshold    REAL NOT NULL DEFAULT 90.0,
			memory_threshold REAL NOT NULL DEFAULT 90.0,
			disk_threshold   REAL NOT NULL DEFAULT 90.0,
			enabled          INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS node_actions (
			id          TEXT PRIMARY KEY,
			node_id     TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE CASCADE,
			action      TEXT NOT NULL,
			params      TEXT NOT NULL DEFAULT '{}',
			status      TEXT NOT NULL DEFAULT 'queued',
			stdout      TEXT NOT NULL DEFAULT '',
			stderr      TEXT NOT NULL DEFAULT '',
			exit_code   INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
			executed_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_actions_node_status ON node_actions(node_id, status)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("store: schema migration failed [%s]: %w", stmt[:min(40, len(stmt))], err)
		}
	}

	// Ensure processes and containers columns exist in case nodes table was created in an earlier migration
	_, _ = db.Exec(`ALTER TABLE nodes ADD COLUMN processes TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE nodes ADD COLUMN containers TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`INSERT OR IGNORE INTO alert_settings (id, webhook_url, cpu_threshold, memory_threshold, disk_threshold, enabled) VALUES (1, '', 90.0, 90.0, 90.0, 0)`)

	return nil
}

// UpsertNode inserts or updates the nodes row and appends a metrics_history row
// in a single transaction. offlineAfter is used by GetNodes to derive status.
func (s *Store) UpsertNode(ctx context.Context, p IngestParams) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Ensure LastSeen is populated.
	if p.LastSeen.IsZero() {
		p.LastSeen = time.Now().UTC()
	}

	// Format the timestamp in SQLite-compatible "YYYY-MM-DD HH:MM:SS" so
	// comparisons with datetime('now', ...) work without timezone confusion.
	lastSeenStr := p.LastSeen.UTC().Format("2006-01-02 15:04:05")

	procsJSON, err := json.Marshal(p.Processes)
	if err != nil || len(procsJSON) == 0 {
		procsJSON = []byte("[]")
	}

	containersJSON, err := json.Marshal(p.Containers)
	if err != nil || len(containersJSON) == 0 {
		containersJSON = []byte("[]")
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO nodes (node_id, hostname, os, platform, kernel_version, agent_version, status, last_seen, processes, containers)
		VALUES (?, ?, ?, ?, ?, ?, 'online', ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			hostname       = excluded.hostname,
			os             = excluded.os,
			platform       = excluded.platform,
			kernel_version = excluded.kernel_version,
			agent_version  = excluded.agent_version,
			status         = 'online',
			last_seen      = excluded.last_seen,
			processes      = excluded.processes,
			containers     = excluded.containers`,
		p.NodeID, p.Hostname, p.OS, p.Platform, p.KernelVersion, p.AgentVersion, lastSeenStr, string(procsJSON), string(containersJSON),
	)
	if err != nil {
		return fmt.Errorf("store: upsert node: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO metrics_history (timestamp, node_id, cpu_percent, mem_percent, disk_percent, net_rx_bytes, net_tx_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		lastSeenStr, p.NodeID, p.CPUPercent, p.MemPercent, p.DiskPercent, p.NetRxBytes, p.NetTxBytes,
	)
	if err != nil {
		return fmt.Errorf("store: insert metrics: %w", err)
	}

	// Prune rows older than 24 hours to bound disk growth.
	_, err = tx.ExecContext(ctx,
		`DELETE FROM metrics_history WHERE node_id = ? AND timestamp < datetime('now', '-24 hours')`,
		p.NodeID,
	)
	if err != nil {
		return fmt.Errorf("store: prune metrics: %w", err)
	}

	return tx.Commit()
}

// GetNodes returns all known nodes with their latest metrics, active processes, and containers.
// A node is considered "offline" when its last_seen is older than offlineAfter.
func (s *Store) GetNodes(ctx context.Context, offlineAfter time.Duration) ([]NodeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			n.node_id, n.hostname, n.os, n.platform, n.kernel_version, n.agent_version,
			n.last_seen,
			COALESCE(n.processes, '[]'),
			COALESCE(n.containers, '[]'),
			COALESCE(m.cpu_percent,  0),
			COALESCE(m.mem_percent,  0),
			COALESCE(m.disk_percent, 0),
			COALESCE(m.net_rx_bytes, 0),
			COALESCE(m.net_tx_bytes, 0)
		FROM nodes n
		LEFT JOIN metrics_history m ON m.node_id = n.node_id
			AND m.id = (
				SELECT id FROM metrics_history
				WHERE node_id = n.node_id
				ORDER BY timestamp DESC LIMIT 1
			)
		ORDER BY n.hostname ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: get nodes: %w", err)
	}
	defer rows.Close()

	cutoff := time.Now().UTC().Add(-offlineAfter)
	var result []NodeRow
	for rows.Next() {
		var r NodeRow
		var lastSeenStr string
		var procsJSON string
		var containersJSON string
		if err := rows.Scan(
			&r.NodeID, &r.Hostname, &r.OS, &r.Platform, &r.KernelVersion, &r.AgentVersion,
			&lastSeenStr,
			&procsJSON,
			&containersJSON,
			&r.CPUPercent, &r.MemPercent, &r.DiskPercent, &r.NetRxBytes, &r.NetTxBytes,
		); err != nil {
			return nil, fmt.Errorf("store: scan node row: %w", err)
		}
		// Parse SQLite datetime string using multi-format helper.
		r.LastSeen = parseSQLiteTime(lastSeenStr)
		if err := json.Unmarshal([]byte(procsJSON), &r.Processes); err != nil || r.Processes == nil {
			r.Processes = []model.ProcessInfo{}
		}
		if err := json.Unmarshal([]byte(containersJSON), &r.Containers); err != nil || r.Containers == nil {
			r.Containers = []model.ContainerInfo{}
		}

		if r.LastSeen.Before(cutoff) {
			r.Status = "offline"
		} else {
			r.Status = "online"
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetNode returns details for a single node specified by nodeID.
func (s *Store) GetNode(ctx context.Context, nodeID string, offlineAfter time.Duration) (*NodeRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			n.node_id, n.hostname, n.os, n.platform, n.kernel_version, n.agent_version,
			n.last_seen,
			COALESCE(n.processes, '[]'),
			COALESCE(n.containers, '[]'),
			COALESCE(m.cpu_percent,  0),
			COALESCE(m.mem_percent,  0),
			COALESCE(m.disk_percent, 0),
			COALESCE(m.net_rx_bytes, 0),
			COALESCE(m.net_tx_bytes, 0)
		FROM nodes n
		LEFT JOIN metrics_history m ON m.node_id = n.node_id
			AND m.id = (
				SELECT id FROM metrics_history
				WHERE node_id = n.node_id
				ORDER BY timestamp DESC LIMIT 1
			)
		WHERE n.node_id = ?`, nodeID)

	var r NodeRow
	var lastSeenStr string
	var procsJSON string
	var containersJSON string
	if err := row.Scan(
		&r.NodeID, &r.Hostname, &r.OS, &r.Platform, &r.KernelVersion, &r.AgentVersion,
		&lastSeenStr,
		&procsJSON,
		&containersJSON,
		&r.CPUPercent, &r.MemPercent, &r.DiskPercent, &r.NetRxBytes, &r.NetTxBytes,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get node: %w", err)
	}

	r.LastSeen = parseSQLiteTime(lastSeenStr)
	if err := json.Unmarshal([]byte(procsJSON), &r.Processes); err != nil || r.Processes == nil {
		r.Processes = []model.ProcessInfo{}
	}
	if err := json.Unmarshal([]byte(containersJSON), &r.Containers); err != nil || r.Containers == nil {
		r.Containers = []model.ContainerInfo{}
	}

	cutoff := time.Now().UTC().Add(-offlineAfter)
	if r.LastSeen.Before(cutoff) {
		r.Status = "offline"
	} else {
		r.Status = "online"
	}
	return &r, nil
}

// GetHistory returns up to limit metric points for nodeID within the last
// window duration, ordered oldest-first for chart rendering.
func (s *Store) GetHistory(ctx context.Context, nodeID string, window time.Duration, limit int) ([]MetricPoint, error) {
	if limit <= 0 {
		limit = 120
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT timestamp, cpu_percent, mem_percent, disk_percent, net_rx_bytes, net_tx_bytes
		FROM metrics_history
		WHERE node_id = ? AND timestamp >= datetime('now', ?)
		ORDER BY timestamp ASC
		LIMIT ?`,
		nodeID, fmt.Sprintf("-%d seconds", int(window.Seconds())), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get history: %w", err)
	}
	defer rows.Close()

	var pts []MetricPoint
	for rows.Next() {
		var pt MetricPoint
		var tsStr string
		if err := rows.Scan(&tsStr, &pt.CPUPercent, &pt.MemPercent, &pt.DiskPercent, &pt.NetRxBytes, &pt.NetTxBytes); err != nil {
			return nil, fmt.Errorf("store: scan history row: %w", err)
		}
		pt.Timestamp = parseSQLiteTime(tsStr)
		pts = append(pts, pt)
	}
	return pts, rows.Err()
}

// IngestParams carries the extracted fields from a TelemetryReport needed
// for persistence. It decouples the store from the model package to keep
// dependency arrows pointing inward.
type IngestParams struct {
	NodeID        string
	Hostname      string
	OS            string
	Platform      string
	KernelVersion string
	AgentVersion  string
	LastSeen      time.Time
	CPUPercent    float64
	MemPercent    float64
	DiskPercent   float64
	NetRxBytes    uint64
	NetTxBytes    uint64
	Processes     []model.ProcessInfo
	Containers    []model.ContainerInfo
}

// GetAlertSettings returns the current threshold and webhook configuration.
func (s *Store) GetAlertSettings(ctx context.Context) (alerting.Settings, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT webhook_url, cpu_threshold, memory_threshold, disk_threshold, enabled
		FROM alert_settings WHERE id = 1`)

	var cfg alerting.Settings
	var enabledInt int
	if err := row.Scan(&cfg.WebhookURL, &cfg.CPUThreshold, &cfg.MemoryThreshold, &cfg.DiskThreshold, &enabledInt); err != nil {
		if err == sql.ErrNoRows {
			return alerting.Settings{
				CPUThreshold:    90.0,
				MemoryThreshold: 90.0,
				DiskThreshold:   90.0,
				Enabled:         false,
			}, nil
		}
		return cfg, fmt.Errorf("store: get alert settings: %w", err)
	}
	cfg.Enabled = enabledInt == 1
	return cfg, nil
}

// SaveAlertSettings upserts the threshold and webhook configuration.
func (s *Store) SaveAlertSettings(ctx context.Context, cfg alerting.Settings) error {
	enabledInt := 0
	if cfg.Enabled {
		enabledInt = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_settings (id, webhook_url, cpu_threshold, memory_threshold, disk_threshold, enabled)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			webhook_url      = excluded.webhook_url,
			cpu_threshold    = excluded.cpu_threshold,
			memory_threshold = excluded.memory_threshold,
			disk_threshold   = excluded.disk_threshold,
			enabled          = excluded.enabled`,
		cfg.WebhookURL, cfg.CPUThreshold, cfg.MemoryThreshold, cfg.DiskThreshold, enabledInt,
	)
	if err != nil {
		return fmt.Errorf("store: save alert settings: %w", err)
	}
	return nil
}

// GetNodeStatuses returns minimal host status records for offline monitoring.
func (s *Store) GetNodeStatuses(ctx context.Context, offlineAfter time.Duration) ([]alerting.NodeStatus, error) {
	nodes, err := s.GetNodes(ctx, offlineAfter)
	if err != nil {
		return nil, err
	}
	statuses := make([]alerting.NodeStatus, len(nodes))
	for i, n := range nodes {
		statuses[i] = alerting.NodeStatus{
			NodeID:   n.NodeID,
			Hostname: n.Hostname,
			LastSeen: n.LastSeen,
			Status:   n.Status,
		}
	}
	return statuses, nil
}

// min returns the smaller of a and b (backport for Go <1.21 build compat).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseSQLiteTime attempts to parse a datetime string returned by SQLite.
// SQLite's datetime() function returns "YYYY-MM-DD HH:MM:SS" (UTC, no zone).
// We also accept ISO8601, RFC3339, and RFC3339Nano for timestamps inserted by Go code or external drivers.
func parseSQLiteTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ActionRecord represents a remote action row in the node_actions table.
type ActionRecord struct {
	ID         string            `json:"id"`
	NodeID     string            `json:"node_id"`
	Action     string            `json:"action"`
	Params     map[string]string `json:"params"`
	Status     string            `json:"status"` // "queued" | "running" | "success" | "failed"
	Stdout     string            `json:"stdout"`
	Stderr     string            `json:"stderr"`
	ExitCode   int               `json:"exit_code"`
	DurationMs int64             `json:"duration_ms"`
	CreatedAt  time.Time         `json:"created_at"`
	ExecutedAt *time.Time        `json:"executed_at,omitempty"`
}

// EnqueueAction inserts a new pending action row into node_actions.
func (s *Store) EnqueueAction(ctx context.Context, actionID, nodeID, action string, params map[string]string) (*ActionRecord, error) {
	if params == nil {
		params = make(map[string]string)
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		paramsJSON = []byte("{}")
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO node_actions (id, node_id, action, params, status, created_at)
		VALUES (?, ?, ?, ?, 'queued', ?)`,
		actionID, nodeID, action, string(paramsJSON), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("store: enqueue action: %w", err)
	}

	return &ActionRecord{
		ID:        actionID,
		NodeID:    nodeID,
		Action:    action,
		Params:    params,
		Status:    "queued",
		CreatedAt: now,
	}, nil
}

// GetPendingActions returns all queued actions for nodeID and transitions their status to 'running'.
func (s *Store) GetPendingActions(ctx context.Context, nodeID string) ([]ActionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, node_id, action, params, status, created_at
		FROM node_actions
		WHERE node_id = ? AND status = 'queued'
		ORDER BY created_at ASC`,
		nodeID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get pending actions: %w", err)
	}
	defer rows.Close()

	var results []ActionRecord
	var ids []string
	for rows.Next() {
		var rec ActionRecord
		var paramsStr, createdAtStr string
		if err := rows.Scan(&rec.ID, &rec.NodeID, &rec.Action, &paramsStr, &rec.Status, &createdAtStr); err != nil {
			return nil, fmt.Errorf("store: scan pending action: %w", err)
		}
		_ = json.Unmarshal([]byte(paramsStr), &rec.Params)
		if rec.Params == nil {
			rec.Params = make(map[string]string)
		}
		rec.CreatedAt = parseSQLiteTime(createdAtStr)
		results = append(results, rec)
		ids = append(ids, rec.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: rows pending actions: %w", err)
	}

	// Mark fetched actions as 'running' so they are not dispatched multiple times
	for _, id := range ids {
		_, _ = s.db.ExecContext(ctx, `UPDATE node_actions SET status = 'running' WHERE id = ?`, id)
	}

	if results == nil {
		results = []ActionRecord{}
	}
	return results, nil
}

// SaveActionResult updates an action record with its execution outcome.
func (s *Store) SaveActionResult(ctx context.Context, actionID, nodeID string, status string, stdout, stderr string, exitCode int, durationMs int64) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE node_actions
		SET status = ?, stdout = ?, stderr = ?, exit_code = ?, duration_ms = ?, executed_at = ?
		WHERE id = ? AND node_id = ?`,
		status, stdout, stderr, exitCode, durationMs, now.Format(time.RFC3339), actionID, nodeID,
	)
	if err != nil {
		return fmt.Errorf("store: save action result: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		_, err = s.db.ExecContext(ctx, `
			UPDATE node_actions
			SET status = ?, stdout = ?, stderr = ?, exit_code = ?, duration_ms = ?, executed_at = ?
			WHERE id = ?`,
			status, stdout, stderr, exitCode, durationMs, now.Format(time.RFC3339), actionID,
		)
		if err != nil {
			return fmt.Errorf("store: save action result fallback: %w", err)
		}
	}
	return nil
}

// GetAction retrieves a single action by its unique ID.
func (s *Store) GetAction(ctx context.Context, actionID string) (*ActionRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, node_id, action, params, status, stdout, stderr, exit_code, duration_ms, created_at, executed_at
		FROM node_actions WHERE id = ?`,
		actionID,
	)

	var rec ActionRecord
	var paramsStr, createdAtStr string
	var executedAtStr sql.NullString
	if err := row.Scan(
		&rec.ID, &rec.NodeID, &rec.Action, &paramsStr, &rec.Status,
		&rec.Stdout, &rec.Stderr, &rec.ExitCode, &rec.DurationMs,
		&createdAtStr, &executedAtStr,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get action: %w", err)
	}

	_ = json.Unmarshal([]byte(paramsStr), &rec.Params)
	if rec.Params == nil {
		rec.Params = make(map[string]string)
	}
	rec.CreatedAt = parseSQLiteTime(createdAtStr)
	if executedAtStr.Valid && executedAtStr.String != "" {
		t := parseSQLiteTime(executedAtStr.String)
		rec.ExecutedAt = &t
	}
	return &rec, nil
}

// GetNodeActions returns the most recent actions executed for a node.
func (s *Store) GetNodeActions(ctx context.Context, nodeID string, limit int) ([]ActionRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, node_id, action, params, status, stdout, stderr, exit_code, duration_ms, created_at, executed_at
		FROM node_actions WHERE node_id = ?
		ORDER BY created_at DESC LIMIT ?`,
		nodeID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get node actions: %w", err)
	}
	defer rows.Close()

	var results []ActionRecord
	for rows.Next() {
		var rec ActionRecord
		var paramsStr, createdAtStr string
		var executedAtStr sql.NullString
		if err := rows.Scan(
			&rec.ID, &rec.NodeID, &rec.Action, &paramsStr, &rec.Status,
			&rec.Stdout, &rec.Stderr, &rec.ExitCode, &rec.DurationMs,
			&createdAtStr, &executedAtStr,
		); err != nil {
			return nil, fmt.Errorf("store: scan node action: %w", err)
		}
		_ = json.Unmarshal([]byte(paramsStr), &rec.Params)
		if rec.Params == nil {
			rec.Params = make(map[string]string)
		}
		rec.CreatedAt = parseSQLiteTime(createdAtStr)
		if executedAtStr.Valid && executedAtStr.String != "" {
			t := parseSQLiteTime(executedAtStr.String)
			rec.ExecutedAt = &t
		}
		results = append(results, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: rows node actions: %w", err)
	}
	if results == nil {
		results = []ActionRecord{}
	}
	return results, nil
}
