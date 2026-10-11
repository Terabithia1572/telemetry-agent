<div align="center">

# 🌐 Telemetry Suite

**[🇹🇷 Türkçe](#-türkçe)** &nbsp;•&nbsp; **[🇬🇧 English](#-english)**

---

</div>

<a id="-türkçe"></a>
# 🇹🇷 Telemetry Agent & Hub — Dağıtık Sistem Gözlemlenebilirlik ve Uç Yönetim Platformu

[![Go Sürümü](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev)
[![Lisans](https://img.shields.io/badge/lisans-MIT-blue?style=flat)](LICENSE)
[![Derleme](https://img.shields.io/badge/derleme-ba%C5%9Far%C4%B1l%C4%B1-brightgreen?style=flat)](#kaynaktan-derleme)
[![RSS Bellek](https://img.shields.io/badge/bellek-%3C15%20MB%20RSS-success?style=flat)](#kaynak-tüketimi--ayak-izi)
[![CPU Kullanımı](https://img.shields.io/badge/cpu-%3C0.5%25%20bo%C5%9Fta-success?style=flat)](#kaynak-tüketimi--ayak-izi)

<p align="center">
  <img src="assets/dashboard.png" alt="Telemetry Hub Kontrol Paneli Önizlemesi" width="100%" />
</p>

[Beszel](https://github.com/henrygd/beszel) ve [node_exporter](https://github.com/prometheus/node_exporter) mimarilerinden ilham alan, saf Go (Golang) ile geliştirilmiş, **sıfır harici çalışma zamanı bağımlılığına (zero-dependency)** ve **ultra düşük kaynak tüketimine** sahip uçtan uca sistem telemetri ajanı ve merkezi gözlemlenebilirlik kontrol düzlemi.

Platform; donanım düzeyinde çoklu altsistem telemetrisi, en çok kaynak tüketen süreçlerin gerçek zamanlı analizi, Docker konteyner gözlemlenebilirliği, Discord/Slack webhook entegrasyonlu eşik değerli alarm motoru ve güvenli, beyaz liste (whitelist) korumalı uzaktan sorun giderme araçları sunar.

Hem **Ajan (`telemetry-agent`)** hem de **Merkezi Kontrol Paneli (`telemetry-hub`)**, `CGO_ENABLED=0` ile statik olarak tek bir ikili (binary) dosyaya derlenir ve libc veya harici veritabanı kurulumu gerektirmeden çalışır.

---

## 🇹🇷 Özellikler & Arayüz Tanıtımı

Telemetry Hub, modern tasarım sistemlerinden (TailAdmin ve shadcn/ui) ilham alan, gömülü (`embed.FS`) ve karanlık modlu bir web paneline sahiptir.

### Küme Genel Bakışı & Gerçek Zamanlı Teleometri
- **Küme KPI Göstergeleri:** Toplam düğüm sayısı, sağlıklı/çevrim içi düğüm oranı, küme geneli ortalama CPU yükü, toplam bellek tahsisi ve çalışan Docker konteyner sayısı anlık özetlenir.
- **Dinamik Düğüm Kartları:** Eşik değerlerine göre renk değiştiren (mavi/sarı/kırmızı) mikro ilerleme çubukları, ağ RX/TX bant genişliği sayaçları ve dinamik göreceli zaman damgaları (*"az önce"*, *"15sn önce"*).
- **Anlık Arama & Filtreleme:** Sunucu adı veya işletim sistemine göre gerçek zamanlı arama; Tümü, Çevrim içi ve Çevrim dışı durum filtreleri.
- **Çift Dil Desteği (i18n):** Türkçe (**TR**, varsayılan) ve İngilizce (**EN**) dilleri arasında sayfa yenilenmeden anında geçiş; tercih tarayıcı `localStorage` üzerinde saklanır.

### Düğüm İnceleme & Gömülü Aksiyon Terminali

<p align="center">
  <img src="assets/node-details.png" alt="Düğüm Metrikleri ve Donanım Detayları" width="49%" />
  <img src="assets/node-terminal.png" alt="Uzaktan Aksiyonlar ve Terminal Konsolu" width="49%" />
</p>

- **Derinlemesine Donanım ve İşletim Sistemi Bilgisi:** Çekirdek (kernel) sürümü, mimari, sistem çalışma süresi (uptime) ve son 1 saatlik CPU, RAM, Disk kullanım grafikleri.
- **En Çok Kaynak Tüketen Süreçler (Top Processes):** Sistemde işlemci ve belleği en çok tüketen süreçlerin PID, isim, CPU%, RSS bellek ve bellek% detaylarıyla anlık dökümü.
- **Docker Konteyner Gözlemlenebilirliği:** Konteyner durumu, CPU yüzdesi ve bellek sınırlarının canlı takibi.
- **Beyaz Liste Korumalı Uzaktan Aksiyonlar:** Web arayüzü üzerinden DNS önbelleğini boşaltma, geçici dosyaları temizleme, ağ geçidine ping atma ve konteynerleri yeniden başlatma; çıktılar canlı ANSI terminal konsolunda görüntülenir.

---

## 🇹🇷 Yetenek Matrisi

| Yetenek | Kapsam | Detaylar |
|---|---|---|
| **Sıfır Çalışma Zamanı Bağımlılığı** | Ajan & Hub | `CGO_ENABLED=0` ile statik derlenmiş saf Go ikilileri; libc veya harici daemon bağımlılığı yoktur. |
| **Minimal Kaynak Ayak İzi** | Ajan | Örnekleme aralıklarında `< 15 MB` RSS bellek ve `< 0.5%` CPU kullanımı garantisi. |
| **Çoklu Altsistem Telemetrisi** | Ajan | CPU (toplam + çekirdek bazlı), RAM (fiziksel + takas), Disk (bağlama noktaları + G/Ç), Ağ (TX/RX hızları ve paket düşmeleri), Ana makine meta verileri. |
| **Süreç Analizi** | Ajan & Hub | CPU ve bellek tüketimine göre en yüksek süreçlerin anlık dökümü (PID, Süreç Adı, CPU %, RSS Bellek, Bellek %). |
| **Docker Konteyner Takibi** | Ajan & Hub | Unix soketi veya Windows named pipe üzerinden doğrudan motor iletişimi. Docker yüklü olmadığında zarifçe atlar. |
| **Gömülü Kontrol Düzlemi** | Hub | Saf Go SQLite (`modernc.org/sqlite`), Server-Sent Events (SSE) anlık veri akışı ve gömülü Tailwind CSS web arayüzü. |
| **Eşik Değerli Alarm Motoru** | Hub | CPU, Bellek, Disk eşikleri ve çevrim dışı kalma (>30sn sessizlik) kuralları; 10 dakikalık dalgalanma (cooldown) koruması ve yeşil çözüm bildirimi. |
| **Evrensel Webhook Dağıtıcısı** | Hub | Zengin Discord embed'leri (renkli durum çubukları) ve Slack uyumlu JSON bildirimleri; anında test gönderim desteği. |
| **Güvenli Uzaktan Aksiyonlar** | Ajan & Hub | Beyaz liste korumalı tanılama ve çözüm araçları (`flush_dns`, `clean_temp`, `ping_gateway`, `restart_docker_container`) ve etkileşimli web terminali. |

---

## 🇹🇷 Sistem Mimarisi

```
┌────────────────────────────────────────────────────────┐
│                   İzlenen Düğüm (Ajan)                 │
│                                                        │
│   ┌──────────────┐     ┌────────────────────────────┐  │
│   │ config (ENV) │────▶│       Orkestratör          │  │
│   └──────────────┘     │   (N saniyede bir tetik)   │  │
│                        │                            │  │
│   ┌──────────────┐     │  ┌───────────┐ ┌────────┐  │  │
│   │ Sistem / CPU │◀────┤  │ Bellek    │ │ Disk   │  │  │
│   └──────────────┘     │  └───────────┘ └────────┘  │  │
│   ┌──────────────┐     │  ┌───────────┐ ┌────────┐  │  │
│   │ Top Süreçler │◀────┤  │ Ağ        │ │ Docker │  │  │
│   └──────────────┘     │  └───────────┘ └────────┘  │  │
│                        └─────────────┬──────────────┘  │
│   ┌──────────────┐                   │ Rapor Yükü      │
│   │Aksiyon İşçisi│◀───┐              ▼                 │
│   │ (DNS/Ping/   │    │      ┌───────────────┐         │
│   │  Temp/Docker)│    │      │  HTTPSender   │         │
│   └──────┬───────┘    │      │ (retry+jitter)│         │
│          │            │      └───────┬───────┘         │
└──────────┼────────────┼──────────────┼─────────────────┘
           │ Sonuç      │ Kuyruk Sorgu │ POST /api/v1/metrics
           ▼            ▼              ▼
┌────────────────────────────────────────────────────────┐
│                 Merkezi Telemetry Hub                  │
│                                                        │
│   ┌────────────────────┐     ┌───────────────────────┐ │
│   │  REST API & SSE    │────▶│ SQLite DB (modernc)   │ │
│   │  Yönlendirici (Go) │     │(düğümler,geçmiş,eylem)│ │
│   └──────────┬─────────┘     └──────────┬────────────┘ │
│              │                          │              │
│              ▼                          ▼              │
│   ┌────────────────────┐     ┌───────────────────────┐ │
│   │ Gömülü Web Paneli  │     │ Alarm Motoru          │ │
│   │ (Karanlık Tema)    │     │ (Discord / Slack)     │ │
│   └────────────────────┘     └───────────────────────┘ │
└────────────────────────────────────────────────────────┘
```

---

## 🇹🇷 Hızlı Başlangıç

### 1. Telemetry Hub Sunucusunu Başlatma

#### Windows (PowerShell):
```powershell
.\dist\telemetry-hub.exe -listen=:18080 -db=telemetry.db
```

#### Linux / macOS (Bash):
```bash
./dist/telemetry-hub -listen=:18080 -db=telemetry.db
```
Tarayıcınızda **[http://localhost:18080](http://localhost:18080)** adresini açın.

### 2. Telemetry Ajanını Başlatma

İzlemek istediğiniz sunucu veya yerel makinede ayrı bir terminalde:

#### Windows (PowerShell):
```powershell
.\dist\telemetry-agent.exe -hub-url=http://127.0.0.1:18080 -interval=5s
```

#### Linux / macOS (Bash):
```bash
./dist/telemetry-agent -hub-url=http://127.0.0.1:18080 -interval=5s
```

---

## 🇹🇷 Uzaktan Aksiyonlar ve İyileştirme

Güvenlik esastır: **Rastgele kabuk (shell) komutlarının çalıştırılması kesinlikle engellenmiştir.** Ajan yalnızca beyaz listede tanımlı, parametreleri doğrulanmış komutları 10 saniyelik zaman aşımı korumasıyla yürütür:

| Aksiyon Adı | Desteklenen Sistemler | Açıklama |
|---|---|---|
| `flush_dns` | Windows, Linux, macOS | Yerel DNS önbelleğini temizler (`ipconfig /flushdns`, `resolvectl flush-caches`, `dscacheutil`). |
| `clean_temp` | Tüm Platformlar | İşletim sisteminin geçici dosya dizinindeki kilitli olmayan gereksiz dosyaları temizler. |
| `ping_gateway` | Windows, Linux, macOS | `1.1.1.1` adresine veya doğrulanan hedefe 4 paketlik tanılama pingi gönderir. |
| `restart_docker_container` | Docker Kurulu Sistemler | Belirtilen Docker konteynerini güvenle yeniden başlatır (`params: {"container_id": "ad"}`). |

---

## 🇹🇷 Alarm Motoru ve Webhook Bildirimleri

Merkezi Hub, gelen her telemetri raporunda ve düzenli aralıklarla kuralları değerlendirir:
- **İşlemci Alarmı:** CPU kullanımı eşik değeri aştığında tetiklenir (varsayılan `%90`).
- **Bellek Alarmı:** RAM kullanımı eşik değeri aştığında tetiklenir (varsayılan `%90`).
- **Disk Alarmı:** Disk doluluğu eşik değeri aştığında tetiklenir (varsayılan `%90`).
- **Çevrim Dışı Alarmı:** Ajan $> 30\text{s}$ boyunca sinyal göndermediğinde tetiklenir.
- **Dalgalanma Koruması (Anti-Spam):** 10 dakikalık bekleme süresi aynı durum için bildirim kirliliğini önler.
- **Otomatik Çözüm Bildirimi:** Değer normale döndüğünde yeşil **ÇÖZÜLDÜ (RESOLVED)** bildirimi gönderilir.

Web kancalarını kontrol panelinin üst çubuğundaki **Zil İkonuna** tıklayarak anında yapılandırabilir ve test edebilirsiniz.

---

## 🇹🇷 Kaynak Tüketimi & Ayak İzi

| Metrik | Hedeflenen | Gerçekleşen |
|---|---|---|
| **RSS Bellek** | `< 15 MB` | `~8–12 MB` |
| **CPU Kullanımı** | `< 0.5%` boşta | `< 0.1%` |
| **İkili Dosya Boyutu** | Minimal | Tek başına sıyrılmış ikili dosya (`~7 MB` Ajan, `~11 MB` Arayüz dahil Hub) |
| **Çalışma Bağımlılığı** | Sıfır | `CGO_ENABLED=0` statik ikili dosya, yalın Alpine veya `scratch` üzerinde çalışır |

---

### 🇹🇷 Geliştirici ve Telif Hakkı
Bu proje **Yunus İNAN** tarafından geliştirilmiştir. Tüm hakları saklıdır.  
Lisans: [MIT](LICENSE)

<br />

---
---

<br />

<a id="-english"></a>
# 🇬🇧 Telemetry Agent & Hub — Distributed Systems Observability & Edge Remediation Platform

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat)](LICENSE)
[![Build](https://img.shields.io/badge/build-passing-brightgreen?style=flat)](#building-from-source)
[![RSS Memory](https://img.shields.io/badge/memory-%3C15%20MB%20RSS-success?style=flat)](#footprint--resource-utilization)
[![CPU Usage](https://img.shields.io/badge/cpu-%3C0.5%25%20idle-success?style=flat)](#footprint--resource-utilization)

<p align="center">
  <img src="assets/dashboard.png" alt="Telemetry Hub Dashboard Preview" width="100%" />
</p>

An ultra-lightweight, **zero-dependency system telemetry agent and central observability control plane** written in pure Go (Golang), inspired by the engineering principles of [Beszel](https://github.com/henrygd/beszel) and [node_exporter](https://github.com/prometheus/node_exporter).

The platform delivers comprehensive hardware telemetry, real-time top resource-consuming process tables, Docker container observability, threshold-based alerting with Discord/Slack webhooks, and secure, strictly whitelisted remote diagnostic/remediation tooling.

Both the **Agent (`telemetry-agent`)** and the **Hub (`telemetry-hub`)** compile down to standalone, statically linked binaries (`CGO_ENABLED=0`) requiring zero external daemons, shared libraries, or database engines.

---

## 🇬🇧 Features & UI Walkthrough

The Telemetry Hub embeds a responsive, dark-mode web dashboard inspired by modern design systems (TailAdmin & shadcn/ui), packed directly into the binary via `embed.FS`.

### Fleet Overview & Real-Time Telemetry
- **Cluster KPI Gauges:** Real-time visibility into total fleet nodes, health ratio, aggregate cluster CPU load, memory allocation, and active Docker containers.
- **Dynamic Node Cards:** Visual resource gauges with warning/critical color thresholds, real-time network TX/RX throughput, and relative heartbeat timestamps (*"just now"*, *"15s ago"*).
- **Instant Search & Status Filtering:** Filter nodes live by hostname, operating system, or status (All / Online / Offline).
- **Persistent Dual-Language i18n:** Instant dynamic toggling between Turkish (**TR**, default) and English (**EN**) without reloading; preference saved in `localStorage`.

### Node Inspection & Embedded Action Terminal

<p align="center">
  <img src="assets/node-details.png" alt="Node Metrics & Hardware Details" width="49%" />
  <img src="assets/node-terminal.png" alt="Remote Actions & Terminal Console" width="49%" />
</p>

- **Deep Hardware & Host Specifications:** Inspect kernel releases, CPU architectures, uptime, and 1-hour time-series metric graphs for CPU, RAM, and storage.
- **Top Resource-Consuming Processes:** Live visibility into processes consuming the most CPU and memory (PID, Process Name, CPU %, Memory RSS, Memory %).
- **Docker Container Observability:** Monitor container status, CPU percent, and memory limits across services.
- **Whitelisted Remote Actions & Web Terminal Console:** Safely dispatch DNS flushing, temporary file purging, network gateway pings, and container restarts with instant streaming execution output in the interactive console.

---

## 🇬🇧 Complete Feature Matrix

| Capability | Scope | Details |
|---|---|---|
| **Zero Runtime Dependencies** | Agent & Hub | Statically linked pure-Go binaries (`CGO_ENABLED=0`), no libc or external daemon dependencies. |
| **Minimal Footprint** | Agent | Strict `< 15 MB` RSS memory and `< 0.5%` CPU utilization between telemetry ticks. |
| **Multi-Subsystem Telemetry** | Agent | CPU (aggregate + per-core), RAM (physical + swap), Disk (mount points + I/O), Network (TX/RX bandwidth & drops), and Host metadata. |
| **Top Process Monitoring** | Agent & Hub | Real-time extraction of top resource-consuming processes (PID, Name, CPU %, Memory RSS, Memory %). |
| **Docker Container Monitoring** | Agent & Hub | Direct engine communication via Unix socket or Windows named pipe. Gracefully skips when Docker is absent. |
| **Embedded Control Plane** | Hub | Pure Go SQLite (`modernc.org/sqlite`), Server-Sent Events (SSE) live streaming, and embedded Tailwind CSS dashboard. |
| **Threshold Alerting Engine** | Hub | Evaluates CPU, Memory, Disk thresholds, and offline transitions (>30s silence) with 10-minute cooldown and green recovery alerts. |
| **Universal Webhook Dispatcher** | Hub | Rich Discord embeds (colored status bars) and Slack-compatible attachments with instant delivery testing. |
| **Secure Remote Actions** | Agent & Hub | Whitelisted diagnostic & remediation actions (`flush_dns`, `clean_temp`, `ping_gateway`, `restart_docker_container`) with interactive web terminal console. |

---

## 🇬🇧 Platform Architecture

```
┌────────────────────────────────────────────────────────┐
│                   Managed Host (Agent)                 │
│                                                        │
│   ┌──────────────┐     ┌────────────────────────────┐  │
│   │ config (ENV) │────▶│       Orchestrator         │  │
│   └──────────────┘     │   (ticker every N seconds) │  │
│                        │                            │  │
│   ┌──────────────┐     │  ┌───────────┐ ┌────────┐  │  │
│   │ Host / CPU   │◀────┤  │ Memory    │ │ Disk   │  │  │
│   └──────────────┘     │  └───────────┘ └────────┘  │  │
│   ┌──────────────┐     │  ┌───────────┐ ┌────────┐  │  │
│   │ Top Processes│◀────┤  │ Network   │ │ Docker │  │  │
│   └──────────────┘     │  └───────────┘ └────────┘  │  │
│                        └─────────────┬──────────────┘  │
│   ┌──────────────┐                   │ Report Payload  │
│   │ Action Worker│◀───┐              ▼                 │
│   │ (DNS/Ping/   │    │      ┌───────────────┐         │
│   │  Temp/Docker)│    │      │  HTTPSender   │         │
│   └──────┬───────┘    │      │ (retry+jitter)│         │
│          │            │      └───────┬───────┘         │
└──────────┼────────────┼──────────────┼─────────────────┘
           │ Result     │ Poll Actions │ POST /api/v1/metrics
           ▼            ▼              ▼
┌────────────────────────────────────────────────────────┐
│                 Central Telemetry Hub                  │
│                                                        │
│   ┌────────────────────┐     ┌───────────────────────┐ │
│   │  REST API & SSE    │────▶│ SQLite DB (modernc)   │ │
│   │  Router (Go 1.22+) │     │ (nodes, history, acts)│ │
│   └──────────┬─────────┘     └──────────┬────────────┘ │
│              │                          │              │
│              ▼                          ▼              │
│   ┌────────────────────┐     ┌───────────────────────┐ │
│   │  Embedded Web UI   │     │ Alerting Engine       │ │
│   │  (Dark Dashboard)  │     │ (Discord / Slack)     │ │
│   └────────────────────┘     └───────────────────────┘ │
└────────────────────────────────────────────────────────┘
```

---

## 🇬🇧 Quick Start

### 1. Launch the Telemetry Hub

#### Windows (PowerShell):
```powershell
.\dist\telemetry-hub.exe -listen=:18080 -db=telemetry.db
```

#### Linux / macOS (Bash):
```bash
./dist/telemetry-hub -listen=:18080 -db=telemetry.db
```
Open **[http://localhost:18080](http://localhost:18080)** in your browser.

### 2. Launch the Telemetry Agent
In a separate terminal on any monitored server:

#### Windows (PowerShell):
```powershell
.\dist\telemetry-agent.exe -hub-url=http://127.0.0.1:18080 -interval=5s
```

#### Linux / macOS (Bash):
```bash
./dist/telemetry-agent -hub-url=http://127.0.0.1:18080 -interval=5s
```

---

## 🇬🇧 Remote Actions & Remediation

Security is paramount: **arbitrary shell execution is strictly disallowed**. The agent only executes whitelisted, parameter-validated actions with a hard 10-second timeout:

| Action | Supported Systems | Description |
|---|---|---|
| `flush_dns` | Windows, Linux, macOS | Flushes local DNS resolver (`ipconfig /flushdns`, `resolvectl flush-caches`, `dscacheutil`). |
| `clean_temp` | All platforms | Safely purges temporary files in the OS temp directory, preserving active/locked files. |
| `ping_gateway` | Windows, Linux, macOS | Executes a 4-packet diagnostic ping to `1.1.1.1` or a sanitized custom target. |
| `restart_docker_container` | Docker hosts | Safely restarts a target Docker container (`params: {"container_id": "name"}`). |

---

## 🇬🇧 Alerting Engine & Webhooks

The Hub includes a background alerting engine that evaluates rules on each metric tick:
- **CPU Threshold Alert:** Breaches when CPU utilization exceeds configurable threshold (default `90%`).
- **Memory Threshold Alert:** Breaches when RAM utilization exceeds threshold (default `90%`).
- **Disk Threshold Alert:** Breaches when Disk usage exceeds threshold (default `90%`).
- **Offline Node Alert:** Fires when an agent has been silent for $> 30\text{s}$.
- **Flapping Protection:** 10-minute cooldown window prevents repeated spam for the same condition.
- **Auto-Resolution:** Emits a green **RESOLVED** notification when conditions clear.

Configure webhooks directly in the dashboard via the **Bell Icon** in the top navigation bar.

---

## 🇬🇧 API Reference

### Telemetry & Ingestion
| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/metrics` | Ingests a `TelemetryReport` payload from an agent. |
| `GET` | `/api/v1/nodes` | Lists all active and registered nodes with current health metrics. |
| `GET` | `/api/v1/nodes/{id}` | Fetches full telemetry snapshot for a specific node. |
| `GET` | `/api/v1/nodes/{id}/history?window=1h` | Returns time-series metric points for charting. |
| `GET` | `/api/v1/events` | Server-Sent Events (SSE) stream pushing updates on each node change. |

### Remote Actions Dispatcher
| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/v1/nodes/{id}/actions` | Enqueues a whitelisted action request for a node. |
| `GET` | `/api/v1/nodes/{id}/actions/pending` | Agent polls for queued actions. |
| `POST` | `/api/v1/nodes/{id}/actions/{action_id}/result` | Agent reports stdout, stderr, exit code, and duration. |
| `GET` | `/api/v1/nodes/{id}/actions/{action_id}` | Polls action execution status and terminal output. |
| `GET` | `/api/v1/nodes/{id}/actions` | Retrieves recent action history for a node. |

### Alerting & Webhooks
| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/v1/alerts/config` | Retrieves current webhook URL and metric thresholds. |
| `POST` | `/api/v1/alerts/config` | Updates alert rules and webhook destination. |
| `POST` | `/api/v1/alerts/test` | Dispatches an immediate test notification to verify delivery. |

---

## 🇬🇧 Agent Configuration Reference

All settings can be provided as CLI flags or environment variables:

| Flag | Env Var | Default | Description |
|---|---|---|---|
| `--hub-url` | `HUB_URL` | `http://localhost:8080` | Base URL of the telemetry hub |
| `--auth-token` | `AUTH_TOKEN` | _(empty)_ | Bearer token for hub authentication |
| `--node-id` | `NODE_ID` | OS hostname | Stable unique identifier for the host |
| `--interval` | `INTERVAL` | `15s` | Telemetry sampling interval (minimum `1s`) |
| `--log-level` | `LOG_LEVEL` | `info` | Logging verbosity: `debug`, `info`, `warn`, `error` |
| `--request-timeout` | — | `10s` | HTTP request timeout per attempt |
| `--max-retries` | — | `5` | Exponential backoff retry attempts per tick |
| `--retry-base-delay` | — | `500ms` | Initial backoff delay before first retry |
| `--retry-max-delay` | — | `30s` | Maximum retry backoff delay cap |

---

## 🇬🇧 Building from Source

**Prerequisites:** Go 1.22+, Git

```bash
git clone https://github.com/Terabithia1572/telemetry-agent.git
cd telemetry-agent

# Build both binaries with zero CGO dependencies
make build-all

# Or build individually:
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/telemetry-agent ./cmd/agent
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/telemetry-hub ./cmd/hub
```

### Running Tests
```bash
go test -v ./...
go vet ./...
```


---

## 🇬🇧 Footprint & Resource Utilization

| Metric | Target | Actual |
|---|---|---|
| **RSS Memory** | `< 15 MB` | `~8–12 MB` |
| **CPU Usage** | `< 0.5%` idle | `< 0.1%` |
| **Binary Size** | Minimal | Single stripped binary (`~7 MB` Agent, `~11 MB` Hub with embedded UI) |
| **Dependencies** | Zero | `CGO_ENABLED=0` static binary, runs on bare Alpine or `scratch` |

---

### 🇬🇧 Author & Copyright
Developed by **Yunus İNAN**. All rights reserved.  
License: [MIT](LICENSE)
