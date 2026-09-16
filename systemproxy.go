package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/net/proxy"
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
	// SOCKS is a socks5:// endpoint, kept separate from the HTTP family: the
	// two are not interchangeable, and only the children that can actually
	// speak SOCKS should be handed one.
	SOCKS string
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
	plan.SOCKS = firstNonEmpty(lookup("socks_proxy"), lookup("SOCKS_PROXY"), lookup("all_proxy"), lookup("ALL_PROXY"))
	if useSystemProxy {
		detected := detect()
		plan.Source = detected.Source
		// A SOCKS endpoint is kept apart from the HTTP family on purpose. It is
		// not interchangeable with one: an http:// or https:// variable naming a
		// SOCKS URL is rejected outright by the Harness, and Go's transport can
		// only reach the scheme through a dedicated dialer. Conflating the two
		// produced a proxy setting that silently routed nothing.
		if plan.HTTP == "" {
			plan.HTTP = detected.HTTP
		}
		if plan.HTTPS == "" {
			plan.HTTPS = firstNonEmpty(detected.HTTPS, detected.HTTP)
		}
		if plan.SOCKS == "" {
			plan.SOCKS = detected.SOCKS
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
	return p.HTTP != "" || p.HTTPS != "" || p.All != "" || p.SOCKS != ""
}

// environment is the variable set the child Harness process must inherit for
// its own outbound traffic to follow the plan. Both letter cases are written,
// because the Harness reads the lower-case names first while a Node transport
// accepts either, and one shared value avoids a case-precedence surprise. A
// plan with nothing to route and nothing inherited writes nothing, so turning
// the setting off truly leaves the child's environment alone.
//
// ALL_PROXY carries the socks5:// URL even though the Harness's own policy
// rejects SOCKS and connects directly. Publishing it is still right: the value
// is what the user configured, every other tool the child spawns reads the same
// variable, and dropping it would hide the setting from them. The Harness's
// rejection is reported by the Harness itself, not swallowed here.
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

// applyToTransport installs the plan's routing on an HTTP transport: the HTTP
// family through the Proxy hook, and a SOCKS endpoint through the dialer, since
// `Transport.Proxy` cannot express the latter.
//
// Installing only the dialer when SOCKS is the sole proxy is what makes a
// SOCKS-only machine work — without it every request would go direct and the
// setting would look honoured while routing nothing.
func (p proxyPlan) applyToTransport(transport *http.Transport) error {
	transport.Proxy = p.proxyFunc()
	dialer, err := p.socksDialer()
	if err != nil {
		return err
	}
	if dialer == nil {
		return nil
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return fmt.Errorf("SOCKS dialer does not support context cancellation")
	}
	transport.DialContext = contextDialer.DialContext
	return nil
}

// proxyFunc routes the desktop updater's requests the same way the child
// process routes its own. Resolving per request keeps a bypass list and a
// scheme split working, which a single static proxy URL could not express.
//
// SOCKS is deliberately absent here: `Transport.Proxy` speaks HTTP CONNECT
// only, so a socks5:// URL returned from this hook would be handed to the HTTP
// proxy code and fail. {@link socksDialer} covers that scheme instead.
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
		if raw == "" || isSOCKSProxyURL(raw) {
			return nil, nil
		}
		return url.Parse(raw)
	}
}

// isSOCKSProxyURL reports whether a proxy URL names a SOCKS endpoint, which the
// HTTP transport cannot use and a dedicated dialer must.
func isSOCKSProxyURL(value string) bool {
	address, err := url.Parse(value)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(address.Scheme)
	return scheme == "socks" || scheme == "socks5" || scheme == "socks5h"
}

// socksDialer builds a dialer that reaches every host through the plan's SOCKS
// endpoint, or returns nil when the plan configures none.
//
// The returned dialer bypasses the same hosts the bypass list names, so a
// SOCKS-only plan does not start tunnelling the loopback traffic the Harness
// depends on.
func (p proxyPlan) socksDialer() (proxy.Dialer, error) {
	raw := firstNonEmpty(p.SOCKS, socksFrom(p.All))
	if raw == "" {
		return nil, nil
	}
	address, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS proxy %q: %w", raw, err)
	}
	host := address.Host
	if host == "" {
		return nil, fmt.Errorf("SOCKS proxy %q names no host", raw)
	}
	var auth *proxy.Auth
	if address.User != nil {
		password, _ := address.User.Password()
		auth = &proxy.Auth{User: address.User.Username(), Password: password}
	}
	dialer, err := proxy.SOCKS5("tcp", host, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("build SOCKS dialer for %q: %w", raw, err)
	}
	return &bypassingDialer{delegate: dialer, bypass: p.NoProxy}, nil
}

// socksFrom returns a SOCKS URL unchanged and any other URL as empty, so a plan
// whose ALL_PROXY holds an http:// endpoint is not mistaken for SOCKS.
func socksFrom(value string) string {
	if isSOCKSProxyURL(value) {
		return value
	}
	return ""
}

// bypassingDialer sends bypassed hosts straight out and everything else through
// the wrapped dialer.
type bypassingDialer struct {
	delegate proxy.Dialer
	bypass   []string
}

// DialContext implements proxy.ContextDialer so a transport can honour request
// cancellation and its own dial timeout.
func (d *bypassingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if bypassesProxy(host, d.bypass) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, address)
	}
	if contextDialer, ok := d.delegate.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, network, address)
	}
	return d.delegate.Dial(network, address)
}

// Dial implements proxy.Dialer.
func (d *bypassingDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
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

// childPathPrefixes returns the runtime-relative directories that must lead
// the child's PATH. The desktop app launched from Finder or the Dock inherits
// launchd's minimal PATH (/usr/bin:/bin:/usr/sbin:/sbin), which contains none
// of the tooling the harness child relies on: plugins such as the plugin shop
// spawn a `dsh` CLI, `dsh plugin` spawns `pnpm`, and `pnpm`'s shebang resolves
// `node` through PATH. All three binaries ship inside the bundled runtime
// (node/npm/npx under root/node/bin, dsh under the app's node_modules/.bin),
// so prefixing those directories repairs the whole chain regardless of what
// the user's login shell would have provided.
func childPathPrefixes(root string) []string {
	prefixes := []string{}
	if runtime.GOOS == "windows" {
		// Windows keeps the node distribution directly under root/node.
		prefixes = append(prefixes, filepath.Join(root, "node"))
		return prefixes
	}
	prefixes = append(prefixes,
		filepath.Join(root, "node", "bin"),
		filepath.Join(root, "app", "node_modules", ".bin"),
	)
	return prefixes
}

// commonUnixToolBins lists the conventional macOS and Linux tool locations a
// login shell would normally have. They are appended (never prepended) after
// the runtime prefixes so a user-managed pnpm or similar tool stays reachable
// even though launchd stripped it from the inherited PATH.
var commonUnixToolBins = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// augmentChildPath returns env with PATH rebuilt so the bundled runtime's
// node and dsh binaries lead, followed by whatever PATH the app inherited,
// followed by the conventional Unix tool directories. Keeping the inherited
// value preserves anything a terminal-launched app would legitimately pass
// down; appending the conventional directories restores the toolchain a
// Finder/Dock launch lost.
func augmentChildPath(env []string, root string) []string {
	prefixes := childPathPrefixes(root)
	var inherited string
	for _, entry := range env {
		if name, value, found := strings.Cut(entry, "="); found && name == "PATH" {
			inherited = value
		}
	}
	merged := make([]string, 0, len(prefixes)+len(commonUnixToolBins)+1)
	seen := make(map[string]bool)
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		merged = append(merged, dir)
	}
	for _, dir := range prefixes {
		add(dir)
	}
	if inherited != "" {
		for _, dir := range filepath.SplitList(inherited) {
			add(dir)
		}
	}
	if runtime.GOOS != "windows" {
		for _, dir := range commonUnixToolBins {
			add(dir)
		}
	}
	rebuilt := "PATH=" + strings.Join(merged, string(filepath.ListSeparator))
	augmented := make([]string, 0, len(env)+1)
	wrote := false
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found && name == "PATH" {
			augmented = append(augmented, rebuilt)
			wrote = true
			continue
		}
		augmented = append(augmented, entry)
	}
	if !wrote {
		augmented = append(augmented, rebuilt)
	}
	return augmented
}
