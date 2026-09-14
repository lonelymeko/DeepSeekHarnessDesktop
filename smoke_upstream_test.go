//go:build smoke

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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// smokeRuntimePath is the runtime `make prepare` writes. The test runs from the
// repository root, so a relative path is the prepared runtime of this checkout.
const smokeRuntimePath = "runtime/current"

// TestUpstreamSmoke boots the real upstream Harness from the prepared runtime
// and drives this shell's own launch, session-exchange, reverse-proxy and
// WebSocket-bridge path against it. It is an adaptation gate, not a unit test,
// which is why it sits behind the `smoke` build tag: `go test ./...` never
// compiles it, and `make smoke` runs it deliberately after an upstream update
// and before publishing a release built on one.
func TestUpstreamSmoke(t *testing.T) {
	root, err := filepath.Abs(smokeRuntimePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root + "/node/bin/node"); err != nil {
		t.Skipf("no prepared runtime at %s; run make prepare first", root)
	}
	home := smokeHarnessHome(t, root)
	t.Logf("runtime %s, smoke harness home %s", root, home)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	output := newHarnessReadyWriter(os.Stdout, target)
	command := exec.Command(root+"/node/bin/node", root+"/app/node_modules/@deepseek-ai/dsh/lib/bin.js",
		"web", "--no-open", "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	command.Dir = root + "/app"
	command.Env = append(os.Environ(), "DSH_DESKTOP=1", "DSH_HOME="+home)
	command.Stdout = output
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()

	var authenticated *url.URL
	select {
	case authenticated = <-output.ready:
	case <-time.After(300 * time.Second):
		t.Fatal("timed out waiting for the `dsh web: <url>` handoff line")
	}
	t.Logf("handoff line parsed: %s", authenticated.Redacted())

	cookie, err := exchangeHarnessBrowserSession(authenticated)
	if err != nil {
		t.Fatalf("token exchange: %v", err)
	}
	t.Logf("session cookie negotiated: %s", strings.SplitN(cookie, "=", 2)[0])

	proxyServer := httptest.NewServer(newHarnessReverseProxy(target, "darwin", "ws://127.0.0.1:45678", cookie))
	defer proxyServer.Close()

	request, err := http.NewRequest(http.MethodGet, proxyServer.URL+"/", nil)
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
	document := string(body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / through the desktop proxy = %s", response.Status)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET / content type = %q", response.Header.Get("Content-Type"))
	}
	if !strings.Contains(document, "</body>") {
		t.Fatal("the upstream document no longer closes its body, so injection cannot land")
	}
	for _, injection := range []string{
		`id="dsh-desktop-session-restore"`,
		`globalThis.__DSH_TRANSPORT__`,
		`{ownsHost:true}`,
		`id="dsh-desktop-updater"`,
		`id="dsh-desktop-settings"`,
		`id="dsh-desktop-titlebar"`,
	} {
		if !strings.Contains(document, injection) {
			t.Errorf("injected document lacks %q", injection)
		}
	}
	t.Logf("document served through the proxy: %d bytes", len(body))

	// The Harness's own API must answer through the same proxy with the
	// negotiated session cookie, which is what the desktop WebView relies on.
	apiRequest, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/api/session/list", strings.NewReader(
		`{"type":"client-request","rpcId":"smoke","method":"session/list","payload":{"args":{"_request":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	apiRequest.Host = "wails.localhost"
	apiRequest.Header.Set("Content-Type", "application/json")
	apiResponse, err := http.DefaultClient.Do(apiRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer apiResponse.Body.Close()
	apiBody, _ := io.ReadAll(apiResponse.Body)
	if apiResponse.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/session/list through the desktop proxy = %s: %s", apiResponse.Status, truncateText(string(apiBody), 300))
	}
	t.Logf("session/list answered: %s", truncateText(string(apiBody), 200))

	// A chunked request body carries no Content-Length, which is the shape a
	// raw Blob/ReadableStream upload takes in this upstream. Session listing is
	// a route that always answers, so forwarding it chunked proves the desktop
	// proxy streams an unknown-length body instead of buffering or refusing it.
	chunkedList, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/api/session/list",
		chunkedReader(`{"type":"client-request","rpcId":"smoke-chunked","method":"session/list","payload":{"args":{"_request":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	chunkedList.Host = "wails.localhost"
	chunkedList.Header.Set("Content-Type", "application/json")
	chunkedListResponse, err := http.DefaultClient.Do(chunkedList)
	if err != nil {
		t.Fatalf("chunked POST /api/session/list through the desktop proxy: %v", err)
	}
	defer chunkedListResponse.Body.Close()
	chunkedListBody, _ := io.ReadAll(chunkedListResponse.Body)
	if chunkedListResponse.StatusCode != http.StatusOK {
		t.Fatalf("chunked POST /api/session/list = %s: %s", chunkedListResponse.Status, truncateText(string(chunkedListBody), 300))
	}
	if !strings.Contains(string(chunkedListBody), `"ok":true`) {
		t.Fatalf("chunked POST /api/session/list did not answer OK: %s", truncateText(string(chunkedListBody), 300))
	}
	t.Logf("chunked request body forwarded: %s", truncateText(string(chunkedListBody), 120))

	// The new streaming upload route answers a malformed body by closing the
	// socket, directly as well as through the proxy. Both are attempted so the
	// comparison is meaningful: the proxy must never turn a direct success into
	// a failure, while an identical rejection is the Harness's own answer to a
	// body this test does not know how to build.
	directRequest, err := http.NewRequest(http.MethodPost, target.String()+"/api/session/uploadFileBinary",
		chunkedReader("--dsh-smoke\r\npayload\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	directRequest.Header.Set("Content-Type", "application/octet-stream")
	directRequest.Header.Set("Cookie", cookie)
	directResponse, directErr := http.DefaultClient.Do(directRequest)
	directStatus := 0
	if directErr == nil {
		body, _ := io.ReadAll(directResponse.Body)
		_ = directResponse.Body.Close()
		directStatus = directResponse.StatusCode
		t.Logf("direct streaming upload: %s %s", directResponse.Status, truncateText(string(body), 120))
	} else {
		t.Logf("direct streaming upload rejected by the Harness: %v", directErr)
	}

	uploadRequest, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/api/session/uploadFileBinary",
		chunkedReader("--dsh-smoke\r\npayload\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	uploadRequest.Host = "wails.localhost"
	uploadRequest.Header.Set("Content-Type", "application/octet-stream")
	uploadResponse, err := http.DefaultClient.Do(uploadRequest)
	if err != nil {
		t.Fatalf("chunked upload through the desktop proxy: %v", err)
	}
	defer uploadResponse.Body.Close()
	uploadBody, _ := io.ReadAll(uploadResponse.Body)
	t.Logf("proxied streaming upload: %s %s", uploadResponse.Status, truncateText(string(uploadBody), 120))
	if directStatus >= 200 && directStatus < 300 {
		if uploadResponse.StatusCode != directStatus {
			t.Fatalf("the desktop proxy turned a direct %d into %s", directStatus, uploadResponse.Status)
		}
	}

	// The desktop shell bridges the Harness's Gateway WebSocket on a listener of
	// its own, because the WebView's origin cannot reach the loopback socket and
	// cannot satisfy the same-origin fence. Raw frames are used rather than a
	// WebSocket client: a 101 is the entire claim under test.
	bridgeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bridge := &http.Server{Handler: newHarnessWebSocketBridge(target, cookie)}
	go func() { _ = bridge.Serve(bridgeListener) }()
	defer func() { _ = bridge.Close() }()

	connection, err := net.DialTimeout("tcp", bridgeListener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	upgrade := fmt.Sprintf("GET %s HTTP/1.1\r\n"+
		"Host: 127.0.0.1:%d\r\n"+
		"Origin: http://127.0.0.1:%d\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n"+
		"Cookie: %s\r\n\r\n", gatewayWebSocketPath, port, port, cookie)
	if _, err := io.WriteString(connection, upgrade); err != nil {
		t.Fatal(err)
	}
	statusLine, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the bridged WebSocket handshake: %v", err)
	}
	statusLine = strings.TrimSpace(statusLine)
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("the desktop WebSocket bridge answered %q, want a 101 upgrade", statusLine)
	}
	t.Logf("WebSocket bridge upgraded: %s", statusLine)
}

// gatewayWebSocketPath is the Harness's Gateway stream mux, the one WebSocket
// the desktop bridge forwards. It is asserted against the runtime rather than
// assumed, because the bridge only rewrites paths under `/api/`.
const gatewayWebSocketPath = "/api/remote.mux"

// smokeHarnessHome gives the smoke run a Harness home of its own, seeded once
// from the shared one. Booting the shared home directly would rewrite the
// operator's real profile on every run, and booting an empty home would install
// the whole plugin set from the network each time. The seed is cheap because a
// profile's `node_modules` is a directory of links into the installed runtime.
func smokeHarnessHome(t *testing.T, runtimeRoot string) string {
	t.Helper()
	home := filepath.Join(runtimeRoot, "smoke-home")
	if _, err := os.Stat(filepath.Join(home, "profiles", "web", "package.json")); err == nil {
		return home
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	shared, err := sharedHarnessHome()
	if err != nil {
		t.Logf("no shared Harness home to seed from (%v); the smoke profile boots cold", err)
		return home
	}
	profile := filepath.Join(shared, "profiles")
	if _, err := os.Stat(profile); err != nil {
		t.Logf("no shared Harness profile at %s; the smoke profile boots cold", profile)
		return home
	}
	t.Logf("seeding the smoke Harness home from %s", profile)
	if output, err := exec.Command("cp", "-a", profile, filepath.Join(home, "profiles")).CombinedOutput(); err != nil {
		t.Fatalf("seed the smoke Harness home from %s: %v: %s", profile, err, output)
	}
	return home
}

// chunkedReader hands back a body Go cannot measure, so the request is sent
// with chunked transfer encoding exactly as a streamed upload would be.
func chunkedReader(payload string) io.Reader {
	half := len(payload) / 2
	return io.NopCloser(io.MultiReader(strings.NewReader(payload[:half]), strings.NewReader(payload[half:])))
}
