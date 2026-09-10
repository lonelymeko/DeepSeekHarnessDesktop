package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DesktopSettings is the desktop shell's own persisted configuration. It lives
// beside the shared Harness data directory instead of inside it, so the
// upstream Harness never reads — or rewrites — desktop-only choices.
type DesktopSettings struct {
	Network NetworkSettings `json:"network"`
}

// NetworkSettings decides how the desktop shell routes outbound traffic.
type NetworkSettings struct {
	// UseSystemProxy sends both the child Harness process and the desktop
	// updater through the proxy the operating system already configures. It
	// defaults to true: a machine behind Clash, V2Ray, or a corporate proxy
	// then works without exporting HTTP_PROXY before opening the app.
	UseSystemProxy bool `json:"useSystemProxy"`
}

const (
	// desktopSettingsFilename is the document's basename inside the desktop data directory.
	desktopSettingsFilename = "settings.json"
)

// desktopSettingsOverride redirects the settings document, so tests never read
// or rewrite the real user configuration. Empty selects the platform default.
var desktopSettingsOverride string

// desktopSettingsMutex serialises read-modify-write of the document, since the
// bound Wails methods and the startup path can both reach it.
var desktopSettingsMutex sync.Mutex

// defaultDesktopSettings is what a fresh install uses: system proxy on.
func defaultDesktopSettings() DesktopSettings {
	return DesktopSettings{Network: NetworkSettings{UseSystemProxy: true}}
}

// desktopSettingsFile resolves the settings document location: a test override
// when one is installed, otherwise the desktop data directory beside the shared
// Harness home.
func desktopSettingsFile() (string, error) {
	if override := strings.TrimSpace(desktopSettingsOverride); override != "" {
		return override, nil
	}
	configHome, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user application data directory: %w", err)
	}
	return filepath.Join(configHome, desktopDataDirectoryName, desktopSettingsFilename), nil
}

// loadDesktopSettings reads the document, falling back to defaults when it does
// not exist yet. A malformed document is an error rather than a silent reset,
// because silently discarding a user's choice is worse than reporting it.
func loadDesktopSettings() (DesktopSettings, error) {
	settings := defaultDesktopSettings()
	path, err := desktopSettingsFile()
	if err != nil {
		return settings, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return settings, fmt.Errorf("read desktop settings %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return settings, nil
	}
	// Decode over the defaults so a document written by an older build, which
	// named fewer fields, keeps the documented default for every absent one.
	settings = defaultDesktopSettings()
	if err := json.Unmarshal(content, &settings); err != nil {
		return defaultDesktopSettings(), fmt.Errorf("parse desktop settings %s: %w", path, err)
	}
	return settings, nil
}

// saveDesktopSettings writes the document by renaming a sibling temporary file
// over the target, so a crash mid-write never leaves a truncated document.
func saveDesktopSettings(settings DesktopSettings) error {
	path, err := desktopSettingsFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create desktop settings directory: %w", err)
	}
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode desktop settings: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write desktop settings %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("replace desktop settings %s: %w", path, err)
	}
	return nil
}

// updateDesktopSettings applies one mutation under the settings lock and
// persists the result, returning the document the caller should now display.
func updateDesktopSettings(mutate func(*DesktopSettings)) (DesktopSettings, error) {
	desktopSettingsMutex.Lock()
	defer desktopSettingsMutex.Unlock()
	settings, err := loadDesktopSettings()
	if err != nil {
		return defaultDesktopSettings(), err
	}
	mutate(&settings)
	if err := saveDesktopSettings(settings); err != nil {
		return settings, err
	}
	return settings, nil
}

// setNetworkUseSystemProxy records the network preference and reports the
// document that is now on disk.
func setNetworkUseSystemProxy(enabled bool) (DesktopSettings, error) {
	return updateDesktopSettings(func(settings *DesktopSettings) {
		settings.Network.UseSystemProxy = enabled
	})
}
