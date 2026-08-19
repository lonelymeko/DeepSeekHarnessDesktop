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

	a.command = exec.Command(node, entry, "web", "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	a.command.Dir = filepath.Join(root, "app")
	a.command.Env = append(os.Environ(), "DSH_DESKTOP=1", "DSH_HOME="+harnessHome)
	a.command.Stdout = io.MultiWriter(os.Stdout, a.logFile)
	a.command.Stderr = io.MultiWriter(os.Stderr, a.logFile)
	if err := a.command.Start(); err != nil {
		return fmt.Errorf("start dsh: %w", err)
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err := waitForHarness(target.String(), a.command, 90*time.Second); err != nil {
		return fmt.Errorf("%w; log: %s", err, logPath)
	}
	bridgeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local WebSocket bridge: %w", err)
	}
	a.bridge = &http.Server{Handler: newHarnessWebSocketBridge(target)}
	go func() {
		if serveErr := a.bridge.Serve(bridgeListener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("DeepSeek Harness WebSocket bridge failed: %v", serveErr)
		}
	}()
	bridgeWebSocketBase := "ws://" + bridgeListener.Addr().String()
	log.Printf("DeepSeek Harness WebSocket bridge: %s", bridgeWebSocketBase)
	a.proxy.Ready(newHarnessReverseProxy(target, runtime.GOOS, bridgeWebSocketBase))
	go func() {
		if waitErr := a.command.Wait(); waitErr != nil {
			a.proxy.Fail(fmt.Errorf("dsh exited: %w; log: %s", waitErr, logPath))
		}
	}()
	return nil
}

func newHarnessTransportProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
		request.Header.Del("Accept-Encoding")
		if request.Header.Get("Origin") != "" {
			request.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
	}
	return proxy
}

func newHarnessWebSocketBridge(target *url.URL) http.Handler {
	fallback := newHarnessTransportProxy(target)
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

func newHarnessReverseProxy(target *url.URL, platform, bridgeWebSocketBase string) *httputil.ReverseProxy {
	proxy := newHarnessTransportProxy(target)
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
        if (url.protocol === "wails:" && url.pathname.startsWith("/api/")) {
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
  request.open("POST", "/api/session.list", false);
  request.setRequestHeader("Content-Type", "application/json");
  request.send(JSON.stringify({type:"client-request",rpcId:"desktop-session-restore",method:"session.list",payload:{}}));
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

func waitForHarness(address string, command *exec.Cmd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		if command.ProcessState != nil && command.ProcessState.Exited() {
			return fmt.Errorf("dsh exited before serving HTTP")
		}
		response, err := client.Get(address)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", address)
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
