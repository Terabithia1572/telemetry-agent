// Package collector – process.go gathers running processes and extracts the top
// resource consumers (CPU and Memory) while gracefully handling transient processes
// and access limitations.
package collector

import (
	"context"
	"sort"

	gopsproc "github.com/shirou/gopsutil/v3/process"
	"github.com/telemetry-agent/internal/model"
)

// ProcessCollector gathers information about the top resource-consuming processes.
type ProcessCollector struct {
	maxTop int
}

// NewProcessCollector returns a ProcessCollector that returns up to maxTop processes.
func NewProcessCollector(maxTop int) *ProcessCollector {
	if maxTop <= 0 {
		maxTop = 10
	}
	return &ProcessCollector{maxTop: maxTop}
}

// Name implements Collector.
func (p *ProcessCollector) Name() string { return "process" }

// Collect implements Collector.
func (p *ProcessCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	procs, err := gopsproc.ProcessesWithContext(ctx)
	if err != nil {
		return err
	}

	var candidates []model.ProcessInfo
	for _, pr := range procs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Skip system idle / inaccessible PIDs
		if pr.Pid <= 0 {
			continue
		}

		name, err := pr.NameWithContext(ctx)
		if err != nil || name == "" {
			continue
		}

		memInfo, err := pr.MemoryInfoWithContext(ctx)
		if err != nil || memInfo == nil {
			continue
		}

		cpuPct, err := pr.CPUPercentWithContext(ctx)
		if err != nil {
			cpuPct = 0
		}

		memPct, err := pr.MemoryPercentWithContext(ctx)
		if err != nil {
			memPct = 0
		}

		candidates = append(candidates, model.ProcessInfo{
			PID:           pr.Pid,
			Name:          name,
			CPUPercent:    roundFloat(cpuPct, 2),
			MemoryRSS:     memInfo.RSS,
			MemoryPercent: float32(roundFloat(float64(memPct), 2)),
		})
	}

	if len(candidates) == 0 {
		report.Processes = []model.ProcessInfo{}
		return nil
	}

	// 1. Top 5 by CPU
	byCPU := make([]model.ProcessInfo, len(candidates))
	copy(byCPU, candidates)
	sort.Slice(byCPU, func(i, j int) bool {
		return byCPU[i].CPUPercent > byCPU[j].CPUPercent
	})
	topCPU := byCPU
	if len(topCPU) > 5 {
		topCPU = topCPU[:5]
	}

	// 2. Top 5 by Memory (RSS)
	byMem := make([]model.ProcessInfo, len(candidates))
	copy(byMem, candidates)
	sort.Slice(byMem, func(i, j int) bool {
		return byMem[i].MemoryRSS > byMem[j].MemoryRSS
	})
	topMem := byMem
	if len(topMem) > 5 {
		topMem = topMem[:5]
	}

	// 3. Deduplicate into unified slice
	seen := make(map[int32]bool)
	var unified []model.ProcessInfo

	for _, pi := range topCPU {
		if !seen[pi.PID] {
			seen[pi.PID] = true
			unified = append(unified, pi)
		}
	}
	for _, pi := range topMem {
		if !seen[pi.PID] {
			seen[pi.PID] = true
			unified = append(unified, pi)
		}
	}

	// 4. Sort unified list primarily by CPU, secondarily by Memory
	sort.Slice(unified, func(i, j int) bool {
		if unified[i].CPUPercent != unified[j].CPUPercent {
			return unified[i].CPUPercent > unified[j].CPUPercent
		}
		return unified[i].MemoryRSS > unified[j].MemoryRSS
	})

	if len(unified) > p.maxTop {
		unified = unified[:p.maxTop]
	}

	report.Processes = unified
	return nil
}
