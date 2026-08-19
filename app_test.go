package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
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
	proxyServer := httptest.NewServer(newHarnessReverseProxy(target, "linux", "ws://127.0.0.1:45678"))
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

func TestHarnessWebSocketBridgeNormalizesWebViewHandshake(t *testing.T) {
	type observedRequest struct {
		host    string
		origin  string
		site    string
		mode    string
		dest    string
		upgrade string
	}
	observed := make(chan observedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observed <- observedRequest{
			host:    request.Host,
			origin:  request.Header.Get("Origin"),
			site:    request.Header.Get("Sec-Fetch-Site"),
			mode:    request.Header.Get("Sec-Fetch-Mode"),
			dest:    request.Header.Get("Sec-Fetch-Dest"),
			upgrade: request.Header.Get("Upgrade"),
		}
		connection, buffer, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprint(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = buffer.Flush()
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	bridge := httptest.NewServer(newHarnessWebSocketBridge(target))
	defer bridge.Close()
	bridgeURL, err := url.Parse(bridge.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", bridgeURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET /api/events.mux HTTP/1.1\r\nHost: wails\r\nOrigin: wails://wails\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-Fetch-Site: cross-site\r\nSec-Fetch-Mode: websocket\r\nSec-Fetch-Dest: websocket\r\n\r\n")
	status, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if status != "HTTP/1.1 101 Switching Protocols\r\n" {
		t.Fatalf("bridge status = %q", status)
	}
	got := <-observed
	if got.host != target.Host || got.origin != target.Scheme+"://"+target.Host {
		t.Fatalf("bridge authority = host %q origin %q", got.host, got.origin)
	}
	if got.site != "same-origin" || got.mode != "websocket" || got.dest != "websocket" || !strings.EqualFold(got.upgrade, "websocket") {
		t.Fatalf("bridge fetch metadata = %+v", got)
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

func TestInjectDesktopSessionRestore(t *testing.T) {
	document := []byte("<!doctype html><body><div id=\"root\"></div></body>")
	result := string(injectDesktopSessionRestore(document, "ws://127.0.0.1:45678"))
	for _, expected := range []string{
		`id="dsh-desktop-session-restore"`,
		`const bridgeBase = "ws://127.0.0.1:45678"`,
		`url.protocol === "wails:"`,
		`url.pathname.startsWith("/api/")`,
		`dsh.sessions.current`,
		`method:"session.list"`,
		`item.blank !== true`,
		`localStorage.setItem`,
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("desktop session restore lacks %q: %s", expected, result)
		}
	}
	if strings.Index(result, `dsh-desktop-session-restore`) > strings.Index(result, `</body>`) {
		t.Fatalf("desktop session restore must run before closing body: %s", result)
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
