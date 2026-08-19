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
    if [[ -e "$APP/Contents/Resources/runtime" ]]; then
      chflags -R nouchg,noschg "$APP/Contents/Resources/runtime" 2>/dev/null || true
      chmod -RN "$APP/Contents/Resources/runtime" 2>/dev/null || true
      chmod -R u+w "$APP/Contents/Resources/runtime" 2>/dev/null || true
      rm -rf "$APP/Contents/Resources/runtime"
    fi
    mkdir -p "$APP/Contents/Resources/runtime"
    tar -C "$ROOT/runtime/current" -cf - . | tar -C "$APP/Contents/Resources/runtime" -xf -
    codesign --force --deep --sign - "$APP"
    cp -R "$APP" "$STAGING/"
    ln -s /Applications "$STAGING/Applications"
    DMG="$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.dmg"
    rm -f "$DMG"
    hdiutil create -volname "DeepSeek Harness Desktop" -srcfolder "$STAGING" -ov -format UDZO "$DMG"
    ;;
  linux)
    wails build -clean -platform "$TARGET" -tags webkit2_41
    cp "$ROOT/build/bin/DeepSeekHarnessDesktop" "$STAGING/"
    cp -R "$ROOT/runtime/current" "$STAGING/runtime"
    tar -C "$STAGING" -czf "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.tar.gz" .
    ;;
  windows)
    wails build -clean -platform "$TARGET" -nsis
    cp "$ROOT/build/bin/DeepSeekHarnessDesktop.exe" "$STAGING/"
    cp -R "$ROOT/runtime/current" "$STAGING/runtime"
    installer="$ROOT/build/bin/DeepSeekHarnessDesktop-${GOARCH_TARGET}-installer.exe"
    if [[ ! -f "$installer" ]]; then
      echo "NSIS installer missing after Wails build: $installer" >&2
      echo "Ensure makensis is installed and available on PATH." >&2
      exit 1
    fi
    cp "$installer" "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}-installer.exe"
    if command -v ditto >/dev/null 2>&1; then
      ditto -c -k --sequesterRsrc --keepParent "$STAGING" "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.zip"
    elif command -v 7z >/dev/null 2>&1; then
      (cd "$STAGING" && 7z a -tzip "$OUTPUT/DeepSeekHarnessDesktop-${VERSION}-${GOOS_TARGET}-${GOARCH_TARGET}.zip" .)
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
