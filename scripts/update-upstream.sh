#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mkdir -p .cache/bin
go build -o .cache/bin/upstream-sync ./cmd/upstream-sync
exec .cache/bin/upstream-sync "$@"
