package main

import (
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
	"sync"
	"time"
)

type App struct {
	ctx      context.Context
	command  *exec.Cmd
	proxy    *HarnessProxy
	logFile  *os.File
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
		if a.command != nil && a.command.Process != nil {
			_ = a.command.Process.Kill()
		}
		if a.logFile != nil {
			_ = a.logFile.Close()
		}
	})
}

func (a *App) startHarness() error {
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
	a.command.Env = append(os.Environ(), "DSH_DESKTOP=1")
	a.command.Stdout = io.MultiWriter(os.Stdout, a.logFile)
	a.command.Stderr = io.MultiWriter(os.Stderr, a.logFile)
	if err := a.command.Start(); err != nil {
		return fmt.Errorf("start dsh: %w", err)
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err := waitForHarness(target.String(), a.command, 90*time.Second); err != nil {
		return fmt.Errorf("%w; log: %s", err, logPath)
	}
	a.proxy.Ready(newHarnessReverseProxy(target))
	go func() {
		if waitErr := a.command.Wait(); waitErr != nil {
			a.proxy.Fail(fmt.Errorf("dsh exited: %w; log: %s", waitErr, logPath))
		}
	}()
	return nil
}

func newHarnessReverseProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
		if request.Header.Get("Origin") != "" {
			request.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
	}
	return proxy
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
