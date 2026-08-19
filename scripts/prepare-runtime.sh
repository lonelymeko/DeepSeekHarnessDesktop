#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

TARGET_OS="${1:-$(go env GOOS)}"
TARGET_ARCH="${2:-$(go env GOARCH)}"

go run ./cmd/prepare-runtime --os "$TARGET_OS" --arch "$TARGET_ARCH" --output runtime/current
