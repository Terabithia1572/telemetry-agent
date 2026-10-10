#!/usr/bin/env bash
# scripts/build.sh – Size-optimised release build for telemetry-agent & telemetry-hub.
#
# Usage:
#   ./scripts/build.sh                  # build both agent and hub for current OS/arch
#   TARGET=agent ./scripts/build.sh     # build agent only
#   TARGET=hub ./scripts/build.sh       # build hub only
#   OS=linux ARCH=amd64 ./scripts/build.sh
#   OS=windows ARCH=amd64 ./scripts/build.sh
#
# Output: ./dist/telemetry-(agent|hub)[-OS-ARCH][.exe]
#
# The build uses:
#   -s   strip the symbol table
#   -w   strip DWARF debug information
# CGO is disabled (CGO_ENABLED=0) to produce fully static binaries.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

TARGET="${TARGET:-all}"
TARGET_OS="${OS:-$(go env GOOS)}"
TARGET_ARCH="${ARCH:-$(go env GOARCH)}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"

build_binary() {
    local bin_name="$1"
    local pkg_path="$2"
    local output_name="${bin_name}"
    
    if [ "${TARGET_OS}" = "windows" ]; then
        output_name="${output_name}.exe"
    fi

    local output_path="${DIST_DIR}/${output_name}"
    if [ "${TARGET_OS}" != "$(go env GOOS)" ] || [ "${TARGET_ARCH}" != "$(go env GOARCH)" ]; then
        if [ "${TARGET_OS}" = "windows" ]; then
            output_path="${DIST_DIR}/${bin_name}-${TARGET_OS}-${TARGET_ARCH}.exe"
        else
            output_path="${DIST_DIR}/${bin_name}-${TARGET_OS}-${TARGET_ARCH}"
        fi
    fi

    echo "▶  Building ${bin_name} (${TARGET_OS}/${TARGET_ARCH}, version: ${VERSION})"
    GOOS="${TARGET_OS}" \
    GOARCH="${TARGET_ARCH}" \
    CGO_ENABLED=0 \
    go build \
        -trimpath \
        -ldflags="-s -w \
            -X main.version=${VERSION} \
            -X main.buildTime=${BUILD_TIME} \
            -X main.commit=${COMMIT}" \
        -o "${output_path}" \
        "${pkg_path}"

    echo "✔  Built: ${output_path}"
}

if [ "${TARGET}" = "all" ] || [ "${TARGET}" = "agent" ]; then
    build_binary "telemetry-agent" "${ROOT_DIR}/cmd/agent"
fi

if [ "${TARGET}" = "all" ] || [ "${TARGET}" = "hub" ]; then
    build_binary "telemetry-hub" "${ROOT_DIR}/cmd/hub"
fi

echo ""
echo "✔  Build complete in ${DIST_DIR}"
ls -lh "${DIST_DIR}"
