package main

import (
	"fmt"
	"net/http"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

type HarnessProxy struct {
	mu      sync.RWMutex
	proxy   http.Handler
	failure error
	ready   chan struct{}
	once    sync.Once
}

func NewHarnessProxy() *HarnessProxy { return &HarnessProxy{ready: make(chan struct{})} }

func (p *HarnessProxy) Ready(proxy http.Handler) {
	p.mu.Lock()
	p.proxy = proxy
	p.failure = nil
	p.mu.Unlock()
	p.once.Do(func() { close(p.ready) })
}

func (p *HarnessProxy) Fail(err error) {
	p.mu.Lock()
	p.failure = err
	p.mu.Unlock()
	p.once.Do(func() { close(p.ready) })
}

func (p *HarnessProxy) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	<-p.ready
	p.mu.RLock()
	proxy, failure := p.proxy, p.failure
	p.mu.RUnlock()
	if proxy != nil {
		proxy.ServeHTTP(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintf(writer, `<!doctype html><meta charset="utf-8"><title>DeepSeek Harness Desktop</title><style>body{font:16px system-ui;margin:48px;max-width:900px}pre{padding:16px;background:#111;color:#eee;white-space:pre-wrap}</style><h1>DeepSeek Harness 启动失败</h1><p>请运行 <code>./scripts/prepare-runtime.sh</code> 后重试。</p><pre>%s</pre>`, failure)
}

func main() {
	proxy := NewHarnessProxy()
	app := NewApp(proxy)
	appOptions := &options.App{
		Title:     "DeepSeek Harness Desktop",
		Width:     1440,
		Height:    920,
		MinWidth:  980,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Handler: proxy,
		},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			IsZoomControlEnabled: true,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			Appearance:           mac.NSAppearanceNameDarkAqua,
		},
	}
	if runtime.GOOS == "windows" {
		appOptions.BackgroundColour = &options.RGBA{R: 18, G: 18, B: 18, A: 1}
	}
	err := wails.Run(appOptions)
	if err != nil {
		panic(err)
	}
}
