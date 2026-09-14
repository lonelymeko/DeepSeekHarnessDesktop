package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// systemProxy is what the operating system reports about outbound proxying.
// Each field is a ready-to-use proxy URL, empty when that scheme is not
// configured. Source names the detector for diagnostics.
type systemProxy struct {
	HTTP    string
	HTTPS   string
	SOCKS   string
	NoProxy []string
	Source  string
}

// detected reports whether the operating system named any proxy at all.
func (p systemProxy) detected() bool {
	return p.HTTP != "" || p.HTTPS != "" || p.SOCKS != ""
}

// proxyURL builds a proxy URL from a detected host and port, bracketing IPv6
// literals so the result parses. It returns "" for an unusable pair.
func proxyURL(scheme, host, port string) string {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || port == "" {
		return ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + ":" + port
}

// loopbackNoProxy is the bypass list the desktop shell always adds. The Harness
// talks to its own loopback server, and the desktop shell reverse-proxies that
// same address; routing either through a proxy would break startup outright.
// The upstream Harness merges the identical list in its own proxy policy.
var loopbackNoProxy = []string{"localhost", "127.0.0.1", "::1", "[::1]"}

// proxyPlan is the outbound-proxy decision for one launch. It is derived once
// at startup and then used for both the child Harness process and the in-process
// updater, so both halves of the shell always agree.
type proxyPlan struct {
	// UseSystemProxy records the user's setting, for display.
	UseSystemProxy bool
	// HTTP, HTTPS and All are explicit proxy URLs; empty means "not configured".
	HTTP  string
	HTTPS string
	All   string
	// NoProxy is the merged bypass list.
	NoProxy []string
	// Source names where the proxies came from, for the settings panel.
	Source string
	// Inherited records that the environment already carried proxy variables.
	// A plan that routes nothing still republishes those, because the child's
	// environment is rebuilt from the plan rather than from the raw inheriting
	// environment.
	Inherited bool
}

// resolveProxyPlan merges the user's environment with the detected system
// proxy. An exported variable always wins for the scheme it names, because
// exporting one is this run's explicit intent; the system proxy fills the
// schemes the environment left open, and only while the setting is on.
func resolveProxyPlan(useSystemProxy bool, lookup func(string) string, detect func() systemProxy) proxyPlan {
	inherited := []string{
		lookup("http_proxy"), lookup("HTTP_PROXY"),
		lookup("https_proxy"), lookup("HTTPS_PROXY"),
		lookup("all_proxy"), lookup("ALL_PROXY"),
		lookup("no_proxy"), lookup("NO_PROXY"),
	}
	plan := proxyPlan{
		UseSystemProxy: useSystemProxy,
		HTTP:           firstNonEmpty(lookup("http_proxy"), lookup("HTTP_PROXY")),
		HTTPS:          firstNonEmpty(lookup("https_proxy"), lookup("HTTPS_PROXY")),
		All:            firstNonEmpty(lookup("all_proxy"), lookup("ALL_PROXY")),
	}
	for _, value := range inherited {
		if strings.TrimSpace(value) != "" {
			plan.Inherited = true
			break
		}
	}
	bypass := splitProxyList(firstNonEmpty(lookup("no_proxy"), lookup("NO_PROXY")))
	if useSystemProxy {
		detected := detect()
		plan.Source = detected.Source
		if plan.HTTP == "" {
			plan.HTTP = firstNonEmpty(detected.HTTP, detected.SOCKS)
		}
		if plan.HTTPS == "" {
			plan.HTTPS = firstNonEmpty(detected.HTTPS, detected.HTTP, detected.SOCKS)
		}
		if plan.All == "" {
			plan.All = firstNonEmpty(detected.SOCKS, detected.HTTPS, detected.HTTP)
		}
		bypass = append(bypass, detected.NoProxy...)
	}
	plan.NoProxy = mergeNoProxy(bypass, loopbackNoProxy)
	return plan
}

// routed reports whether any outbound request would use a proxy.
func (p proxyPlan) routed() bool {
	return p.HTTP != "" || p.HTTPS != "" || p.All != ""
}

// environment is the variable set the child Harness process must inherit for
// its own outbound traffic to follow the plan. Both letter cases are written,
// because the Harness reads the lower-case names first while a Node transport
// accepts either, and one shared value avoids a case-precedence surprise. A
// plan with nothing to route and nothing inherited writes nothing, so turning
// the setting off truly leaves the child's environment alone.
func (p proxyPlan) environment() []string {
	if !p.routed() && !p.Inherited {
		return nil
	}
	var entries []string
	add := func(name, value string) {
		if value != "" {
			entries = append(entries, name+"="+value)
		}
	}
	add("http_proxy", p.HTTP)
	add("HTTP_PROXY", p.HTTP)
	add("https_proxy", p.HTTPS)
	add("HTTPS_PROXY", p.HTTPS)
	add("all_proxy", p.All)
	add("ALL_PROXY", p.All)
	if bypass := strings.Join(p.NoProxy, ","); bypass != "" {
		add("no_proxy", bypass)
		add("NO_PROXY", bypass)
	}
	return entries
}

// proxyFunc routes the desktop updater's requests the same way the child
// process routes its own. Resolving per request keeps a bypass list and a
// scheme split working, which a single static proxy URL could not express.
func (p proxyPlan) proxyFunc() func(*http.Request) (*url.URL, error) {
	return func(request *http.Request) (*url.URL, error) {
		if bypassesProxy(request.URL.Hostname(), p.NoProxy) {
			return nil, nil
		}
		raw := p.HTTP
		if strings.EqualFold(request.URL.Scheme, "https") {
			raw = p.HTTPS
		}
		if raw == "" {
			raw = p.All
		}
		if raw == "" {
			return nil, nil
		}
		return url.Parse(raw)
	}
}

// bypassesProxy reports whether a host matches any bypass entry. Entries are
// host names, IPv4 literals, IPv6 literals, or a leading-dot or wildcard
// domain suffix; CIDR ranges the operating system reports are ignored rather
// than guessed at, because a false bypass is worse than an extra hop.
func bypassesProxy(host string, entries []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		entry = strings.Trim(entry, "[]")
		if entry == "" || entry == "<local>" || strings.Contains(entry, "/") {
			continue
		}
		entry = strings.TrimPrefix(entry, "*")
		entry = strings.TrimPrefix(entry, ".")
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// splitProxyList splits a bypass string on the separators the platforms and
// the environment use.
func splitProxyList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	entries := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

// mergeNoProxy concatenates bypass lists, dropping empty entries and case
// duplicates while preserving the caller's order.
func mergeNoProxy(lists ...[]string) []string {
	seen := map[string]bool{}
	entries := []string{}
	for _, list := range lists {
		for _, entry := range list {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, trimmed)
		}
	}
	return entries
}

// proxyEnvironmentNames are the variables a proxy plan owns, in both letter
// cases, matching exactly what environment() writes.
var proxyEnvironmentNames = []string{
	"http_proxy", "HTTP_PROXY",
	"https_proxy", "HTTPS_PROXY",
	"all_proxy", "ALL_PROXY",
	"no_proxy", "NO_PROXY",
}

// withoutProxyEnvironment drops inherited proxy variables. The plan already
// folded the user's exported values into its decision, so re-adding them from
// the plan leaves exactly one value per name; an inherited duplicate could
// otherwise be ranked differently by the child's different environment readers.
func withoutProxyEnvironment(env []string) []string {
	owned := make(map[string]bool, len(proxyEnvironmentNames))
	for _, name := range proxyEnvironmentNames {
		owned[name] = true
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if owned[name] {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// firstNonEmpty returns the first non-empty, trimmed value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
