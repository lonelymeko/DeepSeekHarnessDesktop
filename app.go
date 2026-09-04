package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type App struct {
	ctx      context.Context
	command  *exec.Cmd
	proxy    *HarnessProxy
	logFile  *os.File
	bridge   *http.Server
	stopOnce sync.Once
}

type harnessReadyWriter struct {
	delegate io.Writer
	target   *url.URL
	ready    chan *url.URL
	mu       sync.Mutex
	pending  string
}

func newHarnessReadyWriter(delegate io.Writer, target *url.URL) *harnessReadyWriter {
	return &harnessReadyWriter{delegate: delegate, target: target, ready: make(chan *url.URL, 1)}
}

func (writer *harnessReadyWriter) Write(content []byte) (int, error) {
	written, err := writer.delegate.Write(content)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.pending += string(content)
	for {
		newline := strings.IndexByte(writer.pending, '\n')
		if newline < 0 {
			break
		}
		line := strings.TrimSpace(writer.pending[:newline])
		writer.pending = writer.pending[newline+1:]
		if address := authenticatedHarnessURL(line, writer.target); address != nil {
			select {
			case writer.ready <- address:
			default:
			}
		}
	}
	return written, err
}

func authenticatedHarnessURL(line string, target *url.URL) *url.URL {
	const prefix = "dsh web: "
	start := strings.Index(line, prefix)
	if start < 0 {
		return nil
	}
	fields := strings.Fields(line[start+len(prefix):])
	if len(fields) == 0 {
		return nil
	}
	address, err := url.Parse(fields[0])
	if err != nil || address.Scheme != target.Scheme || address.Host != target.Host || address.Path != "/" || address.Query().Get("token") == "" {
		return nil
	}
	return address
}

func NewApp(proxy *HarnessProxy) *App { return &App{proxy: proxy} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.startHarness(); err != nil {
		log.Printf("DeepSeek Harness startup failed: %v", err)
		a.proxy.Fail(err)
	}
}

func (a *App) shutdown(context.Context) {
	a.stopOnce.Do(func() {
		if a.bridge != nil {
			_ = a.bridge.Close()
		}
		if a.command != nil && a.command.Process != nil {
			_ = a.command.Process.Kill()
		}
		if a.logFile != nil {
			_ = a.logFile.Close()
		}
	})
}

func (a *App) startHarness() error {
	harnessHome, err := sharedHarnessHome()
	if err != nil {
		return fmt.Errorf("prepare shared Harness data directory: %w", err)
	}
	log.Printf("DeepSeek Harness data directory: %s", harnessHome)

	root, err := runtimeRoot()
	if err != nil {
		return err
	}
	node := filepath.Join(root, "node", "bin", "node")
	if runtime.GOOS == "windows" {
		node = filepath.Join(root, "node", "node.exe")
	}
	entry := filepath.Join(root, "app", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	for _, required := range []string{node, entry} {
		if info, statErr := os.Stat(required); statErr != nil || info.IsDir() {
			return fmt.Errorf("runtime file missing: %s (run ./scripts/prepare-runtime.sh)", required)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("reserve local port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	logDir, err := os.UserCacheDir()
	if err != nil || logDir == "" {
		logDir = os.TempDir()
	}
	logDir = filepath.Join(logDir, "DeepSeekHarnessDesktop", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	logPath := filepath.Join(logDir, time.Now().Format("2006-01-02")+".log")
	a.logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open runtime log: %w", err)
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	output := newHarnessReadyWriter(io.MultiWriter(os.Stdout, a.logFile), target)
	a.command = exec.Command(node, entry, "web", "--no-open", "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	a.command.Dir = filepath.Join(root, "app")
	a.command.Env = append(os.Environ(), "DSH_DESKTOP=1", "DSH_HOME="+harnessHome)
	a.command.Stdout = output
	a.command.Stderr = io.MultiWriter(os.Stderr, a.logFile)
	if err := a.command.Start(); err != nil {
		return fmt.Errorf("start dsh: %w", err)
	}

	exited := make(chan error, 1)
	go func() { exited <- a.command.Wait() }()
	var authenticatedURL *url.URL
	select {
	case authenticatedURL = <-output.ready:
	case waitErr := <-exited:
		return fmt.Errorf("dsh exited before serving HTTP: %w; log: %s", waitErr, logPath)
	case <-time.After(90 * time.Second):
		return fmt.Errorf("timed out waiting for %s; log: %s", target, logPath)
	}
	browserCookie, err := exchangeHarnessBrowserSession(authenticatedURL)
	if err != nil {
		return fmt.Errorf("authenticate desktop browser session: %w; log: %s", err, logPath)
	}
	bridgeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local WebSocket bridge: %w", err)
	}
	a.bridge = &http.Server{Handler: newHarnessWebSocketBridge(target, browserCookie)}
	go func() {
		if serveErr := a.bridge.Serve(bridgeListener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("DeepSeek Harness WebSocket bridge failed: %v", serveErr)
		}
	}()
	bridgeWebSocketBase := "ws://" + bridgeListener.Addr().String()
	log.Printf("DeepSeek Harness WebSocket bridge: %s", bridgeWebSocketBase)
	a.proxy.Ready(newHarnessReverseProxy(target, runtime.GOOS, bridgeWebSocketBase, browserCookie))
	go func() {
		if waitErr := <-exited; waitErr != nil {
			a.proxy.Fail(fmt.Errorf("dsh exited: %w; log: %s", waitErr, logPath))
		}
	}()
	return nil
}

func exchangeHarnessBrowserSession(authenticatedURL *url.URL) (string, error) {
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Get(authenticatedURL.String())
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		return "", fmt.Errorf("token exchange returned %s", response.Status)
	}
	for _, cookie := range response.Cookies() {
		if strings.HasPrefix(cookie.Name, "dsh-auth-") && cookie.Value != "" {
			return cookie.Name + "=" + cookie.Value, nil
		}
	}
	return "", fmt.Errorf("token exchange did not return a Harness session cookie")
}

func newHarnessTransportProxy(target *url.URL, browserCookie string) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
		request.Header.Del("Accept-Encoding")
		request.Header.Set("Cookie", browserCookie)
		if request.Header.Get("Origin") != "" {
			request.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
	}
	return proxy
}

func newHarnessWebSocketBridge(target *url.URL, browserCookie string) http.Handler {
	fallback := newHarnessTransportProxy(target, browserCookie)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
			fallback.ServeHTTP(writer, request)
			return
		}
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "websocket bridge unavailable", http.StatusInternalServerError)
			return
		}
		clientConnection, clientBuffer, err := hijacker.Hijack()
		if err != nil {
			return
		}
		upstreamConnection, err := net.DialTimeout("tcp", target.Host, 5*time.Second)
		if err != nil {
			_ = clientConnection.Close()
			return
		}

		upstreamRequest := request.Clone(request.Context())
		upstreamRequest.RequestURI = ""
		upstreamRequest.URL.Scheme = ""
		upstreamRequest.URL.Host = ""
		upstreamRequest.Host = target.Host
		upstreamRequest.Header.Set("Cookie", browserCookie)
		if upstreamRequest.Header.Get("Origin") != "" {
			upstreamRequest.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
		upstreamRequest.Header.Set("Sec-Fetch-Site", "same-origin")
		upstreamRequest.Header.Set("Sec-Fetch-Mode", "websocket")
		upstreamRequest.Header.Set("Sec-Fetch-Dest", "websocket")
		if err := upstreamRequest.Write(upstreamConnection); err != nil {
			_ = upstreamConnection.Close()
			_ = clientConnection.Close()
			return
		}
		if buffered := clientBuffer.Reader.Buffered(); buffered > 0 {
			if _, err := io.CopyN(upstreamConnection, clientBuffer, int64(buffered)); err != nil {
				_ = upstreamConnection.Close()
				_ = clientConnection.Close()
				return
			}
		}
		upstreamReader := bufio.NewReader(upstreamConnection)
		var responseHeader bytes.Buffer
		for {
			line, readErr := upstreamReader.ReadString('\n')
			if readErr != nil {
				_ = upstreamConnection.Close()
				_ = clientConnection.Close()
				return
			}
			responseHeader.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		if _, err := clientConnection.Write(responseHeader.Bytes()); err != nil {
			_ = upstreamConnection.Close()
			_ = clientConnection.Close()
			return
		}
		go func() {
			_, _ = io.Copy(upstreamConnection, clientConnection)
			_ = upstreamConnection.Close()
		}()
		_, _ = io.Copy(clientConnection, upstreamReader)
		_ = clientConnection.Close()
	})
}

func newHarnessReverseProxy(target *url.URL, platform, bridgeWebSocketBase, browserCookie string) http.Handler {
	proxy := newHarnessTransportProxy(target, browserCookie)
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.URL.Path != "/" || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		body = injectDesktopSessionRestore(body, bridgeWebSocketBase)
		if platform == "darwin" || platform == "windows" {
			body = injectDesktopChrome(body, platform)
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
		response.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}
	return proxy
}

func injectDesktopSessionRestore(document []byte, bridgeWebSocketBase string) []byte {
	script := []byte(fmt.Sprintf(`<script id="dsh-desktop-session-restore">(() => {
try {
  const bridgeBase = %s;
  const NativeWebSocket = window.WebSocket;
  window.WebSocket = new Proxy(NativeWebSocket, {
    construct(Target, args) {
      try {
        const url = new URL(String(args[0]), window.location.href);
        const isDesktopOrigin = url.protocol === "wails:" ||
          (url.protocol === "ws:" && url.host === window.location.host);
        if (isDesktopOrigin && url.pathname.startsWith("/api/")) {
          args[0] = bridgeBase + url.pathname + url.search + url.hash;
        }
      } catch (error) {
        console.warn("DeepSeek Harness Desktop could not bridge a WebSocket", error);
      }
      return Reflect.construct(Target, args);
    }
  });
  const key = "dsh.sessions.current";
  const stored = JSON.parse(localStorage.getItem(key) || "{}");
  if (typeof stored.sessionId === "string" && stored.sessionId.length > 0) return;
  const request = new XMLHttpRequest();
  request.open("POST", "/api/session/list", false);
  request.setRequestHeader("Content-Type", "application/json");
  request.send(JSON.stringify({type:"client-request",rpcId:"desktop-session-restore",method:"session/list",payload:{args:{_request:{}}}}));
  if (request.status < 200 || request.status >= 300) return;
  const response = JSON.parse(request.responseText);
  const items = response?.result?.ok === true ? response.result.value?.items : undefined;
  if (!Array.isArray(items)) return;
  const selected = items.filter((item) => item && item.blank !== true && typeof item.sessionId === "string")
    .sort((left, right) => Number(right.updatedAt || 0) - Number(left.updatedAt || 0))[0];
  if (selected) localStorage.setItem(key, JSON.stringify({sessionId:selected.sessionId}));
} catch (error) {
  console.warn("DeepSeek Harness Desktop could not restore the recent session", error);
}
})()</script>`, strconv.Quote(bridgeWebSocketBase)))
	return injectBeforeClosingBody(document, script)
}

func injectDesktopChrome(document []byte, platform string) []byte {
	controls := ""
	dragRight := "8px"
	chromeHeight := "44px"
	if platform == "windows" {
		dragRight = "116px"
		chromeHeight = "32px"
		controls = `<div id="dsh-desktop-window-controls"><button type="button" aria-label="Minimise" onclick="window.runtime?.WindowMinimise?.()">&#8722;</button><button type="button" aria-label="Maximise" onclick="window.runtime?.WindowToggleMaximise?.()">&#9723;</button><button class="close" type="button" aria-label="Close" onclick="window.runtime?.Quit?.()">&#215;</button></div>`
	}
	chrome := fmt.Sprintf(`<style id="dsh-desktop-chrome-style">
html,body{overflow:hidden!important}body{padding-top:%s!important;box-sizing:border-box!important}#root{height:calc(100vh - %s)!important;min-height:0!important}
#dsh-desktop-titlebar{position:fixed;inset:0 0 auto 0;height:%s;z-index:2147483646;pointer-events:none;background:rgba(248,248,248,.78);border-bottom:1px solid rgba(0,0,0,.08);backdrop-filter:blur(18px);-webkit-backdrop-filter:blur(18px)}
body[data-ds-dark-theme] #dsh-desktop-titlebar{background:rgba(20,20,20,.76);border-bottom-color:rgba(255,255,255,.08)}
#dsh-desktop-drag-region{position:absolute;top:0;bottom:0;left:%s;right:%s;pointer-events:auto;user-select:none;--wails-draggable:drag}
#dsh-desktop-window-controls{position:absolute;top:0;right:0;height:32px;display:flex;pointer-events:auto;--wails-draggable:no-drag}
#dsh-desktop-window-controls button{width:38px;height:32px;border:0;border-radius:0;background:transparent;color:inherit;font:15px/1 system-ui;cursor:default}
#dsh-desktop-window-controls button:hover{background:rgba(127,127,127,.18)}#dsh-desktop-window-controls button.close:hover{background:#c42b1c;color:#fff}
</style><div id="dsh-desktop-titlebar"><div id="dsh-desktop-drag-region" aria-hidden="true"></div>%s</div>`, chromeHeight, chromeHeight, chromeHeight, map[string]string{"darwin": "78px", "windows": "8px"}[platform], dragRight, controls)
	return injectBeforeClosingBody(document, []byte(chrome))
}

func injectBeforeClosingBody(document, content []byte) []byte {
	closingBody := []byte("</body>")
	if index := bytes.LastIndex(document, closingBody); index >= 0 {
		result := make([]byte, 0, len(document)+len(content))
		result = append(result, document[:index]...)
		result = append(result, content...)
		result = append(result, document[index:]...)
		return result
	}
	return append(document, content...)
}

func runtimeRoot() (string, error) {
	if value := os.Getenv("DSH_DESKTOP_RUNTIME"); value != "" {
		return filepath.Abs(value)
	}
	executable, err := os.Executable()
	if err == nil {
		executableDir := filepath.Dir(executable)
		candidates := []string{filepath.Join(executableDir, "runtime")}
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, filepath.Clean(filepath.Join(executableDir, "..", "Resources", "runtime")))
		}
		for _, candidate := range candidates {
			if _, statErr := os.Stat(candidate); statErr == nil {
				return candidate, nil
			}
		}
	}
	workingDir, _ := os.Getwd()
	for _, candidate := range []string{filepath.Join(workingDir, "runtime", "current"), filepath.Join(workingDir, "runtime")} {
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("DeepSeek Harness runtime not found")
}
