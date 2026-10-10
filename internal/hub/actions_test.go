package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHubActionEndpointsAndStore(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("failed to open memory store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Seed node in store
	_ = store.UpsertNode(ctx, IngestParams{
		NodeID:   "node-alpha",
		Hostname: "alpha.local",
		LastSeen: time.Now().UTC(),
	})

	// 1. Enqueue action directly via store
	record, err := store.EnqueueAction(ctx, "act-test-01", "node-alpha", "flush_dns", map[string]string{})
	if err != nil {
		t.Fatalf("failed to enqueue action: %v", err)
	}
	if record.Status != "queued" {
		t.Errorf("expected status 'queued', got %s", record.Status)
	}

	// 2. Fetch pending actions
	pending, err := store.GetPendingActions(ctx, "node-alpha")
	if err != nil {
		t.Fatalf("failed to get pending actions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending action, got %d", len(pending))
	}
	if pending[0].ID != "act-test-01" {
		t.Errorf("expected action ID 'act-test-01', got %s", pending[0].ID)
	}

	// 3. Second fetch should be empty (status transitioned to 'running')
	pending2, err := store.GetPendingActions(ctx, "node-alpha")
	if err != nil {
		t.Fatalf("failed second pending fetch: %v", err)
	}
	if len(pending2) != 0 {
		t.Errorf("expected 0 pending actions after first fetch, got %d", len(pending2))
	}

	// 4. Save action result
	err = store.SaveActionResult(ctx, "act-test-01", "node-alpha", "success", "DNS cache flushed", "", 0, 42)
	if err != nil {
		t.Fatalf("failed to save action result: %v", err)
	}

	// 5. Get action by ID
	saved, err := store.GetAction(ctx, "act-test-01")
	if err != nil {
		t.Fatalf("failed to get action: %v", err)
	}
	if saved == nil {
		t.Fatal("expected action record, got nil")
	}
	if saved.Status != "success" || saved.ExitCode != 0 || saved.DurationMs != 42 {
		t.Errorf("unexpected action details: %+v", saved)
	}

	// 6. Test HTTP API handlers
	api := NewAPI(store, "", 30*time.Second, nil, nil)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	// POST /api/v1/nodes/node-alpha/actions with whitelisted action
	body := bytes.NewBufferString(`{"action":"clean_temp","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/node-alpha/actions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on enqueue, got %d: %s", w.Code, w.Body.String())
	}

	var enqResp ActionRecord
	if err := json.NewDecoder(w.Body).Decode(&enqResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if enqResp.Action != "clean_temp" || enqResp.Status != "queued" {
		t.Errorf("unexpected enqueued record: %+v", enqResp)
	}

	// POST /api/v1/nodes/node-alpha/actions with forbidden non-whitelisted action
	badBody := bytes.NewBufferString(`{"action":"sudo_reboot","params":{}}`)
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/node-alpha/actions", badBody)
	badReq.Header.Set("Content-Type", "application/json")
	badW := httptest.NewRecorder()
	mux.ServeHTTP(badW, badReq)

	if badW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for unauthorized action, got %d: %s", badW.Code, badW.Body.String())
	}
}
