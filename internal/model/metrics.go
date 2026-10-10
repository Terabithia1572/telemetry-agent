// Package model defines the domain types for telemetry payloads transmitted
// from a telemetry agent to a central hub. All types are JSON-serialisable and
// carry only value semantics (no unexported fields) so they can be freely
// marshalled without a custom encoder.
package model

import "time"

// HostInfo captures static identity information about the monitored node.
// These fields change rarely and are included in every TelemetryReport for
// correlation across long time-series queries.
type HostInfo struct {
	// Hostname is the OS-reported short hostname of the machine.
	Hostname string `json:"hostname"`

	// OS is the operating system family, e.g. "linux" or "windows".
	OS string `json:"os"`

	// Platform is the specific distribution or edition, e.g. "ubuntu" or
	// "Windows Server 2022".
	Platform string `json:"platform"`

	// PlatformVersion is the version string for the platform.
	PlatformVersion string `json:"platform_version"`

	// KernelVersion is the running kernel or NT version string.
	KernelVersion string `json:"kernel_version"`

	// UptimeSeconds is the number of seconds since the host last booted.
	UptimeSeconds uint64 `json:"uptime_seconds"`
}

// CPUStats carries aggregate and per-core CPU utilisation percentages for the
// current collection interval.
type CPUStats struct {
	// CoreCount is the number of logical CPU cores exposed to the OS.
	CoreCount int `json:"core_count"`

	// UsagePercent is the overall (aggregate) CPU utilisation across all cores,
	// expressed as a value in [0, 100].
	UsagePercent float64 `json:"usage_percent"`

	// PerCorePercent holds per-logical-core utilisation in the same [0, 100]
	// range, indexed by logical core number.
	PerCorePercent []float64 `json:"per_core_percent"`
}

// MemoryStats describes the utilisation of both physical RAM and swap space.
type MemoryStats struct {
	// Physical holds utilisation figures for RAM.
	Physical MemorySegment `json:"physical"`

	// Swap holds utilisation figures for swap / page-file space.
	Swap MemorySegment `json:"swap"`
}

// MemorySegment captures the capacity breakdown of a single memory region.
// All byte values are expressed in bytes to avoid rounding ambiguity.
type MemorySegment struct {
	// TotalBytes is the total capacity of this memory segment in bytes.
	TotalBytes uint64 `json:"total_bytes"`

	// UsedBytes is the number of bytes currently in use.
	UsedBytes uint64 `json:"used_bytes"`

	// FreeBytes is the number of bytes available without eviction or swapping.
	FreeBytes uint64 `json:"free_bytes"`

	// UsagePercent is the ratio of used to total, expressed as [0, 100].
	UsagePercent float64 `json:"usage_percent"`
}

// DiskStats reports utilisation and IO counters for a single mounted filesystem.
type DiskStats struct {
	// MountPoint is the path at which this filesystem is mounted, e.g. "/" or
	// "C:\".
	MountPoint string `json:"mount_point"`

	// Device is the backing block device or volume name.
	Device string `json:"device"`

	// Fstype is the filesystem type, e.g. "ext4" or "NTFS".
	Fstype string `json:"fstype"`

	// TotalBytes is the raw capacity of the filesystem in bytes.
	TotalBytes uint64 `json:"total_bytes"`

	// UsedBytes is the number of bytes currently allocated to files.
	UsedBytes uint64 `json:"used_bytes"`

	// FreeBytes is the number of bytes available for new writes.
	FreeBytes uint64 `json:"free_bytes"`

	// UsagePercent is the ratio of used to total, expressed as [0, 100].
	UsagePercent float64 `json:"usage_percent"`

	// ReadBytesTotal is the cumulative number of bytes read from this device
	// since boot (monotonically increasing counter).
	ReadBytesTotal uint64 `json:"read_bytes_total"`

	// WriteBytesTotal is the cumulative number of bytes written to this device
	// since boot (monotonically increasing counter).
	WriteBytesTotal uint64 `json:"write_bytes_total"`

	// ReadOpsTotal is the cumulative number of completed read IO operations.
	ReadOpsTotal uint64 `json:"read_ops_total"`

	// WriteOpsTotal is the cumulative number of completed write IO operations.
	WriteOpsTotal uint64 `json:"write_ops_total"`
}

// NetworkStats captures the traffic counters for a single network interface.
type NetworkStats struct {
	// Interface is the OS name of the network interface, e.g. "eth0" or
	// "Ethernet".
	Interface string `json:"interface"`

	// BytesSent is the cumulative count of bytes transmitted by this interface
	// since boot.
	BytesSent uint64 `json:"bytes_sent"`

	// BytesRecv is the cumulative count of bytes received by this interface
	// since boot.
	BytesRecv uint64 `json:"bytes_recv"`

	// PacketsSent is the cumulative count of packets transmitted.
	PacketsSent uint64 `json:"packets_sent"`

	// PacketsRecv is the cumulative count of packets received.
	PacketsRecv uint64 `json:"packets_recv"`

	// DropIn is the cumulative count of inbound packets dropped.
	DropIn uint64 `json:"drop_in"`

	// DropOut is the cumulative count of outbound packets dropped.
	DropOut uint64 `json:"drop_out"`

	// ErrorIn is the cumulative count of inbound errors.
	ErrorIn uint64 `json:"error_in"`

	// ErrorOut is the cumulative count of outbound errors.
	ErrorOut uint64 `json:"error_out"`
}

// TelemetryReport is the root envelope transmitted from the agent to the hub
// at every collection tick. It aggregates all subsystem metrics along with
// enough metadata for the hub to route, store, and deduplicate the payload.
type TelemetryReport struct {
	// NodeID is a stable, administrator-assigned identifier for this host.
	// It is set via the NODE_ID environment variable or --node-id flag and
	// defaults to the OS hostname when unset.
	NodeID string `json:"node_id"`

	// AgentVersion is the semver string of the running agent binary,
	// injected at compile time via -ldflags.
	AgentVersion string `json:"agent_version"`

	// CollectedAt is the UTC wall-clock instant at which collection finished.
	// Consumers should use this field (not the HTTP receipt time) for
	// time-series alignment.
	CollectedAt time.Time `json:"collected_at"`

	// Host provides static identity information about the node.
	Host HostInfo `json:"host"`

	// CPU provides current CPU utilisation statistics.
	CPU CPUStats `json:"cpu"`

	// Memory provides current physical RAM and swap utilisation.
	Memory MemoryStats `json:"memory"`

	// Disks is an ordered slice of per-filesystem utilisation records.
	// Virtual, pseudo, and loop-back filesystems are excluded by the collector.
	Disks []DiskStats `json:"disks"`

	// Network is an ordered slice of per-interface traffic counters.
	// Loopback and virtual interfaces are excluded by the collector.
	Network []NetworkStats `json:"network"`

	// Processes holds top resource-consuming processes on the host.
	Processes []ProcessInfo `json:"processes"`
}

// ProcessInfo captures resource consumption metrics for a single running process.
type ProcessInfo struct {
	// PID is the OS process identifier.
	PID int32 `json:"pid"`

	// Name is the executable name or command.
	Name string `json:"name"`

	// CPUPercent is the process CPU utilisation percentage [0, 100*cores].
	CPUPercent float64 `json:"cpu_percent"`

	// MemoryRSS is the resident set size in bytes.
	MemoryRSS uint64 `json:"memory_rss"`

	// MemoryPercent is the percentage of total RAM consumed by this process.
	MemoryPercent float32 `json:"memory_percent"`
}
