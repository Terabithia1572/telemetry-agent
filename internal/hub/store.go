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
	NetTxBytes    uint64              `json:"net_tx_bytes"`
	Processes     []model.ProcessInfo `json:"processes"`
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
			processes      TEXT NOT NULL DEFAULT '[]'
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
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("store: schema migration failed [%s]: %w", stmt[:min(40, len(stmt))], err)
		}
	}

	// Ensure processes column exists in case nodes table was created in an earlier migration
	_, _ = db.Exec(`ALTER TABLE nodes ADD COLUMN processes TEXT NOT NULL DEFAULT '[]'`)

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

	_, err = tx.ExecContext(ctx, `
		INSERT INTO nodes (node_id, hostname, os, platform, kernel_version, agent_version, status, last_seen, processes)
		VALUES (?, ?, ?, ?, ?, ?, 'online', ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			hostname       = excluded.hostname,
			os             = excluded.os,
			platform       = excluded.platform,
			kernel_version = excluded.kernel_version,
			agent_version  = excluded.agent_version,
			status         = 'online',
			last_seen      = excluded.last_seen,
			processes      = excluded.processes`,
		p.NodeID, p.Hostname, p.OS, p.Platform, p.KernelVersion, p.AgentVersion, lastSeenStr, string(procsJSON),
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

// GetNodes returns all known nodes with their latest metrics and active processes.
// A node is considered "offline" when its last_seen is older than offlineAfter.
func (s *Store) GetNodes(ctx context.Context, offlineAfter time.Duration) ([]NodeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			n.node_id, n.hostname, n.os, n.platform, n.kernel_version, n.agent_version,
			n.last_seen,
			COALESCE(n.processes, '[]'),
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
		if err := rows.Scan(
			&r.NodeID, &r.Hostname, &r.OS, &r.Platform, &r.KernelVersion, &r.AgentVersion,
			&lastSeenStr,
			&procsJSON,
			&r.CPUPercent, &r.MemPercent, &r.DiskPercent, &r.NetRxBytes, &r.NetTxBytes,
		); err != nil {
			return nil, fmt.Errorf("store: scan node row: %w", err)
		}
		// Parse SQLite datetime string using multi-format helper.
		r.LastSeen = parseSQLiteTime(lastSeenStr)
		if err := json.Unmarshal([]byte(procsJSON), &r.Processes); err != nil || r.Processes == nil {
			r.Processes = []model.ProcessInfo{}
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
	if err := row.Scan(
		&r.NodeID, &r.Hostname, &r.OS, &r.Platform, &r.KernelVersion, &r.AgentVersion,
		&lastSeenStr,
		&procsJSON,
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
