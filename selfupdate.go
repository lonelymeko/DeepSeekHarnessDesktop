package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Self-update: replace the running application in place, so the user never
// drags an icon or runs an installer.
//
// A process cannot overwrite its own executable, and on macOS it cannot
// reliably rewrite the bundle it is running from either. Every platform is
// therefore handed to a detached helper that waits for this process to exit,
// swaps the payload, and relaunches:
//
//   - macOS: `ditto` extracts the new .app from the mounted DMG over the old
//     bundle. The old bundle is moved aside first and restored if the swap
//     fails, so a failed update leaves a working application rather than none.
//   - Windows: the NSIS installer is run silently, which is what it is for; it
//     closes and replaces the installed application itself.
//   - Linux: the tar.gz is unpacked over the directory the running binary lives
//     in, with the same move-aside-and-restore guard as macOS.
//
// The helper is written to a temporary script and run detached, because it must
// outlive this process. Its log is kept beside the update cache so a failed
// silent swap can be diagnosed after the fact.

// updateRelocationSuffix marks the directory a running application is moved to
// while its replacement is written.
const updateRelocationSuffix = ".dsh-previous"

// selfUpdateSupported reports whether this platform can replace the running
// application without user interaction.
func selfUpdateSupported() bool {
	switch runtime.GOOS {
	case "darwin", "windows", "linux":
		return true
	default:
		return false
	}
}

// applyDownloadedUpdate starts the detached helper that performs the swap and
// relaunches, and returns once the helper is running.
//
// It deliberately does not wait: the helper's first act is to wait for this
// process to exit, so blocking here would deadlock the update.
func applyDownloadedUpdate(downloaded string) error {
	if !selfUpdateSupported() {
		return fmt.Errorf("in-place updates are unavailable on %s", runtime.GOOS)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running application: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve the running application: %w", err)
	}

	target, err := updateTargetPath(executable)
	if err != nil {
		return err
	}

	cacheRoot, err := os.UserCacheDir()
	if err != nil || cacheRoot == "" {
		cacheRoot = os.TempDir()
	}
	logRoot := filepath.Join(cacheRoot, desktopDataDirectoryName, "updates")
	if err := os.MkdirAll(logRoot, 0o700); err != nil {
		return fmt.Errorf("create update log directory: %w", err)
	}
	logPath := filepath.Join(logRoot, "self-update.log")

	script, err := updateHelperScript(downloaded, target, executable)
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(logRoot, fmt.Sprintf("self-update-%d.sh", time.Now().UnixNano()))
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return fmt.Errorf("write update helper: %w", err)
	}

	command := exec.Command("/bin/sh", scriptPath)
	command.Env = append(os.Environ(), "DSH_UPDATE_LOG="+logPath)
	command.Stdout = nil
	command.Stderr = nil
	// A new session detaches the helper from this process group, so quitting —
	// or being killed — does not take the swap down with it.
	command.SysProcAttr = detachedProcessAttributes()
	if err := command.Start(); err != nil {
		return fmt.Errorf("start the update helper: %w", err)
	}
	log.Printf("DeepSeek Harness Desktop self-update: helper %d will replace %s", command.Process.Pid, target)
	// The caller quits the application; the helper is already waiting.
	return nil
}

// updateTargetPath resolves what the helper must replace.
//
// On macOS the whole bundle is replaced, not the executable inside it, because
// that is the unit the user launches and the one carrying the signature.
func updateTargetPath(executable string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		// …/Foo.app/Contents/MacOS/Foo -> …/Foo.app
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(executable)))
		if !strings.HasSuffix(bundle, ".app") {
			return "", fmt.Errorf("the running binary is not inside an application bundle: %s", executable)
		}
		if !withinWritableLocation(bundle) {
			return "", fmt.Errorf("%s is not writable; move the application out of a read-only location such as the DMG", bundle)
		}
		return bundle, nil
	case "windows", "linux":
		return filepath.Dir(executable), nil
	default:
		return "", fmt.Errorf("in-place updates are unavailable on %s", runtime.GOOS)
	}
}

// withinWritableLocation reports whether the bundle's parent directory can be
// written to, which is what the move-aside swap needs.
func withinWritableLocation(bundle string) bool {
	parent := filepath.Dir(bundle)
	probe, err := os.CreateTemp(parent, ".dsh-write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

// updateHelperScript builds the /bin/sh program that performs the swap.
//
// Every platform follows the same shape: wait for exit, stage the new payload
// next to the old one, move the old aside, install the new, restore on failure,
// then relaunch a fresh copy and clean up.
func updateHelperScript(downloaded, target, executable string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return darwinUpdateHelper(downloaded, target, executable), nil
	case "windows":
		return windowsUpdateHelper(downloaded), nil
	case "linux":
		return linuxUpdateHelper(downloaded, target, executable), nil
	default:
		return "", fmt.Errorf("in-place updates are unavailable on %s", runtime.GOOS)
	}
}

// darwinUpdateHelper mounts the downloaded DMG, copies the application out of
// it over the installed one, and relaunches.
func darwinUpdateHelper(downloaded, target, executable string) string {
	return fmt.Sprintf(`#!/bin/sh
# Wait for the application to exit: the bundle cannot be replaced underneath a
# process that is still running out of it.
pid=%d
while kill -0 "$pid" 2>/dev/null; do sleep 1; done

log() { echo "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) $*" >> "$DSH_UPDATE_LOG"; }
log "starting: %s -> %s"

mount=$(mktemp -d /tmp/dsh-update-mount.XXXXXX)
if ! hdiutil attach -nobrowse -quiet -mountpoint "$mount" %s; then
  log "failed to mount the update image"
  rm -rf "$mount"
  open %s
  exit 1
fi

source_app=$(ls -d "$mount"/*.app 2>/dev/null | head -1)
if [ -z "$source_app" ]; then
  log "the mounted image contains no application"
  hdiutil detach "$mount" -quiet
  rm -rf "$mount"
  open %s
  exit 1
fi

previous="%s%s"
rm -rf "$previous"
if ! mv "%s" "$previous"; then
  log "could not move the installed application aside"
  hdiutil detach "$mount" -quiet
  rm -rf "$mount"
  open %s
  exit 1
fi

if ditto "$source_app" "%s"; then
  log "installed the new version"
  rm -rf "$previous"
else
  log "install failed; restoring the previous version"
  rm -rf "%s"
  mv "$previous" "%s"
  hdiutil detach "$mount" -quiet
  rm -rf "$mount"
  open %s
  exit 1
fi

hdiutil detach "$mount" -quiet
rm -rf "$mount"
log "relaunching"
open "%s"
rm -f "$0"
`,
		os.Getpid(),
		shellQuote(downloaded), shellQuote(target),
		shellQuote(downloaded),
		shellQuote(target),
		shellQuote(target),
		shellQuote(target), updateRelocationSuffix,
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
	)
}

// linuxUpdateHelper unpacks the release archive over the installation
// directory, moving the old tree aside first so a failure can be undone.
func linuxUpdateHelper(downloaded, target, executable string) string {
	return fmt.Sprintf(`#!/bin/sh
pid=%d
while kill -0 "$pid" 2>/dev/null; do sleep 1; done

log() { echo "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) $*" >> "$DSH_UPDATE_LOG"; }
log "starting: %s -> %s"

staging=$(mktemp -d /tmp/dsh-update.XXXXXX)
if ! tar -xzf %s -C "$staging"; then
  log "failed to unpack the update archive"
  rm -rf "$staging"
  exit 1
fi

payload="$staging"
if [ ! -f "$payload/DeepSeekHarnessDesktop" ]; then
  inner=$(ls -d "$staging"/*/ 2>/dev/null | head -1)
  if [ -n "$inner" ] && [ -f "$inner/DeepSeekHarnessDesktop" ]; then
    payload="$inner"
  else
    log "the archive contains no application binary"
    rm -rf "$staging"
    exit 1
  fi
fi

previous="%s%s"
rm -rf "$previous"
if ! mv "%s" "$previous"; then
  log "could not move the installation aside"
  rm -rf "$staging"
  exit 1
fi

if cp -a "$payload/." "%s/"; then
  log "installed the new version"
  rm -rf "$previous" "$staging"
else
  log "install failed; restoring the previous version"
  rm -rf "%s"
  mv "$previous" "%s"
  rm -rf "$staging"
  exit 1
fi

log "relaunching"
"%s" >/dev/null 2>&1 &
rm -f "$0"
`,
		os.Getpid(),
		shellQuote(downloaded), shellQuote(target),
		shellQuote(downloaded),
		shellQuote(target), updateRelocationSuffix,
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
		shellQuote(target),
		shellQuote(executable),
	)
}

// windowsUpdateHelper runs the NSIS installer silently. The installer owns
// closing and replacing the application, so the helper only waits and launches
// it; `/S` is NSIS's silent switch.
func windowsUpdateHelper(downloaded string) string {
	return fmt.Sprintf(`#!/bin/sh
pid=%d
while kill -0 "$pid" 2>/dev/null; do sleep 1; done
# Give the application a moment to release its files.
sleep 2
"%s" /S
rm -f "$0"
`,
		os.Getpid(),
		downloaded,
	)
}

// shellQuote wraps a value in single quotes for /bin/sh, escaping embedded
// quotes, so a path containing spaces or quotes cannot break out of its
// argument.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// tarGzRoot returns the single top-level directory an archive wraps its payload
// in, or "" when the archive holds its files at the root.
func tarGzRoot(archive string) (string, error) {
	file, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(header.Name), "./"), "/")
		if len(parts) > 1 && parts[0] != "" {
			return parts[0], nil
		}
		return "", nil
	}
}
