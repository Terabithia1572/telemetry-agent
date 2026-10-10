// Package collector – docker.go collects metrics and metadata for Docker containers.
// It uses direct HTTP-over-socket/pipe communication to the Docker daemon.
// If the Docker engine is missing or stopped, it gracefully skips without errors or panics.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/telemetry-agent/internal/model"
)

// DockerCollector inspects running Docker containers.
type DockerCollector struct {
	client *http.Client
	log    *slog.Logger
}

// NewDockerCollector constructs a DockerCollector with custom socket/pipe dialer.
func NewDockerCollector(log *slog.Logger) *DockerCollector {
	if log == nil {
		log = slog.Default()
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialDocker(ctx)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 3 * time.Second,
	}

	return &DockerCollector{
		client: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
		log: log,
	}
}

// Name implements Collector.
func (d *DockerCollector) Name() string { return "docker" }

// dockerContainerJSON mirrors the fields returned by GET /containers/json.
type dockerContainerJSON struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Created int64    `json:"Created"`
}

// dockerStatsJSON mirrors the fields returned by GET /containers/{id}/stats?stream=false.
type dockerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint64 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
}

// Collect implements Collector.
func (d *DockerCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/containers/json?all=1", nil)
	if err != nil {
		return nil
	}

	resp, err := d.client.Do(req)
	if err != nil {
		d.log.Debug("docker engine not available, skipping", "error", err)
		report.Containers = []model.ContainerInfo{}
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		d.log.Debug("docker returned non-200 status", "status", resp.StatusCode)
		report.Containers = []model.ContainerInfo{}
		return nil
	}

	var rawContainers []dockerContainerJSON
	if err := json.NewDecoder(resp.Body).Decode(&rawContainers); err != nil {
		d.log.Debug("failed to decode docker containers JSON", "error", err)
		report.Containers = []model.ContainerInfo{}
		return nil
	}

	containers := make([]model.ContainerInfo, 0, len(rawContainers))
	for _, rc := range rawContainers {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := rc.ID[:min(12, len(rc.ID))]
		if len(rc.Names) > 0 {
			name = strings.TrimPrefix(rc.Names[0], "/")
		}

		ci := model.ContainerInfo{
			ID:      rc.ID[:min(12, len(rc.ID))],
			Name:    name,
			Image:   rc.Image,
			Status:  rc.State,
			Created: rc.Created,
		}

		// Only query stats if container is running
		if strings.ToLower(rc.State) == "running" {
			cpu, memUsage, memLimit := d.fetchStats(ctx, rc.ID)
			ci.CPUPercent = cpu
			ci.MemoryUsage = memUsage
			ci.MemoryLimit = memLimit
		}

		containers = append(containers, ci)
	}

	report.Containers = containers
	return nil
}

// fetchStats retrieves instantaneous resource metrics for a running container.
func (d *DockerCollector) fetchStats(ctx context.Context, containerID string) (float64, uint64, uint64) {
	statCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	url := fmt.Sprintf("http://localhost/containers/%s/stats?stream=false", containerID)
	req, err := http.NewRequestWithContext(statCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, 0
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, 0, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, 0
	}

	var stats dockerStatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return 0, 0, 0
	}

	// Calculate CPU %
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage) - float64(stats.PreCPUStats.SystemCPUUsage)
	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}

	var cpuPercent float64
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}

	return roundFloat(cpuPercent, 2), stats.MemoryStats.Usage, stats.MemoryStats.Limit
}
