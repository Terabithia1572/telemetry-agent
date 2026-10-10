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
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/telemetry-agent/internal/actions"
	"github.com/telemetry-agent/internal/hub/alerting"
	"github.com/telemetry-agent/internal/model"
)

// ── API handler struct ────────────────────────────────────────────────────────

// API bundles the HTTP handlers with their shared dependencies.
type API struct {
	store        *Store
	authToken    string // empty → no authentication enforced
	offlineAfter time.Duration
	engine       *alerting.Engine
	log          *slog.Logger
	broker       *SSEBroker
}

// NewAPI constructs an [API] ready to be registered on a [*http.ServeMux].
func NewAPI(store *Store, authToken string, offlineAfter time.Duration, engine *alerting.Engine, log *slog.Logger) *API {
	return &API{
		store:        store,
		authToken:    authToken,
		offlineAfter: offlineAfter,
		engine:       engine,
		log:          log,
		broker:       NewSSEBroker(),
	}
}

// RegisterRoutes attaches all API routes to mux.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/metrics", a.handleIngest)
	mux.HandleFunc("GET /api/v1/nodes", a.handleListNodes)
	mux.HandleFunc("GET /api/v1/nodes/{id}/history", a.handleGetNodeHistory)
	mux.HandleFunc("GET /api/v1/nodes/{id}", a.handleGetNode)
	mux.HandleFunc("GET /api/v1/events", a.handleSSE)

	// Remote Actions API
	mux.HandleFunc("POST /api/v1/nodes/{id}/actions", a.handleEnqueueAction)
	mux.HandleFunc("GET /api/v1/nodes/{id}/actions/pending", a.handleGetPendingActions)
	mux.HandleFunc("POST /api/v1/nodes/{id}/actions/{action_id}/result", a.handleSaveActionResult)
	mux.HandleFunc("GET /api/v1/nodes/{id}/actions/{action_id}", a.handleGetAction)
	mux.HandleFunc("GET /api/v1/nodes/{id}/actions", a.handleListActions)

	// Alert settings & testing
	mux.HandleFunc("GET /api/v1/alerts/config", a.handleGetAlertConfig)
	mux.HandleFunc("POST /api/v1/alerts/config", a.handleSaveAlertConfig)
	mux.HandleFunc("POST /api/v1/alerts/test", a.handleTestAlert)
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
		"procs", len(p.Processes),
		"containers", len(p.Containers),
	)

	// Notify SSE subscribers so the dashboard updates immediately.
	go a.broker.Broadcast(r.Context())

	// Evaluate threshold alerting
	if a.engine != nil {
		a.engine.EvaluateIngest(r.Context(), alerting.IngestMetrics{
			NodeID:      p.NodeID,
			Hostname:    p.Hostname,
			CPUPercent:  p.CPUPercent,
			MemPercent:  p.MemPercent,
			DiskPercent: p.DiskPercent,
		})
	}

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
		Processes:     r.Processes,
		Containers:    r.Containers,
	}
	if p.Processes == nil {
		p.Processes = []model.ProcessInfo{}
	}
	if p.Containers == nil {
		p.Containers = []model.ContainerInfo{}
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

// ── Node routes (/api/v1/nodes/{id} and /api/v1/nodes/{id}/history) ───────────

func (a *API) handleGetNodeHistory(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
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

func (a *API) handleGetNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		a.jsonError(w, http.StatusBadRequest, "invalid node id in path")
		return
	}

	node, err := a.store.GetNode(r.Context(), nodeID, a.offlineAfter)
	if err != nil {
		a.log.Error("get node: store error", "node_id", nodeID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if node == nil {
		a.jsonError(w, http.StatusNotFound, "node not found")
		return
	}
	a.jsonOK(w, node)
}

// ── Remote Actions Handlers ───────────────────────────────────────────────────

type enqueueActionRequest struct {
	Action string            `json:"action"`
	Params map[string]string `json:"params"`
}

func (a *API) handleEnqueueAction(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		a.jsonError(w, http.StatusBadRequest, "node id is required")
		return
	}

	var req enqueueActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if !actions.IsAllowed(req.Action) {
		a.jsonError(w, http.StatusBadRequest, fmt.Sprintf("unauthorized action %q; only whitelisted actions are allowed", req.Action))
		return
	}

	actionID := fmt.Sprintf("act-%d-%06x", time.Now().UnixMilli(), rand.Intn(0xffffff))
	record, err := a.store.EnqueueAction(r.Context(), actionID, nodeID, req.Action, req.Params)
	if err != nil {
		a.log.Error("enqueue action: store error", "node_id", nodeID, "action", req.Action, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "failed to enqueue action")
		return
	}

	w.WriteHeader(http.StatusCreated)
	a.jsonOK(w, record)
}

func (a *API) handleGetPendingActions(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		a.jsonError(w, http.StatusBadRequest, "node id is required")
		return
	}

	pending, err := a.store.GetPendingActions(r.Context(), nodeID)
	if err != nil {
		a.log.Error("get pending actions: store error", "node_id", nodeID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}
	a.jsonOK(w, pending)
}

type saveActionResultRequest struct {
	Status     string `json:"status"` // "success" | "failed"
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
}

func (a *API) handleSaveActionResult(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	actionID := r.PathValue("action_id")
	if actionID == "" {
		a.jsonError(w, http.StatusBadRequest, "action id is required")
		return
	}

	var res saveActionResultRequest
	if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid result body: "+err.Error())
		return
	}

	if res.Status == "" {
		if res.ExitCode == 0 {
			res.Status = "success"
		} else {
			res.Status = "failed"
		}
	}

	if err := a.store.SaveActionResult(r.Context(), actionID, nodeID, res.Status, res.Stdout, res.Stderr, res.ExitCode, res.DurationMs); err != nil {
		a.log.Error("save action result: store error", "action_id", actionID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "failed to save result")
		return
	}

	a.jsonOK(w, map[string]string{"status": "recorded", "action_id": actionID})
}

func (a *API) handleGetAction(w http.ResponseWriter, r *http.Request) {
	actionID := r.PathValue("action_id")
	if actionID == "" {
		a.jsonError(w, http.StatusBadRequest, "action id is required")
		return
	}

	record, err := a.store.GetAction(r.Context(), actionID)
	if err != nil {
		a.log.Error("get action: store error", "action_id", actionID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if record == nil {
		a.jsonError(w, http.StatusNotFound, "action not found")
		return
	}
	a.jsonOK(w, record)
}

func (a *API) handleListActions(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		a.jsonError(w, http.StatusBadRequest, "node id is required")
		return
	}

	records, err := a.store.GetNodeActions(r.Context(), nodeID, 20)
	if err != nil {
		a.log.Error("list node actions: store error", "node_id", nodeID, "error", err)
		a.jsonError(w, http.StatusInternalServerError, "storage error")
		return
	}
	a.jsonOK(w, records)
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

// ── Alert settings handlers ───────────────────────────────────────────────────

func (a *API) handleGetAlertConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.store.GetAlertSettings(r.Context())
	if err != nil {
		a.log.Error("get alert config error", "error", err)
		a.jsonError(w, http.StatusInternalServerError, "failed to load alert settings")
		return
	}
	a.jsonOK(w, cfg)
}

func (a *API) handleSaveAlertConfig(w http.ResponseWriter, r *http.Request) {
	if !a.checkAuth(w, r) {
		return
	}

	var cfg alerting.Settings
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.store.SaveAlertSettings(r.Context(), cfg); err != nil {
		a.log.Error("save alert config error", "error", err)
		a.jsonError(w, http.StatusInternalServerError, "failed to save alert settings")
		return
	}

	if a.engine != nil {
		a.engine.UpdateSettings(cfg)
	}

	a.jsonOK(w, map[string]any{"status": "saved", "config": cfg})
}

func (a *API) handleTestAlert(w http.ResponseWriter, r *http.Request) {
	if !a.checkAuth(w, r) {
		return
	}

	var req struct {
		WebhookURL string `json:"webhook_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if a.engine == nil {
		a.jsonError(w, http.StatusInternalServerError, "alert engine not available")
		return
	}

	if err := a.engine.TestWebhook(r.Context(), req.WebhookURL); err != nil {
		a.log.Warn("test alert webhook failed", "error", err)
		a.jsonError(w, http.StatusBadRequest, fmt.Sprintf("webhook test failed: %v", err))
		return
	}

	a.jsonOK(w, map[string]string{"status": "ok", "message": "Test notification delivered successfully"})
}
