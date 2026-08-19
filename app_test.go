package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessProxyNormalizesWebViewAuthority(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != request.URL.Host && request.URL.Host != "" {
			t.Fatalf("unexpected URL host: %s", request.URL.Host)
		}
		_, _ = io.WriteString(writer, request.Host+"\n"+request.Header.Get("Origin"))
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyServer := httptest.NewServer(newHarnessReverseProxy(target, "linux"))
	defer proxyServer.Close()

	request, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/api/settings.describe", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "wails.localhost"
	request.Header.Set("Origin", "wails://wails.localhost")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := target.Host + "\n" + target.Scheme + "://" + target.Host
	if string(body) != want {
		t.Fatalf("normalized headers = %q, want %q", body, want)
	}
}

func TestInjectDesktopChrome(t *testing.T) {
	document := []byte("<!doctype html><body><div id=\"root\"></div></body>")
	mac := string(injectDesktopChrome(document, "darwin"))
	if !strings.Contains(mac, `id="dsh-desktop-drag-region"`) || strings.Contains(mac, `<div id="dsh-desktop-window-controls"`) {
		t.Fatalf("mac chrome was not injected correctly: %s", mac)
	}
	if !strings.Contains(mac, "padding-top:44px") {
		t.Fatalf("mac chrome does not reserve traffic-light clearance: %s", mac)
	}
	windows := string(injectDesktopChrome(document, "windows"))
	for _, expected := range []string{"dsh-desktop-drag-region", "dsh-desktop-window-controls", "WindowToggleMaximise", "window.runtime?.Quit"} {
		if !strings.Contains(windows, expected) {
			t.Fatalf("windows chrome lacks %q", expected)
		}
	}
}

func TestMigrateSharedHarnessHomeMovesDataAndCreatesCompatibilityLink(t *testing.T) {
	root := t.TempDir()
	legacyHome := filepath.Join(root, ".dsh")
	desktopHome := filepath.Join(root, "app-data", "dsh")
	if err := os.MkdirAll(filepath.Join(legacyHome, "sessions", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("existing session memory")
	dataFile := filepath.Join(legacyHome, "sessions", "project", "session.json")
	if err := os.WriteFile(dataFile, want, 0o600); err != nil {
		t.Fatal(err)
	}

	gotHome, err := migrateSharedHarnessHome(legacyHome, desktopHome)
	if err != nil {
		t.Fatal(err)
	}
	if gotHome != desktopHome {
		t.Fatalf("shared home = %q, want %q", gotHome, desktopHome)
	}
	legacyInfo, err := os.Lstat(legacyHome)
	if err != nil {
		t.Fatal(err)
	}
	if legacyInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("legacy home is not a compatibility symlink: %s", legacyHome)
	}
	got, err := os.ReadFile(filepath.Join(desktopHome, "sessions", "project", "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("migrated data = %q, want %q", got, want)
	}
}

func TestMigrateSharedHarnessHomeRefusesConflictingDirectories(t *testing.T) {
	root := t.TempDir()
	legacyHome := filepath.Join(root, ".dsh")
	desktopHome := filepath.Join(root, "app-data", "dsh")
	for _, path := range []string{legacyHome, desktopHome} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	_, err := migrateSharedHarnessHome(legacyHome, desktopHome)
	if err == nil || !strings.Contains(err.Error(), "both Harness data directories exist") {
		t.Fatalf("conflict error = %v", err)
	}
}
