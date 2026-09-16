package main

import (
	"net/http"
	"testing"
)

// A SOCKS-only plan must reach the network, which means the dialer carries the
// request rather than the (absent) HTTP proxy hook.
func TestSOCKSOnlyPlanBuildsADialer(t *testing.T) {
	plan := resolveProxyPlan(true, func(string) string { return "" }, func() systemProxy {
		return systemProxy{SOCKS: "socks5://127.0.0.1:7897", Source: "fixture"}
	})
	if plan.SOCKS != "socks5://127.0.0.1:7897" {
		t.Fatalf("SOCKS = %q", plan.SOCKS)
	}
	if !plan.routed() {
		t.Fatal("a SOCKS-only plan must count as routed")
	}
	if plan.HTTP != "" || plan.HTTPS != "" {
		t.Fatalf("SOCKS must not be conflated into the HTTP family: %+v", plan)
	}
	dialer, err := plan.socksDialer()
	if err != nil {
		t.Fatal(err)
	}
	if dialer == nil {
		t.Fatal("a SOCKS-only plan must build a dialer")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := plan.applyToTransport(transport); err != nil {
		t.Fatal(err)
	}
	if transport.DialContext == nil {
		t.Fatal("the SOCKS dialer was not installed")
	}
	// The HTTP hook must stay inert: it cannot express SOCKS.
	request, _ := http.NewRequest(http.MethodGet, "https://api.deepseek.com/user/balance", nil)
	resolved, err := transport.Proxy(request)
	if err != nil || resolved != nil {
		t.Fatalf("HTTP proxy hook returned (%v, %v), want (nil, nil)", resolved, err)
	}
}

// An http:// ALL_PROXY must not be mistaken for a SOCKS endpoint.
func TestSocksFromRejectsHTTP(t *testing.T) {
	if socksFrom("http://127.0.0.1:7897") != "" {
		t.Fatal("an http:// proxy must not be treated as SOCKS")
	}
	if socksFrom("socks5://127.0.0.1:7897") == "" {
		t.Fatal("a socks5:// proxy must be recognised")
	}
	if !isSOCKSProxyURL("socks5h://127.0.0.1:1080") {
		t.Fatal("socks5h must be recognised")
	}
	if isSOCKSProxyURL("http://127.0.0.1:1080") {
		t.Fatal("http must not be recognised as SOCKS")
	}
}

// A plan with no SOCKS endpoint installs no dialer, leaving the default one.
func TestNoSOCKSEndpointInstallsNoDialer(t *testing.T) {
	plan := resolveProxyPlan(true, func(string) string { return "" }, func() systemProxy {
		return systemProxy{HTTP: "http://127.0.0.1:7897"}
	})
	dialer, err := plan.socksDialer()
	if err != nil {
		t.Fatal(err)
	}
	if dialer != nil {
		t.Fatal("an HTTP-only plan must not build a SOCKS dialer")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	before := transport.DialContext
	if err := plan.applyToTransport(transport); err != nil {
		t.Fatal(err)
	}
	if transport.DialContext == nil {
		t.Fatal("the default dialer must survive")
	}
	_ = before
	request, _ := http.NewRequest(http.MethodGet, "https://api.deepseek.com/", nil)
	resolved, err := transport.Proxy(request)
	if err != nil || resolved == nil || resolved.String() != "http://127.0.0.1:7897" {
		t.Fatalf("HTTP hook returned (%v, %v)", resolved, err)
	}
}

// A SOCKS dialer routed through the real local proxy must reach the network,
// which is the whole claim of the feature.
func TestSOCKSPlanReachesTheNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	plan := resolveProxyPlan(true, func(string) string { return "" }, func() systemProxy {
		return systemProxy{SOCKS: "socks5://127.0.0.1:7897"}
	})
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := plan.applyToTransport(transport); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: transport}
	request, _ := http.NewRequest(http.MethodGet, "https://api.deepseek.com/user/balance", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Skipf("local SOCKS proxy unreachable: %v", err)
	}
	defer response.Body.Close()
	t.Logf("through SOCKS: HTTP %d (401 = reached DeepSeek without a key)", response.StatusCode)
	if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %s", response.Status)
	}
}
