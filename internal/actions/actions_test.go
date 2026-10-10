package actions

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockActionClient struct {
	mu        sync.Mutex
	pending   []ActionRequest
	submitted []ActionResult
}

func (m *mockActionClient) FetchPendingActions(_ context.Context, _ string) ([]ActionRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.pending
	m.pending = nil // drain pending
	return res, nil
}

func (m *mockActionClient) SubmitActionResult(_ context.Context, res ActionResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submitted = append(m.submitted, res)
	return nil
}

func TestWhitelistSecurityEnforcement(t *testing.T) {
	runner := NewRunner(5*time.Second, slog.Default())
	ctx := context.Background()

	// 1. Attempt unauthorized arbitrary action
	badReq := ActionRequest{
		ID:     "act-test-1",
		NodeID: "node-1",
		Action: "cat /etc/shadow",
	}

	res := runner.Execute(ctx, badReq)
	if res.Status != "failed" || res.ExitCode != 1 {
		t.Fatalf("expected security violation status failed, got status %s, code %d", res.Status, res.ExitCode)
	}
	if res.Stderr == "" {
		t.Fatal("expected security violation error message in Stderr")
	}

	// 2. Attempt command injection in ping target
	injectReq := ActionRequest{
		ID:     "act-test-2",
		NodeID: "node-1",
		Action: ActionPingGateway,
		Params: map[string]string{"target": "127.0.0.1; rm -rf /"},
	}

	res = runner.Execute(ctx, injectReq)
	if res.Status != "failed" || res.ExitCode != 1 {
		t.Fatalf("expected security violation on illegal ping target, got %s", res.Status)
	}

	// 3. Attempt command injection in container target
	containerInject := ActionRequest{
		ID:     "act-test-3",
		NodeID: "node-1",
		Action: ActionRestartDocker,
		Params: map[string]string{"container_id": "redis && reboot"},
	}

	res = runner.Execute(ctx, containerInject)
	if res.Status != "failed" || res.ExitCode != 1 {
		t.Fatalf("expected security violation on illegal container name, got %s", res.Status)
	}
}

func TestCleanTempAction(t *testing.T) {
	runner := NewRunner(5*time.Second, slog.Default())
	ctx := context.Background()

	// Create an old file in temp dir to clean
	tempDir := os.TempDir()
	testFile := filepath.Join(tempDir, fmt.Sprintf("telemetry_test_%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(testFile, []byte("junk data"), 0600); err != nil {
		t.Fatalf("failed to create test temp file: %v", err)
	}
	// Backdate modification time by 10 minutes so cleanTemp cleans it
	oldTime := time.Now().Add(-10 * time.Minute)
	_ = os.Chtimes(testFile, oldTime, oldTime)

	req := ActionRequest{
		ID:     "act-clean-1",
		NodeID: "node-1",
		Action: ActionCleanTemp,
	}

	res := runner.Execute(ctx, req)
	if res.Status != "success" || res.ExitCode != 0 {
		t.Fatalf("expected clean_temp success, got status %s, stderr %s", res.Status, res.Stderr)
	}

	// Verify file was removed
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Errorf("expected test file to be cleaned up, but it still exists")
		_ = os.Remove(testFile)
	}
}

func TestWorkerExecutionCycle(t *testing.T) {
	runner := NewRunner(5*time.Second, slog.Default())
	mockClient := &mockActionClient{
		pending: []ActionRequest{
			{
				ID:     "act-worker-1",
				NodeID: "node-test",
				Action: ActionCleanTemp,
			},
		},
	}

	worker := NewWorker(mockClient, runner, slog.Default(), "node-test", 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	// Wait for worker to poll and execute
	time.Sleep(150 * time.Millisecond)
	cancel()

	mockClient.mu.Lock()
	defer mockClient.mu.Unlock()

	if len(mockClient.submitted) != 1 {
		t.Fatalf("expected 1 submitted action result, got %d", len(mockClient.submitted))
	}
	sub := mockClient.submitted[0]
	if sub.ActionID != "act-worker-1" || sub.Status != "success" {
		t.Errorf("unexpected submitted action result: %+v", sub)
	}
}
