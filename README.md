# Telemetry Agent

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat)](LICENSE)
[![Build](https://img.shields.io/badge/build-passing-brightgreen?style=flat)](#build)
[![RSS](https://img.shields.io/badge/memory-%3C15%20MB%20RSS-success?style=flat)](#footprint)
[![CPU](https://img.shields.io/badge/cpu-%3C0.5%25%20idle-success?style=flat)](#footprint)

A **zero-dependency, ultra-lightweight system telemetry agent** written in pure Go. Inspired by [Beszel](https://github.com/henrygd/beszel) and [node_exporter](https://github.com/prometheus/node_exporter), it collects hardware and OS metrics and ships them to a central hub over HTTP — as a single, statically linked binary under 15 MB RSS.

---

## Table of Contents

- [Features](#features)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
- [Installation](#installation)
- [Configuration Reference](#configuration-reference)
- [Telemetry Payload](#telemetry-payload)
- [Running as a Service](#running-as-a-service)
- [Building from Source](#building-from-source)
- [Footprint](#footprint)
- [Contributing](#contributing)

---

## Features

| Capability | Detail |
|---|---|
| **Zero runtime deps** | Single static binary, no libc, no external libraries needed at runtime |
| **Cross-platform** | Linux (x86_64, ARM64) and Windows (x86_64) |
| **Minimal footprint** | < 15 MB RSS · < 0.5 % CPU between ticks |
| **Resilient transport** | Exponential backoff with jitter; hub downtime never crashes the agent |
| **Structured logging** | JSON output via `log/slog`; configurable level |
| **Graceful shutdown** | SIGINT / SIGTERM drains in-flight requests and exits cleanly |
| **Clean architecture** | Config · Model · Collector · Transport — each in a dedicated package |

### Metrics collected

- **Host** — hostname, OS, platform, kernel version, uptime
- **CPU** — aggregate + per-core utilisation (500 ms sampling window)
- **Memory** — physical RAM and swap: total / used / free / %
- **Disk** — per-mount-point: capacity, utilisation %, and cumulative IO counters
- **Network** — per-interface: bytes, packets, drops, and errors (TX + RX)

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     telemetry-agent                      │
│                                                         │
│  ┌─────────┐    ┌──────────────────────────────────┐   │
│  │ config  │───▶│         Orchestrator              │   │
│  └─────────┘    │  (ticker every N seconds)         │   │
│                 │                                   │   │
│  ┌──────────┐   │  ┌──────────┐  ┌──────────────┐  │   │
│  │HostColl. │◀──┤  │ CPU Coll │  │ Memory Coll. │  │   │
│  └──────────┘   │  └──────────┘  └──────────────┘  │   │
│  ┌──────────┐   │  ┌──────────┐  ┌──────────────┐  │   │
│  │DiskColl. │◀──┤  │ Net Coll │  │  (future…)   │  │   │
│  └──────────┘   │  └──────────┘  └──────────────┘  │   │
│                 └────────────┬─────────────────────┘   │
│                              │ TelemetryReport          │
│                              ▼                          │
│                    ┌──────────────────┐                 │
│                    │   HTTPSender     │                 │
│                    │  (backoff+retry) │                 │
│                    └────────┬─────────┘                 │
└─────────────────────────────┼───────────────────────────┘
                              │  POST /api/v1/metrics
                              ▼
                    ┌──────────────────┐
                    │   Telemetry Hub  │
                    │  (your backend)  │
                    └──────────────────┘
```

### Package layout

```
telemetry-agent/
├── cmd/
│   ├── agent/main.go           # Agent entrypoint: config → logger → sender → orchestrator
│   └── hub/main.go             # Hub entrypoint: sqlite store → REST API → embedded web dashboard
├── internal/
│   ├── config/config.go        # Flags + ENV → validated Config struct
│   ├── model/metrics.go        # TelemetryReport and all sub-types (JSON)
│   ├── collector/
│   │   ├── collector.go        # Collector interface + Orchestrator ticker
│   │   └── system.go           # Host, CPU, Memory, Disk, Network collectors
│   ├── transport/
│   │   ├── sender.go           # Sender interface (Send + Close)
│   │   └── http_sender.go      # HTTP POST + exponential backoff with jitter
│   └── hub/
│       ├── store.go            # Pure-Go SQLite persistence layer (modernc.org/sqlite)
│       ├── api.go              # REST API endpoints, auth middleware & SSE broker
│       └── server.go           # HTTP server, CORS, access logs & graceful shutdown
├── webui/
│   ├── embed.go                # go:embed static asset package
│   └── web/index.html          # Embedded dark-mode Tailwind + Chart.js dashboard
├── scripts/build.sh            # Size-optimised release build script
├── Makefile                    # Targets: build, build-hub, build-all, run, run-hub, test
└── README.md
```

---

## Quick Start

```bash
# 1. Download the latest binary (Linux x86_64)
curl -L https://github.com/your-org/telemetry-agent/releases/latest/download/telemetry-agent-linux-amd64 \
  -o telemetry-agent
chmod +x telemetry-agent

# 2. Run against your hub
./telemetry-agent \
  --hub-url https://hub.example.com \
  --auth-token YOUR_TOKEN \
  --interval 15s \
  --log-level info
```

---

## Installation

### Binary (recommended)

Download the pre-built binary for your platform from the [Releases](https://github.com/your-org/telemetry-agent/releases) page.

| Platform | Binary |
|---|---|
| Linux x86_64 | `telemetry-agent-linux-amd64` |
| Linux ARM64 | `telemetry-agent-linux-arm64` |
| Windows x86_64 | `telemetry-agent-windows-amd64.exe` |

### Docker

```dockerfile
FROM scratch
COPY telemetry-agent-linux-amd64 /telemetry-agent
ENTRYPOINT ["/telemetry-agent"]
```

```bash
docker run --rm \
  -e HUB_URL=https://hub.example.com \
  -e AUTH_TOKEN=YOUR_TOKEN \
  -e INTERVAL=15s \
  ghcr.io/your-org/telemetry-agent:latest
```

---

## Configuration Reference

All options can be supplied as **CLI flags** or **environment variables**.  
Flags take precedence over environment variables.

| Flag | Env Var | Default | Description |
|---|---|---|---|
| `--hub-url` | `HUB_URL` | `http://localhost:8080` | Base URL of the telemetry hub |
| `--auth-token` | `AUTH_TOKEN` | _(empty)_ | Bearer token for hub authentication |
| `--node-id` | `NODE_ID` | OS hostname | Stable identifier for this node |
| `--interval` | `INTERVAL` | `15s` | Metric collection interval (min `5s`) |
| `--log-level` | `LOG_LEVEL` | `info` | Log verbosity: `debug` · `info` · `warn` · `error` |
| `--request-timeout` | — | `10s` | HTTP request timeout per attempt |
| `--max-retries` | — | `5` | Max send retries before dropping a payload |
| `--retry-base-delay` | — | `500ms` | Initial backoff delay before first retry |
| `--retry-max-delay` | — | `30s` | Maximum backoff delay cap |

---

## Telemetry Payload

Every collection tick produces a single JSON object posted to `POST <HUB_URL>/api/v1/metrics`:

```jsonc
{
  "node_id": "web-01",
  "agent_version": "v1.2.0",
  "collected_at": "2024-01-15T10:30:00Z",
  "host": {
    "hostname": "web-01",
    "os": "linux",
    "platform": "ubuntu",
    "platform_version": "22.04",
    "kernel_version": "5.15.0-91-generic",
    "uptime_seconds": 864000
  },
  "cpu": {
    "core_count": 8,
    "usage_percent": 12.5,
    "per_core_percent": [14.2, 11.8, 13.1, 10.9, 12.0, 13.5, 11.2, 14.8]
  },
  "memory": {
    "physical": { "total_bytes": 16777216000, "used_bytes": 8388608000, "free_bytes": 8388608000, "usage_percent": 50.0 },
    "swap":     { "total_bytes": 4294967296,  "used_bytes": 0,          "free_bytes": 4294967296,  "usage_percent": 0.0  }
  },
  "disks": [
    {
      "mount_point": "/", "device": "/dev/sda1", "fstype": "ext4",
      "total_bytes": 107374182400, "used_bytes": 32212254720,
      "free_bytes": 75161927680,  "usage_percent": 30.0,
      "read_bytes_total": 1073741824, "write_bytes_total": 536870912,
      "read_ops_total": 102400, "write_ops_total": 51200
    }
  ],
  "network": [
    {
      "interface": "eth0",
      "bytes_sent": 1073741824, "bytes_recv": 2147483648,
      "packets_sent": 1048576,   "packets_recv": 2097152,
      "drop_in": 0, "drop_out": 0, "error_in": 0, "error_out": 0
    }
  ]
}
```

### HTTP Headers

| Header | Value |
|---|---|
| `Content-Type` | `application/json; charset=utf-8` |
| `Authorization` | `Bearer <AUTH_TOKEN>` _(omitted when token is empty)_ |
| `X-Node-ID` | The configured node identifier |
| `X-Collected-At` | RFC3339 timestamp of the collection instant |
| `User-Agent` | `telemetry-agent/1.0` |

---

## Running as a Service

### Linux — systemd

Save as `/etc/systemd/system/telemetry-agent.service`:

```ini
[Unit]
Description=Telemetry Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nobody
ExecStart=/usr/local/bin/telemetry-agent \
  --hub-url=https://hub.example.com \
  --auth-token=YOUR_TOKEN \
  --interval=15s
Restart=on-failure
RestartSec=5s
# Hard memory cap — agent targets < 15 MB but this is a safety net.
MemoryMax=64M
# No new privileges, private /tmp.
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now telemetry-agent
sudo journalctl -u telemetry-agent -f
```

### Windows — Services (sc.exe)

```powershell
# Copy the binary
Copy-Item .\telemetry-agent-windows-amd64.exe C:\Program Files\TelemetryAgent\agent.exe

# Create the service
sc.exe create TelemetryAgent `
  binPath= '"C:\Program Files\TelemetryAgent\agent.exe" --hub-url=https://hub.example.com --auth-token=YOUR_TOKEN' `
  start= auto `
  DisplayName= "Telemetry Agent"

sc.exe description TelemetryAgent "Lightweight system telemetry agent"
sc.exe start TelemetryAgent
```

---

## Building from Source

**Prerequisites:** Go 1.22+, Git

```bash
git clone https://github.com/your-org/telemetry-agent.git
cd telemetry-agent

# Download dependencies
go mod download

# Build for the current platform
make build            # → ./dist/telemetry-agent

# Cross-compile all platforms
make build-all        # → ./dist/telemetry-agent-{linux-amd64,linux-arm64,windows-amd64.exe}

# Run tests
make test

# Run locally (debug mode)
HUB_URL=http://localhost:8080 make run
```

Using the shell script directly:

```bash
# Linux/macOS/WSL
chmod +x scripts/build.sh
./scripts/build.sh

# Cross-compile
OS=linux ARCH=arm64 ./scripts/build.sh
OS=windows ARCH=amd64 ./scripts/build.sh
```

---

## Footprint

The agent is engineered to be invisible on the host:

| Metric | Target | Typical |
|---|---|---|
| RSS memory | < 15 MB | ~8–12 MB |
| CPU (idle between ticks) | < 0.5 % | < 0.1 % |
| Binary size (stripped) | — | ~10–14 MB |
| Goroutines at rest | — | 5–8 |

**Key decisions that minimise footprint:**
- Single `time.Ticker` owned by the orchestrator; no per-collector goroutines.
- Short-lived `bytes.Buffer` per tick — immediately GC'd after the HTTP POST.
- Reused HTTP keep-alive connection pool (max 4 idle connections).
- `CGO_ENABLED=0` + `-trimpath -s -w` = no C runtime, no debug bloat.

---

## Contributing

1. Fork the repository and create a feature branch.
2. Run `make vet fmt test` before opening a PR.
3. Keep each commit focused — one logical change per commit.
4. Update documentation and add tests for new collectors or transport backends.

---

## License

MIT © 2024 Your Organisation. See [LICENSE](LICENSE) for details.
