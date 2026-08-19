package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	proxyServer := httptest.NewServer(newHarnessReverseProxy(target))
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
