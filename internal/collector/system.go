// Package collector – system.go implements all hardware-level subsystem
// collectors using github.com/shirou/gopsutil/v3.
//
// Filtering rules applied by each collector:
//   - CPU:     uses a 500 ms sample window via gopsutil to get a meaningful
//              utilisation delta rather than a noisy instantaneous snapshot.
//   - Disk:    pseudo-filesystems (proc, sys, devtmpfs, tmpfs, devfs,
//              overlay, squashfs, nsfs, cgroup, cgroup2, debugfs, tracefs,
//              securityfs, pstore, bpf, hugetlbfs, mqueue, fusectl, efivarfs)
//              and loop-back devices (/dev/loop*) are excluded.
//   - Network: loopback ("lo") and common virtual/tunnel interfaces (veth*,
//              docker*, br-*, virbr*, tun*, tap*) are excluded.
package collector

import (
	"context"
	"strings"
	"time"

	gopshost "github.com/shirou/gopsutil/v3/host"
	gopscpu "github.com/shirou/gopsutil/v3/cpu"
	gopsdisk "github.com/shirou/gopsutil/v3/disk"
	gopsmem "github.com/shirou/gopsutil/v3/mem"
	gopsnet "github.com/shirou/gopsutil/v3/net"

	"github.com/telemetry-agent/internal/model"
)

// ─── Host collector ──────────────────────────────────────────────────────────

// HostCollector collects static identity information about the machine.
type HostCollector struct{}

// NewHostCollector returns a ready-to-use [HostCollector].
func NewHostCollector() *HostCollector { return &HostCollector{} }

// Name implements [Collector].
func (h *HostCollector) Name() string { return "host" }

// Collect implements [Collector].
func (h *HostCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	info, err := gopshost.InfoWithContext(ctx)
	if err != nil {
		return err
	}

	report.Host = model.HostInfo{
		Hostname:        info.Hostname,
		OS:              info.OS,
		Platform:        info.Platform,
		PlatformVersion: info.PlatformVersion,
		KernelVersion:   info.KernelVersion,
		UptimeSeconds:   info.Uptime,
	}
	return nil
}

// ─── CPU collector ───────────────────────────────────────────────────────────

// CPUCollector samples CPU utilisation over a short window so that the
// returned value reflects meaningful activity rather than a noisy instant.
type CPUCollector struct {
	// sampleWindow is the duration gopsutil uses to compute a utilisation
	// delta; 500 ms is a good balance between accuracy and latency.
	sampleWindow time.Duration
}

// NewCPUCollector returns a [CPUCollector] with the given sample window.
// Pass 0 to use the default 500 ms window.
func NewCPUCollector(sampleWindow time.Duration) *CPUCollector {
	if sampleWindow <= 0 {
		sampleWindow = 500 * time.Millisecond
	}
	return &CPUCollector{sampleWindow: sampleWindow}
}

// Name implements [Collector].
func (c *CPUCollector) Name() string { return "cpu" }

// Collect implements [Collector].
func (c *CPUCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	// Overall utilisation across all cores.
	totals, err := gopscpu.PercentWithContext(ctx, c.sampleWindow, false)
	if err != nil {
		return err
	}

	// Per-core utilisation; gopsutil re-samples internally.
	perCore, err := gopscpu.PercentWithContext(ctx, c.sampleWindow, true)
	if err != nil {
		return err
	}

	counts, err := gopscpu.CountsWithContext(ctx, true) // logical
	if err != nil {
		counts = len(perCore)
	}

	var overall float64
	if len(totals) > 0 {
		overall = totals[0]
	}

	report.CPU = model.CPUStats{
		CoreCount:      counts,
		UsagePercent:   roundFloat(overall, 2),
		PerCorePercent: roundFloats(perCore, 2),
	}
	return nil
}

// ─── Memory collector ────────────────────────────────────────────────────────

// MemoryCollector collects physical RAM and swap utilisation.
type MemoryCollector struct{}

// NewMemoryCollector returns a ready-to-use [MemoryCollector].
func NewMemoryCollector() *MemoryCollector { return &MemoryCollector{} }

// Name implements [Collector].
func (m *MemoryCollector) Name() string { return "memory" }

// Collect implements [Collector].
func (m *MemoryCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	vm, err := gopsmem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return err
	}

	swap, err := gopsmem.SwapMemoryWithContext(ctx)
	if err != nil {
		// Non-fatal: swap may not exist on all systems.
		swap = &gopsmem.SwapMemoryStat{}
	}

	report.Memory = model.MemoryStats{
		Physical: model.MemorySegment{
			TotalBytes:   vm.Total,
			UsedBytes:    vm.Used,
			FreeBytes:    vm.Free,
			UsagePercent: roundFloat(vm.UsedPercent, 2),
		},
		Swap: model.MemorySegment{
			TotalBytes:   swap.Total,
			UsedBytes:    swap.Used,
			FreeBytes:    swap.Free,
			UsagePercent: roundFloat(swap.UsedPercent, 2),
		},
	}
	return nil
}

// ─── Disk collector ──────────────────────────────────────────────────────────

// pseudoFSTypes is the set of filesystem type identifiers that should be
// excluded from disk reports because they are virtual or kernel-internal.
var pseudoFSTypes = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "tmpfs": true,
	"devfs": true, "overlay": true, "squashfs": true, "nsfs": true,
	"cgroup": true, "cgroup2": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "bpf": true, "hugetlbfs": true,
	"mqueue": true, "fusectl": true, "efivarfs": true, "autofs": true,
	"rpc_pipefs": true, "configfs": true, "selinuxfs": true,
}

// DiskCollector reports capacity and IO counters for real, mounted filesystems.
type DiskCollector struct{}

// NewDiskCollector returns a ready-to-use [DiskCollector].
func NewDiskCollector() *DiskCollector { return &DiskCollector{} }

// Name implements [Collector].
func (d *DiskCollector) Name() string { return "disk" }

// Collect implements [Collector].
func (d *DiskCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	partitions, err := gopsdisk.PartitionsWithContext(ctx, false)
	if err != nil {
		return err
	}

	// Fetch IO counters for all devices in one call; ignore error because
	// some environments (containers, certain BSDs) may not expose it.
	ioCounters, _ := gopsdisk.IOCountersWithContext(ctx)

	var stats []model.DiskStats
	seen := make(map[string]bool)

	for _, p := range partitions {
		if pseudoFSTypes[p.Fstype] {
			continue
		}
		// Exclude loop devices.
		if strings.HasPrefix(p.Device, "/dev/loop") {
			continue
		}
		// Deduplicate by device to avoid double-counting bind mounts.
		if seen[p.Device] {
			continue
		}
		seen[p.Device] = true

		usage, err := gopsdisk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue // partition may have been unmounted between list and usage call
		}

		ds := model.DiskStats{
			MountPoint:   p.Mountpoint,
			Device:       p.Device,
			Fstype:       p.Fstype,
			TotalBytes:   usage.Total,
			UsedBytes:    usage.Used,
			FreeBytes:    usage.Free,
			UsagePercent: roundFloat(usage.UsedPercent, 2),
		}

		// Resolve the device basename for counter lookup (e.g. "/dev/sda1" → "sda1").
		devName := deviceBasename(p.Device)
		if ic, ok := ioCounters[devName]; ok {
			ds.ReadBytesTotal = ic.ReadBytes
			ds.WriteBytesTotal = ic.WriteBytes
			ds.ReadOpsTotal = ic.ReadCount
			ds.WriteOpsTotal = ic.WriteCount
		}

		stats = append(stats, ds)
	}

	report.Disks = stats
	return nil
}

// deviceBasename extracts the trailing component of a device path.
// "/dev/sda1" → "sda1", "C:" → "C:", "sda1" → "sda1".
func deviceBasename(device string) string {
	if idx := strings.LastIndexAny(device, "/\\"); idx >= 0 {
		return device[idx+1:]
	}
	return device
}

// ─── Network collector ───────────────────────────────────────────────────────

// virtualIfacePrefixes lists the network interface name prefixes that should
// be excluded from the network report because they are virtual, overlay, or
// loopback adapters.
var virtualIfacePrefixes = []string{
	"lo", "veth", "docker", "br-", "virbr", "tun", "tap",
	"dummy", "bond", "vlan", "macvlan", "vxlan", "flannel",
}

// NetworkCollector collects per-interface traffic counters for physical and
// non-virtual network interfaces.
type NetworkCollector struct{}

// NewNetworkCollector returns a ready-to-use [NetworkCollector].
func NewNetworkCollector() *NetworkCollector { return &NetworkCollector{} }

// Name implements [Collector].
func (n *NetworkCollector) Name() string { return "network" }

// Collect implements [Collector].
func (n *NetworkCollector) Collect(ctx context.Context, report *model.TelemetryReport) error {
	counters, err := gopsnet.IOCountersWithContext(ctx, true) // per-NIC
	if err != nil {
		return err
	}

	var stats []model.NetworkStats
	for _, c := range counters {
		if isVirtualInterface(c.Name) {
			continue
		}
		stats = append(stats, model.NetworkStats{
			Interface:   c.Name,
			BytesSent:   c.BytesSent,
			BytesRecv:   c.BytesRecv,
			PacketsSent: c.PacketsSent,
			PacketsRecv: c.PacketsRecv,
			DropIn:      c.Dropin,
			DropOut:     c.Dropout,
			ErrorIn:     c.Errin,
			ErrorOut:    c.Errout,
		})
	}

	report.Network = stats
	return nil
}

// isVirtualInterface returns true when name matches any of the known virtual
// interface prefixes.  The check is case-insensitive so that Windows adapter
// names like "Loopback Pseudo-Interface" are caught by "lo".
func isVirtualInterface(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range virtualIfacePrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// roundFloat truncates f to prec decimal places without importing math/big.
func roundFloat(f float64, prec int) float64 {
	p := 1.0
	for i := 0; i < prec; i++ {
		p *= 10
	}
	return float64(int64(f*p+0.5)) / p
}

// roundFloats applies roundFloat to every element of fs.
func roundFloats(fs []float64, prec int) []float64 {
	out := make([]float64, len(fs))
	for i, f := range fs {
		out[i] = roundFloat(f, prec)
	}
	return out
}
