# Makefile for telemetry-agent
# Compatible with GNU Make 3.82+ on Linux/macOS/WSL.
# On Windows (native PowerShell), use the build.sh script via Git Bash or WSL.

# ── Variables ─────────────────────────────────────────────────────────────────
MODULE        := github.com/telemetry-agent
CMD_PKG       := ./cmd/agent
DIST_DIR      := ./dist
BINARY        := $(DIST_DIR)/telemetry-agent
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT        ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# ld flags: strip symbol table + DWARF, inject version metadata.
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.buildTime=$(BUILD_TIME) \
	-X main.commit=$(COMMIT)

GO         := go
GOTEST     := $(GO) test
GOBUILD    := CGO_ENABLED=0 $(GO) build
GOVET      := $(GO) vet

# Default target.
.DEFAULT_GOAL := help

# ── Targets ───────────────────────────────────────────────────────────────────

.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: ## Download and tidy module dependencies
	$(GO) mod download
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: build
build: $(DIST_DIR) ## Build the release binary (stripped, statically linked)
	$(GOBUILD) \
		-trimpath \
		-ldflags="$(LDFLAGS)" \
		-o $(BINARY) \
		$(CMD_PKG)
	@echo "Built: $(BINARY)"
	@ls -lh $(BINARY)

.PHONY: build-all
build-all: $(DIST_DIR) ## Cross-compile for linux/amd64, linux/arm64, and windows/amd64
	GOOS=linux  GOARCH=amd64 $(GOBUILD) -trimpath -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/telemetry-agent-linux-amd64  $(CMD_PKG)
	GOOS=linux  GOARCH=arm64 $(GOBUILD) -trimpath -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/telemetry-agent-linux-arm64  $(CMD_PKG)
	GOOS=windows GOARCH=amd64 $(GOBUILD) -trimpath -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/telemetry-agent-windows-amd64.exe $(CMD_PKG)
	@echo "Cross-compiled binaries:"
	@ls -lh $(DIST_DIR)/

.PHONY: run
run: ## Run the agent locally (requires HUB_URL or --hub-url)
	$(GO) run $(CMD_PKG) \
		--hub-url=$${HUB_URL:-http://localhost:8080} \
		--interval=$${INTERVAL:-15s} \
		--log-level=$${LOG_LEVEL:-debug}

.PHONY: test
test: ## Run all unit tests with race detector
	$(GOTEST) -v -race -count=1 ./...

.PHONY: test-short
test-short: ## Run only short tests (skips integration / network tests)
	$(GOTEST) -v -short -count=1 ./...

.PHONY: vet
vet: ## Run go vet across all packages
	$(GOVET) ./...

.PHONY: fmt
fmt: ## Format all Go source files with gofmt
	gofmt -l -w .

.PHONY: lint
lint: ## Run golangci-lint (must be installed separately)
	golangci-lint run ./...

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf $(DIST_DIR)

$(DIST_DIR):
	mkdir -p $(DIST_DIR)
