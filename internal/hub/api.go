// Package hub – api.go implements all HTTP handler functions for the telemetry
// hub REST API and the Server-Sent Events (SSE) stream.
//
// Endpoints:
//
//	POST /api/v1/metrics            – ingest a TelemetryReport from an agent
//	GET  /api/v1/nodes              – list all nodes with current stats
//	GET  /api/v1/nodes/{id}/history – historical metric points for one node
//	GET  /api/v1/events             – SSE stream; pushes node list on each change
package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/telemetry-agent/internal/model"
)

// ── API handler struct ────────────────────────────────────────────────────────

// API bundles the HTTP handlers with their shared dependencies.
type API struct {
	store       *Store
	authToken   string // empty → no authentication enforced
	offlineAfter time.Duration
	log         *slog.Logger
	broker      *SSEBroker
}

// NewAPI constructs an [API] ready to be registered on a [*http.ServeMux].
func NewAPI(store *Store, authToken string, offlineAfter time.Duration, log *slog.Logger) *API {
	return &API{
		store:        store,
		authToken:    authToken,
		offlineAfter: offlineAfter,
		log:          log,
		broker:       NewSSEBroker(),
	}
}

// RegisterRoutes attaches all API routes to mux.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/metrics", a.handleIngest)
	mux.HandleFunc("GET /api/v1/nodes", a.handleListNodes)
	mux.HandleFunc("GET /api/v1/nodes/", a.handleNodeHistory) // matches /api/v1/nodes/{id}/history
	mux.HandleFunc("GET /api/v1/events", a.handleSSE)
}

// ── Ingest ────────────────────────────────────────────────────────────────────

func (a *API) handleIngest(w http.ResponseWriter, r *http.Request) {
	if !a.checkAuth(w, r) {
		return
	}

	var report model.TelemetryReport
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)) // 1 MB cap
	if err := dec.Decode(&report); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid JSON payload: "+err.Error())
		return
	}

	if report.NodeID == "" {
		a.jsonError(w, http.StatusBadRequest, "node_id must not be empty")
		return
	}

	p := extractIngestParams(&report)

	if err := a.store.UpsertNode(r.Context(), p); err != nil {
		a.log.Error("ingest: store error", "node_id", report.NodeID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}

	a.log.Info("ingest: received report",
		"node_id", report.NodeID,
		"cpu", fmt.Sprintf("%.1f%%", p.CPUPercent),
		"mem", fmt.Sprintf("%.1f%%", p.MemPercent),
	)

	// Notify SSE subscribers so the dashboard updates immediately.
	go a.broker.Broadcast(r.Context())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintln(w, `{"status":"accepted"}`)
}

// extractIngestParams maps a TelemetryReport to IngestParams, deriving
// aggregate values from the sub-structures.
func extractIngestParams(r *model.TelemetryReport) IngestParams {
	p := IngestParams{
		NodeID:        r.NodeID,
		Hostname:      r.Host.Hostname,
		OS:            r.Host.OS,
		Platform:      r.Host.Platform,
		KernelVersion: r.Host.KernelVersion,
		AgentVersion:  r.AgentVersion,
		LastSeen:      r.CollectedAt,
		CPUPercent:    r.CPU.UsagePercent,
		MemPercent:    r.Memory.Physical.UsagePercent,
	}

	// Aggregate disk usage: weighted by total bytes across all mounts.
	var totalBytes, usedBytes uint64
	for _, d := range r.Disks {
		totalBytes += d.TotalBytes
		usedBytes += d.UsedBytes
	}
	if totalBytes > 0 {
		p.DiskPercent = float64(usedBytes) / float64(totalBytes) * 100
	}

	// Sum network bytes across all interfaces.
	for _, n := range r.Network {
		p.NetRxBytes += n.BytesRecv
		p.NetTxBytes += n.BytesSent
	}

	if p.LastSeen.IsZero() {
		p.LastSeen = time.Now().UTC()
	}

	return p
}

// ── List nodes ────────────────────────────────────────────────────────────────

func (a *API) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.store.GetNodes(r.Context(), a.offlineAfter)
	if err != nil {
		a.log.Error("list nodes: store error", "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if nodes == nil {
		nodes = []NodeRow{} // return [] not null
	}
	a.jsonOK(w, nodes)
}

// ── Node history ──────────────────────────────────────────────────────────────

// handleNodeHistory matches /api/v1/nodes/{id}/history.
func (a *API) handleNodeHistory(w http.ResponseWriter, r *http.Request) {
	// Extract node ID from path: /api/v1/nodes/<id>/history
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	path = strings.TrimSuffix(path, "/history")
	nodeID := path

	if nodeID == "" || strings.Contains(nodeID, "/") {
		a.jsonError(w, http.StatusBadRequest, "invalid node id in path")
		return
	}

	windowStr := r.URL.Query().Get("window")
	window := time.Hour // default 1 h
	if windowStr != "" {
		if d, err := time.ParseDuration(windowStr); err == nil && d > 0 {
			window = d
		}
	}

	pts, err := a.store.GetHistory(r.Context(), nodeID, window, 240)
	if err != nil {
		a.log.Error("get history: store error", "node_id", nodeID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if pts == nil {
		pts = []MetricPoint{}
	}
	a.jsonOK(w, pts)
}

// ── SSE ───────────────────────────────────────────────────────────────────────

// handleSSE upgrades the connection to a Server-Sent Events stream.
// The client receives a "nodes" event whenever a new ingest arrives, and also
// on an initial connection so the dashboard doesn't wait for the first agent tick.
func (a *API) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	ch := a.broker.Subscribe()
	defer a.broker.Unsubscribe(ch)

	// Send initial snapshot.
	a.sendNodeEvent(w, flusher, r.Context())

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			a.sendNodeEvent(w, flusher, r.Context())
		}
	}
}

func (a *API) sendNodeEvent(w http.ResponseWriter, f http.Flusher, ctx context.Context) {
	nodes, err := a.store.GetNodes(ctx, a.offlineAfter)
	if err != nil {
		return
	}
	if nodes == nil {
		nodes = []NodeRow{}
	}
	data, err := json.Marshal(nodes)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: nodes\ndata: %s\n\n", data)
	f.Flush()
}

// ── SSE broker ────────────────────────────────────────────────────────────────

// SSEBroker manages a set of subscriber channels and fans out broadcast signals.
type SSEBroker struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// NewSSEBroker creates an empty broker.
func NewSSEBroker() *SSEBroker {
	return &SSEBroker{subs: make(map[chan struct{}]struct{})}
}

// Subscribe returns a new channel that receives a value each time Broadcast is
// called.
func (b *SSEBroker) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a subscriber channel.
func (b *SSEBroker) Unsubscribe(ch chan struct{}) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	close(ch)
}

// Broadcast sends a non-blocking notification to all current subscribers.
func (b *SSEBroker) Broadcast(_ context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default: // drop if subscriber is slow
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (a *API) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if a.authToken == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != a.authToken {
		a.jsonError(w, http.StatusUnauthorized, "invalid or missing bearer token")
		return false
	}
	return true
}

func (a *API) jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.log.Warn("json encode error", "error", err)
	}
}

func (a *API) jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
