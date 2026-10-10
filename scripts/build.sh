#!/usr/bin/env bash
# scripts/build.sh – Size-optimised release build for telemetry-agent.
#
# Usage:
#   ./scripts/build.sh                  # build for current OS/arch
#   OS=linux ARCH=amd64 ./scripts/build.sh
#   OS=linux ARCH=arm64 ./scripts/build.sh
#   OS=windows ARCH=amd64 ./scripts/build.sh
#
# Output: ./dist/telemetry-agent[-OS-ARCH][.exe]
#
# The build uses:
#   -s   strip the symbol table
#   -w   strip DWARF debug information
# These two flags together reduce the binary by 25–35 %.
# CGO is disabled to produce a fully static binary with no libc dependency.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

TARGET_OS="${OS:-$(go env GOOS)}"
TARGET_ARCH="${ARCH:-$(go env GOARCH)}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")}"

OUTPUT_NAME="telemetry-agent"
if [ "${TARGET_OS}" = "windows" ]; then
    OUTPUT_NAME="${OUTPUT_NAME}.exe"
fi

OUTPUT_PATH="${DIST_DIR}/${OUTPUT_NAME}"
# When cross-compiling, suffix with OS and arch.
if [ "${TARGET_OS}" != "$(go env GOOS)" ] || [ "${TARGET_ARCH}" != "$(go env GOARCH)" ]; then
    if [ "${TARGET_OS}" = "windows" ]; then
        OUTPUT_PATH="${DIST_DIR}/telemetry-agent-${TARGET_OS}-${TARGET_ARCH}.exe"
    else
        OUTPUT_PATH="${DIST_DIR}/telemetry-agent-${TARGET_OS}-${TARGET_ARCH}"
    fi
fi

echo "▶  Building telemetry-agent"
echo "   OS:      ${TARGET_OS}"
echo "   Arch:    ${TARGET_ARCH}"
echo "   Version: ${VERSION}"
echo "   Commit:  ${COMMIT}"
echo "   Output:  ${OUTPUT_PATH}"

GOOS="${TARGET_OS}" \
GOARCH="${TARGET_ARCH}" \
CGO_ENABLED=0 \
go build \
    -trimpath \
    -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.buildTime=${BUILD_TIME} \
        -X main.commit=${COMMIT}" \
    -o "${OUTPUT_PATH}" \
    "${ROOT_DIR}/cmd/agent"

echo ""
echo "✔  Build successful"
ls -lh "${OUTPUT_PATH}"

# Optional: print size reduction vs debug build for reference.
if command -v du &>/dev/null; then
    SIZE=$(du -sh "${OUTPUT_PATH}" | cut -f1)
    echo "   Size: ${SIZE}"
fi
