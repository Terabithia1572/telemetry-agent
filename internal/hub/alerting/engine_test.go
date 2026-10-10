package alerting

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type mockStore struct {
	mu       sync.Mutex
	settings Settings
	nodes    []NodeStatus
}

func (m *mockStore) GetAlertSettings(_ context.Context) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings, nil
}

func (m *mockStore) SaveAlertSettings(_ context.Context, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = s
	return nil
}

func (m *mockStore) GetNodeStatuses(_ context.Context, _ time.Duration) ([]NodeStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodes, nil
}

func TestDispatcherSendAndTestWebhook(t *testing.T) {
	var receivedPayload multiWebhookPayload
	var receivedCount int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		receivedCount++
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewDispatcher()

	// Test Send with firing alert
	event := AlertEvent{
		NodeID:       "node-123",
		Hostname:     "prod-worker-1",
		Metric:       "CPU",
		CurrentValue: "95.5%",
		Threshold:    "90.0%",
		IsResolved:   false,
		Timestamp:    time.Now().UTC(),
	}

	err := d.Send(context.Background(), srv.URL, event)
	if err != nil {
		t.Fatalf("expected Send to succeed, got %v", err)
	}

	mu.Lock()
	if receivedCount != 1 {
		t.Fatalf("expected 1 request, got %d", receivedCount)
	}
	if len(receivedPayload.Embeds) == 0 {
		t.Fatal("expected Discord embeds in payload")
	}
	if receivedPayload.Embeds[0].Color != 15680324 { // #EF4444
		t.Errorf("expected red color for firing alert, got %d", receivedPayload.Embeds[0].Color)
	}
	if len(receivedPayload.Attachments) == 0 {
		t.Fatal("expected Slack attachments in payload")
	}
	mu.Unlock()

	// Test TestWebhook
	err = d.TestWebhook(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("expected TestWebhook to succeed, got %v", err)
	}

	// Test error when empty URL
	err = d.TestWebhook(context.Background(), "")
	if err == nil {
		t.Fatal("expected error with empty webhook URL, got nil")
	}

	// Test error when HTTP error response
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer errSrv.Close()

	err = d.Send(context.Background(), errSrv.URL, event)
	if err == nil {
		t.Fatal("expected error on 404 response, got nil")
	}
}

func TestEngineAlertLifecycle(t *testing.T) {
	var dispatchedEvents []AlertEvent
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p multiWebhookPayload
		_ = json.Unmarshal(body, &p)
		mu.Lock()
		if len(p.Embeds) > 0 {
			var ev AlertEvent
			for _, f := range p.Embeds[0].Fields {
				switch f.Name {
				case "Node":
					ev.Hostname = f.Value
				case "Metric":
					ev.Metric = f.Value
				case "Current Value":
					ev.CurrentValue = f.Value
				case "Threshold":
					ev.Threshold = f.Value
				case "Node ID":
					ev.NodeID = f.Value
				case "Status":
					ev.IsResolved = (f.Value == "RESOLVED")
				}
			}
			dispatchedEvents = append(dispatchedEvents, ev)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := &mockStore{
		settings: Settings{
			WebhookURL:      srv.URL,
			CPUThreshold:    85.0,
			MemoryThreshold: 85.0,
			DiskThreshold:   90.0,
			Enabled:         true,
		},
	}

	engine := NewEngine(store, slog.Default())
	engine.UpdateSettings(store.settings)

	ctx := context.Background()

	// 1. Report normal values: no alerts should fire
	engine.EvaluateIngest(ctx, IngestMetrics{
		NodeID:      "node-1",
		Hostname:    "web-01",
		CPUPercent:  40.0,
		MemPercent:  50.0,
		DiskPercent: 60.0,
	})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(dispatchedEvents) != 0 {
		t.Fatalf("expected 0 events for normal metrics, got %d", len(dispatchedEvents))
	}
	mu.Unlock()

	// 2. CPU breaches threshold (92% > 85%): should trigger 1 firing alert
	engine.EvaluateIngest(ctx, IngestMetrics{
		NodeID:      "node-1",
		Hostname:    "web-01",
		CPUPercent:  92.0,
		MemPercent:  50.0,
		DiskPercent: 60.0,
	})

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if len(dispatchedEvents) != 1 {
		t.Fatalf("expected 1 event for CPU breach, got %d", len(dispatchedEvents))
	}
	if dispatchedEvents[0].Metric != "CPU" || dispatchedEvents[0].IsResolved {
		t.Errorf("unexpected event: %+v", dispatchedEvents[0])
	}
	mu.Unlock()

	// 3. Consecutive breach within cooldown window: should NOT trigger duplicate notification
	engine.EvaluateIngest(ctx, IngestMetrics{
		NodeID:      "node-1",
		Hostname:    "web-01",
		CPUPercent:  95.0,
		MemPercent:  50.0,
		DiskPercent: 60.0,
	})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(dispatchedEvents) != 1 {
		t.Fatalf("expected cooldown to silence duplicate alert, got %d events", len(dispatchedEvents))
	}
	mu.Unlock()

	// 4. Metric returns below threshold (50% <= 85%): should trigger RESOLVED notification
	engine.EvaluateIngest(ctx, IngestMetrics{
		NodeID:      "node-1",
		Hostname:    "web-01",
		CPUPercent:  50.0,
		MemPercent:  50.0,
		DiskPercent: 60.0,
	})

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if len(dispatchedEvents) != 2 {
		t.Fatalf("expected 2 total events after resolution, got %d", len(dispatchedEvents))
	}
	if !dispatchedEvents[1].IsResolved {
		t.Errorf("expected resolved event, got %+v", dispatchedEvents[1])
	}
	mu.Unlock()
}
