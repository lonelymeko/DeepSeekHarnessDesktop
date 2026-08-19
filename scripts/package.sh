#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

TARGET="${1:-$(go env GOOS)/$(go env GOARCH)}"
VERSION="${VERSION:-0.1.0}"
GOOS_TARGET="${TARGET%/*}"
GOARCH_TARGET="${TARGET#*/}"
OUTPUT="$ROOT/dist-packages"
STAGING="$ROOT/.cache/package/${GOOS_TARGET}-${GOARCH_TARGET}"

rm -rf "$STAGING"
mkdir -p "$OUTPUT" "$STAGING"
./scripts/prepare-runtime.sh "$GOOS_TARGET" "$GOARCH_TARGET"

case "$GOOS_TARGET" in
  darwin)
    wails build -clean -platform "$TARGET"
    APP="$ROOT/build/bin/DeepSeekHarnessDesktop.app"
    mkdir -p "$APP/Contents/Resources"
    rm -rf "$APP/Contents/Resources/runtime"
    cp -R "$ROOT/runtime/current" "$APP/Contents/Resources/runtime"
    codesign --force --deep --sign - "$APP"
    cp -R "$APP" "$STAGING/"
    ln -s /Applications "$STAGING/Applications"
    DMG="$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.dmg"
    rm -f "$DMG"
    hdiutil create -volname "DeepSeek Harness Desktop" -srcfolder "$STAGING" -ov -format UDZO "$DMG"
    ;;
  linux)
    wails build -clean -platform "$TARGET"
    cp "$ROOT/build/bin/DeepSeekHarnessDesktop" "$STAGING/"
    cp -R "$ROOT/runtime/current" "$STAGING/runtime"
    tar -C "$STAGING" -czf "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.tar.gz" .
    ;;
  windows)
    wails build -clean -platform "$TARGET" -nsis
    cp "$ROOT/build/bin/DeepSeekHarnessDesktop.exe" "$STAGING/"
    cp -R "$ROOT/runtime/current" "$STAGING/runtime"
    installer="$ROOT/build/bin/DeepSeekHarnessDesktop-${GOARCH_TARGET}-installer.exe"
    if [[ -f "$installer" ]]; then
      cp "$installer" "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}-installer.exe"
    fi
    if command -v ditto >/dev/null 2>&1; then
      ditto -c -k --sequesterRsrc --keepParent "$STAGING" "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.zip"
    else
      (cd "$STAGING" && zip -qr "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.zip" .)
    fi
    ;;
  *)
    echo "Unsupported target: $TARGET" >&2
    exit 2
    ;;
esac

echo "Created packages in $OUTPUT"
