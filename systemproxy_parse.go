package main

import (
	"net"
	"strconv"
	"strings"
)

// This file holds the platform detectors' pure halves: the text parsers that
// turn a command's output into a systemProxy. They carry no build tag so the
// suite exercises every platform's parsing on whichever machine runs it; only
// running the command itself stays platform-specific.

// parseScutilProxy reads the `<dictionary>` text `scutil --proxy` prints. The
// format is one `key : value` pair per line, with `ExceptionsList` as the only
// nested array, so a small line reader admits exactly what that command emits
// without pulling in a plist parser.
func parseScutilProxy(text string) systemProxy {
	values := map[string]string{}
	exceptions := []string{}
	inExceptions := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if key, ok := strings.CutSuffix(line, ": <array> {"); ok {
			inExceptions = strings.TrimSpace(key) == "ExceptionsList"
			continue
		}
		if line == "}" {
			inExceptions = false
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if inExceptions {
			if value != "" {
				exceptions = append(exceptions, value)
			}
			continue
		}
		values[key] = value
	}

	proxy := systemProxy{NoProxy: exceptions}
	// macOS proxies HTTPS through the same HTTP CONNECT endpoint, so an HTTPS
	// entry is an `http://` URL, never an `https://` one.
	if values["HTTPEnable"] == "1" {
		proxy.HTTP = proxyURL("http", values["HTTPProxy"], values["HTTPPort"])
	}
	if values["HTTPSEnable"] == "1" {
		proxy.HTTPS = proxyURL("http", values["HTTPSProxy"], values["HTTPSPort"])
	}
	if values["SOCKSEnable"] == "1" {
		proxy.SOCKS = proxyURL("socks5", values["SOCKSProxy"], values["SOCKSPort"])
	}
	// A PAC script is deliberately not resolved: fetching and evaluating one
	// would run third-party code, and every real client that honours PAC would
	// then disagree with this shell about the answer.
	if proxy.detected() {
		proxy.Source = "macOS 系统代理"
	}
	return proxy
}

// parseWindowsProxyRegistry reads `reg query` output: one `name  TYPE  value`
// record per line, indented under the key path the command echoes first.
func parseWindowsProxyRegistry(text string) systemProxy {
	values := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		switch strings.ToUpper(fields[1]) {
		case "REG_DWORD":
			if parsed, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(fields[2]), "0x"), 16, 64); err == nil {
				values[name] = strconv.FormatInt(parsed, 10)
			}
		case "REG_SZ", "REG_EXPAND_SZ":
			// A registered value may contain spaces, so the rest of the line is
			// the value rather than only its third field.
			values[name] = strings.Join(fields[2:], " ")
		}
	}

	proxy := systemProxy{}
	if values["ProxyEnable"] != "1" {
		return proxy
	}
	server := values["ProxyServer"]
	if server == "" {
		return proxy
	}
	if strings.Contains(server, "=") {
		// The per-scheme form: `http=host:port;https=host:port;socks=host:port`.
		for _, part := range strings.Split(server, ";") {
			scheme, address, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(scheme)) {
			case "http":
				proxy.HTTP = windowsProxyURL("http", address)
			case "https":
				proxy.HTTPS = windowsProxyURL("http", address)
			case "socks", "socks4", "socks5":
				proxy.SOCKS = windowsProxyURL("socks5", address)
			}
		}
	} else if address := windowsProxyURL("http", server); address != "" {
		// One endpoint for every scheme.
		proxy.HTTP = address
		proxy.HTTPS = address
	}
	if !proxy.detected() {
		return systemProxy{}
	}
	proxy.NoProxy = splitProxyList(values["ProxyOverride"])
	proxy.Source = "Windows 系统代理"
	return proxy
}

// windowsProxyURL admits a `host:port` WinINET value. A bare host is rejected:
// guessing a port would silently route traffic somewhere the user never named.
func windowsProxyURL(scheme, address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	return proxyURL(scheme, host, port)
}
