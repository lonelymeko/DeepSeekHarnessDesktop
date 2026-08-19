package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const desktopDataDirectoryName = "DeepSeekHarnessDesktop"

func sharedHarnessHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv("DSH_DESKTOP_HOME")); override != "" {
		path, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve DSH_DESKTOP_HOME: %w", err)
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return "", fmt.Errorf("create DSH_DESKTOP_HOME %s: %w", path, err)
		}
		return path, nil
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	legacyHome := filepath.Join(userHome, ".dsh")
	if runtime.GOOS == "windows" {
		if err := os.MkdirAll(legacyHome, 0o700); err != nil {
			return "", fmt.Errorf("create Harness data directory %s: %w", legacyHome, err)
		}
		return legacyHome, nil
	}

	configHome, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user application data directory: %w", err)
	}
	desktopHome := filepath.Join(configHome, desktopDataDirectoryName, "dsh")
	return migrateSharedHarnessHome(legacyHome, desktopHome)
}

func migrateSharedHarnessHome(legacyHome, desktopHome string) (string, error) {
	legacyHome = filepath.Clean(legacyHome)
	desktopHome = filepath.Clean(desktopHome)
	if legacyHome == desktopHome {
		if err := os.MkdirAll(desktopHome, 0o700); err != nil {
			return "", err
		}
		return desktopHome, nil
	}

	legacyInfo, legacyErr := os.Lstat(legacyHome)
	if legacyErr == nil && legacyInfo.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(legacyHome)
		if err != nil {
			return "", fmt.Errorf("resolve existing Harness data link %s: %w", legacyHome, err)
		}
		if err := os.MkdirAll(resolved, 0o700); err != nil {
			return "", fmt.Errorf("open linked Harness data directory %s: %w", resolved, err)
		}
		return resolved, nil
	}
	if legacyErr != nil && !os.IsNotExist(legacyErr) {
		return "", fmt.Errorf("inspect existing Harness data directory %s: %w", legacyHome, legacyErr)
	}
	if legacyErr == nil && !legacyInfo.IsDir() {
		return "", fmt.Errorf("existing Harness data path is not a directory: %s", legacyHome)
	}

	desktopInfo, desktopErr := os.Lstat(desktopHome)
	if desktopErr != nil && !os.IsNotExist(desktopErr) {
		return "", fmt.Errorf("inspect desktop Harness data directory %s: %w", desktopHome, desktopErr)
	}
	if desktopErr == nil && !desktopInfo.IsDir() {
		return "", fmt.Errorf("desktop Harness data path is not a directory: %s", desktopHome)
	}

	if legacyErr == nil && desktopErr == nil {
		return "", fmt.Errorf("both Harness data directories exist; merge them manually before starting: %s and %s", legacyHome, desktopHome)
	}
	if err := os.MkdirAll(filepath.Dir(desktopHome), 0o700); err != nil {
		return "", fmt.Errorf("create desktop data parent directory: %w", err)
	}

	movedLegacy := false
	if legacyErr == nil {
		if err := os.Rename(legacyHome, desktopHome); err != nil {
			return "", fmt.Errorf("move Harness data from %s to %s: %w", legacyHome, desktopHome, err)
		}
		movedLegacy = true
	} else if err := os.MkdirAll(desktopHome, 0o700); err != nil {
		return "", fmt.Errorf("create desktop Harness data directory %s: %w", desktopHome, err)
	}

	if err := os.Symlink(desktopHome, legacyHome); err != nil {
		if movedLegacy {
			_ = os.Rename(desktopHome, legacyHome)
		} else {
			_ = os.Remove(desktopHome)
		}
		return "", fmt.Errorf("link official Harness data path %s to %s: %w", legacyHome, desktopHome, err)
	}
	return desktopHome, nil
}
