package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// scutilProxyFixture is real `scutil --proxy` output from a machine running a
// local Clash-style proxy, the exact case this feature exists for.
const scutilProxyFixture = `<dictionary> {
  ExceptionsList : <array> {
    0 : 127.0.0.1
    1 : 192.168.0.0/16
    2 : 10.0.0.0/8
    3 : localhost
    4 : *.local
    5 : <local>
  }
  HTTPEnable : 1
  HTTPPort : 7897
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7897
  HTTPSProxy : 127.0.0.1
  ProxyAutoConfigEnable : 0
  SOCKSEnable : 1
  SOCKSPort : 7897
  SOCKSProxy : 127.0.0.1
}
`

func TestParseScutilProxy(t *testing.T) {
	proxy := parseScutilProxy(scutilProxyFixture)
	if proxy.HTTP != "http://127.0.0.1:7897" {
		t.Errorf("HTTP = %q", proxy.HTTP)
	}
	if proxy.HTTPS != "http://127.0.0.1:7897" {
		t.Errorf("HTTPS = %q, macOS proxies HTTPS through an HTTP CONNECT endpoint", proxy.HTTPS)
	}
	if proxy.SOCKS != "socks5://127.0.0.1:7897" {
		t.Errorf("SOCKS = %q", proxy.SOCKS)
	}
	if proxy.Source == "" {
		t.Error("a detected proxy must name its source")
	}
	for _, expected := range []string{"127.0.0.1", "192.168.0.0/16", "<local>", "*.local"} {
		if !containsString(proxy.NoProxy, expected) {
			t.Errorf("NoProxy %v lacks %q", proxy.NoProxy, expected)
		}
	}
}

func TestParseScutilProxyIgnoresDisabledSchemes(t *testing.T) {
	proxy := parseScutilProxy(`<dictionary> {
  HTTPEnable : 0
  HTTPPort : 7897
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 1080
  HTTPSProxy : proxy.example.com
  ProxyAutoConfigEnable : 1
  ProxyAutoConfigURLString : https://example.com/proxy.pac
}
`)
	if proxy.HTTP != "" {
		t.Errorf("a disabled HTTP proxy must not be reported: %q", proxy.HTTP)
	}
	if proxy.HTTPS != "http://proxy.example.com:1080" {
		t.Errorf("HTTPS = %q", proxy.HTTPS)
	}
	if proxy.SOCKS != "" {
		t.Errorf("an absent SOCKS entry must not be invented: %q", proxy.SOCKS)
	}
}

func TestParseScutilProxyReportsNothingWhenEmpty(t *testing.T) {
	proxy := parseScutilProxy("<dictionary> {\n  ProxyAutoConfigEnable : 0\n}\n")
	if proxy.detected() {
		t.Fatalf("no scheme is enabled, yet %+v was reported", proxy)
	}
	if proxy.Source != "" {
		t.Fatalf("an undetected proxy must not name a source: %q", proxy.Source)
	}
}

func TestParseWindowsProxyRegistrySingleEndpoint(t *testing.T) {
	proxy := parseWindowsProxyRegistry(`HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyEnable    REG_DWORD    0x1
    ProxyServer    REG_SZ    127.0.0.1:7897
    ProxyOverride    REG_SZ    <local>;*.example.com;10.0.0.1
`)
	if proxy.HTTP != "http://127.0.0.1:7897" || proxy.HTTPS != "http://127.0.0.1:7897" {
		t.Fatalf("one endpoint must serve both schemes: %+v", proxy)
	}
	for _, expected := range []string{"<local>", "*.example.com", "10.0.0.1"} {
		if !containsString(proxy.NoProxy, expected) {
			t.Errorf("NoProxy %v lacks %q", proxy.NoProxy, expected)
		}
	}
}

func TestParseWindowsProxyRegistryPerScheme(t *testing.T) {
	proxy := parseWindowsProxyRegistry(`HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyEnable    REG_DWORD    0x1
    ProxyServer    REG_SZ    http=127.0.0.1:8080;https=127.0.0.1:8443;socks=127.0.0.1:1080
`)
	if proxy.HTTP != "http://127.0.0.1:8080" {
		t.Errorf("HTTP = %q", proxy.HTTP)
	}
	if proxy.HTTPS != "http://127.0.0.1:8443" {
		t.Errorf("HTTPS = %q", proxy.HTTPS)
	}
	if proxy.SOCKS != "socks5://127.0.0.1:1080" {
		t.Errorf("SOCKS = %q", proxy.SOCKS)
	}
}

func TestParseWindowsProxyRegistryHonoursDisable(t *testing.T) {
	proxy := parseWindowsProxyRegistry(`HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyEnable    REG_DWORD    0x0
    ProxyServer    REG_SZ    127.0.0.1:7897
`)
	if proxy.detected() {
		t.Fatalf("proxying is off, yet %+v was reported", proxy)
	}
}

func TestParseWindowsProxyRegistryRejectsPortlessServer(t *testing.T) {
	proxy := parseWindowsProxyRegistry(`HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyEnable    REG_DWORD    0x1
    ProxyServer    REG_SZ    proxy.example.com
`)
	if proxy.detected() {
		t.Fatalf("a bare host names no port, so it must be refused: %+v", proxy)
	}
}

func TestResolveProxyPlanFillsFromSystemProxy(t *testing.T) {
	detect := func() systemProxy { return parseScutilProxy(scutilProxyFixture) }
	plan := resolveProxyPlan(true, func(string) string { return "" }, detect)
	if plan.HTTP != "http://127.0.0.1:7897" || plan.HTTPS != "http://127.0.0.1:7897" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.All != "socks5://127.0.0.1:7897" {
		t.Fatalf("the SOCKS endpoint must back the ALL_PROXY fallback: %+v", plan)
	}
	if plan.Source == "" {
		t.Error("the plan must record where its proxies came from")
	}
	for _, expected := range loopbackNoProxy {
		if !containsString(plan.NoProxy, expected) {
			t.Errorf("NoProxy %v must always carry loopback entry %q", plan.NoProxy, expected)
		}
	}
}

func TestResolveProxyPlanLetsExplicitEnvironmentWin(t *testing.T) {
	detect := func() systemProxy { return parseScutilProxy(scutilProxyFixture) }
	environment := map[string]string{
		"http_proxy": "http://explicit.example:3128",
		"no_proxy":   "internal.example",
	}
	plan := resolveProxyPlan(true, func(name string) string { return environment[name] }, detect)
	if plan.HTTP != "http://explicit.example:3128" {
		t.Fatalf("an exported variable must win for its own scheme: %+v", plan)
	}
	if plan.HTTPS != "http://127.0.0.1:7897" {
		t.Fatalf("the system proxy must fill the schemes the environment left open: %+v", plan)
	}
	if plan.All != "socks5://127.0.0.1:7897" {
		t.Fatalf("ALL_PROXY fallback = %q", plan.All)
	}
	if !containsString(plan.NoProxy, "internal.example") {
		t.Fatalf("the exported bypass list must survive: %v", plan.NoProxy)
	}
}

func TestResolveProxyPlanIgnoresSystemProxyWhenDisabled(t *testing.T) {
	detect := func() systemProxy {
		t.Error("the operating system must not be consulted while the setting is off")
		return systemProxy{}
	}
	plan := resolveProxyPlan(false, func(string) string { return "" }, detect)
	if plan.routed() {
		t.Fatalf("the setting is off, so nothing may be routed: %+v", plan)
	}
	if len(plan.environment()) != 0 {
		t.Fatalf("the setting is off, so no variable may be written: %v", plan.environment())
	}
}

func TestResolveProxyPlanStillHonoursEnvironmentWhenDisabled(t *testing.T) {
	// Turning the setting off must not "fix" a deliberate `export http_proxy=`.
	detect := func() systemProxy { return systemProxy{} }
	plan := resolveProxyPlan(false, func(name string) string {
		if name == "https_proxy" {
			return "http://explicit.example:3128"
		}
		return ""
	}, detect)
	if plan.HTTPS != "http://explicit.example:3128" {
		t.Fatalf("an exported variable must survive the setting being off: %+v", plan)
	}
}

func TestProxyPlanEnvironmentWritesBothCases(t *testing.T) {
	plan := resolveProxyPlan(true, func(string) string { return "" }, func() systemProxy {
		return parseScutilProxy(scutilProxyFixture)
	})
	entries := map[string]string{}
	for _, entry := range plan.environment() {
		name, value, _ := strings.Cut(entry, "=")
		entries[name] = value
	}
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY"} {
		if entries[name] == "" {
			t.Errorf("environment() must write %s: %v", name, entries)
		}
	}
	if entries["http_proxy"] != entries["HTTP_PROXY"] {
		t.Errorf("both letter cases must carry one value: %q vs %q", entries["http_proxy"], entries["HTTP_PROXY"])
	}
	if !strings.Contains(entries["NO_PROXY"], "127.0.0.1") {
		t.Errorf("NO_PROXY must always bypass loopback: %q", entries["NO_PROXY"])
	}
}

func TestProxyPlanRoutesBySchemeAndBypassesLoopback(t *testing.T) {
	plan := resolveProxyPlan(true, func(string) string { return "" }, func() systemProxy {
		return parseScutilProxy(scutilProxyFixture)
	})
	route := plan.proxyFunc()
	cases := []struct {
		target string
		want   string
	}{
		{"https://api.github.com/repos/x", "http://127.0.0.1:7897"},
		{"https://api.deepseek.com/user/balance", "http://127.0.0.1:7897"},
		{"http://example.com/", "http://127.0.0.1:7897"},
		// The Harness's own loopback server and the desktop bridge must never
		// be sent through a proxy, or startup breaks outright.
		{"http://127.0.0.1:52431/api/settings.describe", ""},
		{"http://localhost:52431/", ""},
		{"http://[::1]:52431/", ""},
	}
	for _, test := range cases {
		request, err := http.NewRequest(http.MethodGet, test.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := route(request)
		if err != nil {
			t.Fatalf("proxyFunc(%s): %v", test.target, err)
		}
		got := ""
		if resolved != nil {
			got = resolved.String()
		}
		if got != test.want {
			t.Errorf("proxyFunc(%s) = %q, want %q", test.target, got, test.want)
		}
	}
}

func TestProxyPlanRoutesNothingWhenEmpty(t *testing.T) {
	plan := resolveProxyPlan(false, func(string) string { return "" }, func() systemProxy { return systemProxy{} })
	request, err := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := plan.proxyFunc()(request)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != nil {
		t.Fatalf("nothing is configured, yet %s was chosen", resolved)
	}
}

func TestBypassesProxy(t *testing.T) {
	entries := []string{"localhost", "127.0.0.1", "::1", "[::1]", ".example.com", "*.internal", "<local>", "10.0.0.0/8", ""}
	cases := map[string]bool{
		"localhost":        true,
		"127.0.0.1":        true,
		"::1":              true,
		"api.example.com":  true,
		"example.com":      true,
		"host.internal":    true,
		"api.github.com":   false,
		"10.1.2.3":         false,
		"notexample.com":   false,
		"example.com.evil": false,
	}
	for host, want := range cases {
		if got := bypassesProxy(host, entries); got != want {
			t.Errorf("bypassesProxy(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestWithoutProxyEnvironmentDropsOnlyProxyNames(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"http_proxy=http://stale.example:1",
		"HTTPS_PROXY=http://stale.example:2",
		"NO_PROXY=stale.example",
		"HOME=/Users/example",
		"DSH_HOME=/tmp/dsh",
	}
	filtered := withoutProxyEnvironment(env)
	want := []string{"PATH=/usr/bin", "HOME=/Users/example", "DSH_HOME=/tmp/dsh"}
	if len(filtered) != len(want) {
		t.Fatalf("withoutProxyEnvironment = %v, want %v", filtered, want)
	}
	for index, entry := range want {
		if filtered[index] != entry {
			t.Fatalf("withoutProxyEnvironment = %v, want %v", filtered, want)
		}
	}
}

func TestRedactProxyURL(t *testing.T) {
	if got := redactProxyURL(""); got != "" {
		t.Errorf("redactProxyURL(\"\") = %q", got)
	}
	plain := "http://127.0.0.1:7897"
	if got := redactProxyURL(plain); got != plain {
		t.Errorf("a credential-free URL must survive unchanged: %q", got)
	}
	redacted := redactProxyURL("http://user:secret@proxy.example:80")
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "user") {
		t.Errorf("credentials leaked into %q", redacted)
	}
	if !strings.Contains(redacted, "proxy.example:80") {
		t.Errorf("redaction must keep the address usable: %q", redacted)
	}
}

func TestProxyURLRejectsIncompletePairs(t *testing.T) {
	cases := []struct {
		scheme, host, port, want string
	}{
		{"http", "127.0.0.1", "7897", "http://127.0.0.1:7897"},
		{"socks5", "::1", "1080", "socks5://[::1]:1080"},
		{"http", "", "7897", ""},
		{"http", "127.0.0.1", "", ""},
		{"http", "127.0.0.1", "not-a-port", ""},
	}
	for _, test := range cases {
		if got := proxyURL(test.scheme, test.host, test.port); got != test.want {
			t.Errorf("proxyURL(%q, %q, %q) = %q, want %q", test.scheme, test.host, test.port, got, test.want)
		}
	}
}

func TestSplitProxyListSeparators(t *testing.T) {
	got := splitProxyList("a.example,b.example;c.example d.example")
	want := []string{"a.example", "b.example", "c.example", "d.example"}
	if len(got) != len(want) {
		t.Fatalf("splitProxyList = %v, want %v", got, want)
	}
	for index, entry := range want {
		if got[index] != entry {
			t.Fatalf("splitProxyList = %v, want %v", got, want)
		}
	}
}

func TestProxyPlanProxyFuncReturnsParseableURL(t *testing.T) {
	plan := resolveProxyPlan(true, func(string) string { return "" }, func() systemProxy {
		return systemProxy{HTTP: "http://127.0.0.1:7897", Source: "fixture"}
	})
	request, err := http.NewRequest(http.MethodGet, "https://api.deepseek.com/user/balance", nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := plan.proxyFunc()(request)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil {
		t.Fatal("an http:// fallback must serve the https scheme")
	}
	parsed, err := url.Parse(resolved.String())
	if err != nil {
		t.Fatalf("resolved proxy URL is unparseable: %v", err)
	}
	if parsed.Host != "127.0.0.1:7897" {
		t.Fatalf("resolved host = %q", parsed.Host)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
