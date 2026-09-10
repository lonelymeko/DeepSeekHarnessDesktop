package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateDesktopSettings points the settings document at a temporary file, so a
// test never reads or rewrites the real user configuration.
func isolateDesktopSettings(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	previous := desktopSettingsOverride
	desktopSettingsOverride = path
	t.Cleanup(func() { desktopSettingsOverride = previous })
	return path
}

func TestDesktopSettingsDefaultToSystemProxy(t *testing.T) {
	isolateDesktopSettings(t)
	settings, err := loadDesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Network.UseSystemProxy {
		t.Fatal("a fresh install must default to routing through the system proxy")
	}
}

func TestDesktopSettingsRoundTrip(t *testing.T) {
	path := isolateDesktopSettings(t)
	saved, err := setNetworkUseSystemProxy(false)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Network.UseSystemProxy {
		t.Fatalf("setNetworkUseSystemProxy(false) returned %+v", saved)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"useSystemProxy": false`) {
		t.Fatalf("persisted document = %s", content)
	}
	reloaded, err := loadDesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Network.UseSystemProxy {
		t.Fatalf("reloaded settings = %+v", reloaded)
	}
}

func TestDesktopSettingsKeepDefaultsForAbsentFields(t *testing.T) {
	path := isolateDesktopSettings(t)
	// A document written by an older build names fewer fields; every absent one
	// must keep its documented default rather than decay to the zero value.
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := loadDesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Network.UseSystemProxy {
		t.Fatalf("an empty document must keep the system-proxy default: %+v", settings)
	}
}

func TestDesktopSettingsTreatEmptyDocumentAsDefault(t *testing.T) {
	path := isolateDesktopSettings(t)
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := loadDesktopSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Network.UseSystemProxy {
		t.Fatalf("a blank document must keep the system-proxy default: %+v", settings)
	}
}

func TestDesktopSettingsRejectMalformedDocument(t *testing.T) {
	path := isolateDesktopSettings(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadDesktopSettings(); err == nil {
		t.Fatal("a malformed document must be reported, not silently reset")
	}
	if _, err := setNetworkUseSystemProxy(true); err == nil {
		t.Fatal("a malformed document must also fail the write path")
	}
}

func TestDesktopSettingsFileHonoursOverride(t *testing.T) {
	path := isolateDesktopSettings(t)
	resolved, err := desktopSettingsFile()
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf("desktopSettingsFile() = %q, want %q", resolved, path)
	}
}

func TestDesktopSettingsFileDefaultsBesideTheHarnessHome(t *testing.T) {
	previous := desktopSettingsOverride
	desktopSettingsOverride = ""
	t.Cleanup(func() { desktopSettingsOverride = previous })

	configHome, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user configuration directory on this platform")
	}
	resolved, err := desktopSettingsFile()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configHome, desktopDataDirectoryName, desktopSettingsFilename)
	if resolved != want {
		t.Fatalf("desktopSettingsFile() = %q, want %q", resolved, want)
	}
}
