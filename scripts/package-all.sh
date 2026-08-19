#!/usr/bin/env bash
set -euo pipefail

HOST_OS="$(go env GOOS)"
HOST_ARCH="$(go env GOARCH)"

case "$HOST_OS" in
  darwin) targets=("darwin/$HOST_ARCH") ;;
  linux) targets=("linux/$HOST_ARCH") ;;
  windows) targets=("windows/$HOST_ARCH") ;;
  *) echo "Unsupported host: $HOST_OS" >&2; exit 2 ;;
esac

for target in "${targets[@]}"; do
  ./scripts/package.sh "$target"
done

echo "Cross-OS installers are built by the GitHub Actions release matrix."
