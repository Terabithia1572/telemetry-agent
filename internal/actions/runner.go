package actions

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

var (
	// validTargetRegex ensures ping target contains only safe hostname/IP characters.
	validTargetRegex = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)
	// validContainerRegex ensures container identifier contains only standard docker name/ID chars.
	validContainerRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

// Runner manages safe, whitelisted action execution with strict timeouts.
type Runner struct {
	timeout time.Duration
	log     *slog.Logger
}

// NewRunner returns an initialized Runner with a default 10-second timeout.
func NewRunner(timeout time.Duration, log *slog.Logger) *Runner {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		timeout: timeout,
		log:     log,
	}
}

// Execute performs the requested action under strict whitelist controls.
func (r *Runner) Execute(ctx context.Context, req ActionRequest) ActionResult {
	start := time.Now()

	// Whitelist check
	if !IsAllowed(req.Action) {
		r.log.Warn("rejected unauthorized action", "action", req.Action, "node_id", req.NodeID)
		return ActionResult{
			ActionID:   req.ID,
			NodeID:     req.NodeID,
			Action:     req.Action,
			Status:     "failed",
			Stderr:     fmt.Sprintf("security violation: action %q is not in the whitelist", req.Action),
			ExitCode:   1,
			DurationMs: time.Since(start).Milliseconds(),
			ExecutedAt: time.Now().UTC(),
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var res ActionResult
	switch req.Action {
	case ActionFlushDNS:
		res = r.flushDNS(execCtx, req)
	case ActionCleanTemp:
		res = r.cleanTemp(execCtx, req)
	case ActionPingGateway:
		res = r.pingGateway(execCtx, req)
	case ActionRestartDocker:
		res = r.restartDocker(execCtx, req)
	default:
		res = ActionResult{
			ActionID: req.ID,
			NodeID:   req.NodeID,
			Action:   req.Action,
			Status:   "failed",
			Stderr:   fmt.Sprintf("unknown action: %s", req.Action),
			ExitCode: 1,
		}
	}

	res.DurationMs = time.Since(start).Milliseconds()
	res.ExecutedAt = time.Now().UTC()
	return res
}

// flushDNS clears the local operating system DNS resolver cache.
func (r *Runner) flushDNS(ctx context.Context, req ActionRequest) ActionResult {
	res := ActionResult{
		ActionID: req.ID,
		NodeID:   req.NodeID,
		Action:   req.Action,
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "ipconfig", "/flushdns")
	case "linux":
		if path, err := exec.LookPath("resolvectl"); err == nil {
			cmd = exec.CommandContext(ctx, path, "flush-caches")
		} else if path, err := exec.LookPath("systemd-resolve"); err == nil {
			cmd = exec.CommandContext(ctx, path, "--flush-caches")
		} else if path, err := exec.LookPath("nscd"); err == nil {
			cmd = exec.CommandContext(ctx, path, "-i", "hosts")
		} else {
			res.Status = "failed"
			res.Stderr = "no supported DNS resolver found (systemd-resolved / resolvectl / nscd)"
			res.ExitCode = 1
			return res
		}
	case "darwin":
		cmd = exec.CommandContext(ctx, "dscacheutil", "-flushcache")
	default:
		res.Status = "failed"
		res.Stderr = fmt.Sprintf("unsupported platform for flush_dns: %s", runtime.GOOS)
		res.ExitCode = 1
		return res
	}

	return r.runCmd(cmd, res)
}

// cleanTemp purges temporary junk files in the OS temp directory.
func (r *Runner) cleanTemp(ctx context.Context, req ActionRequest) ActionResult {
	res := ActionResult{
		ActionID: req.ID,
		NodeID:   req.NodeID,
		Action:   req.Action,
	}

	tempDir := os.TempDir()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		res.Status = "failed"
		res.Stderr = fmt.Sprintf("failed to read temp directory %q: %v", tempDir, err)
		res.ExitCode = 1
		return res
	}

	var deletedCount int
	var freedBytes int64
	var skippedCount int

	cutoff := time.Now().Add(-5 * time.Minute)

	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}

		path := filepath.Join(tempDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			skippedCount++
			continue
		}

		// Skip directories and files modified within the last 5 minutes (may still be in use)
		if entry.IsDir() || info.ModTime().After(cutoff) {
			skippedCount++
			continue
		}

		size := info.Size()
		if err := os.Remove(path); err == nil {
			deletedCount++
			freedBytes += size
		} else {
			skippedCount++
		}
	}

	res.Status = "success"
	res.ExitCode = 0
	res.Stdout = fmt.Sprintf(
		"Purged temporary junk files in %s\n- Removed files: %d\n- Space reclaimed: %s\n- Active/locked files preserved: %d",
		tempDir, deletedCount, formatBytes(freedBytes), skippedCount,
	)
	return res
}

// pingGateway executes a 4-packet diagnostic ping to 1.1.1.1 or the specified target.
func (r *Runner) pingGateway(ctx context.Context, req ActionRequest) ActionResult {
	res := ActionResult{
		ActionID: req.ID,
		NodeID:   req.NodeID,
		Action:   req.Action,
	}

	target := "1.1.1.1"
	if custom := req.Params["target"]; custom != "" {
		if !validTargetRegex.MatchString(custom) || len(custom) > 128 {
			res.Status = "failed"
			res.Stderr = fmt.Sprintf("security violation: invalid ping target %q (must be alphanumeric hostname or IP)", custom)
			res.ExitCode = 1
			return res
		}
		target = custom
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "ping", "-n", "4", target)
	default:
		cmd = exec.CommandContext(ctx, "ping", "-c", "4", "-W", "2", target)
	}

	return r.runCmd(cmd, res)
}

// restartDocker restarts a specific Docker container if Docker is available.
func (r *Runner) restartDocker(ctx context.Context, req ActionRequest) ActionResult {
	res := ActionResult{
		ActionID: req.ID,
		NodeID:   req.NodeID,
		Action:   req.Action,
	}

	target := req.Params["container_id"]
	if target == "" {
		target = req.Params["container_name"]
	}
	if target == "" {
		target = req.Params["container"]
	}

	if target == "" {
		res.Status = "failed"
		res.Stderr = "missing required parameter: 'container_id' or 'container_name'"
		res.ExitCode = 1
		return res
	}

	if !validContainerRegex.MatchString(target) || len(target) > 128 {
		res.Status = "failed"
		res.Stderr = fmt.Sprintf("security violation: invalid container identifier %q", target)
		res.ExitCode = 1
		return res
	}

	cmd := exec.CommandContext(ctx, "docker", "restart", target)
	return r.runCmd(cmd, res)
}

// runCmd executes the external command, capturing stdout, stderr, and exit code.
func (r *Runner) runCmd(cmd *exec.Cmd, res ActionResult) ActionResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
		}
		res.Status = "failed"
		if res.Stderr == "" {
			res.Stderr = err.Error()
		}
	} else {
		res.ExitCode = 0
		res.Status = "success"
	}

	return res
}

func formatBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(b)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
