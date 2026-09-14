//go:build linux

package main

import (
	"os/exec"
	"strings"
)

// detectSystemProxy reads the GNOME/desktop proxy configuration through
// `gsettings`. Desktop Linux has no single proxy store, so this covers the
// common GNOME/Unity/Cinnamon stack and reports "nothing detected" everywhere
// else, where exported HTTP_PROXY variables are the norm anyway.
func detectSystemProxy() systemProxy {
	mode, ok := gsettingsValue("org.gnome.system.proxy", "mode")
	if !ok || !strings.Contains(mode, "manual") {
		return systemProxy{}
	}
	proxy := systemProxy{
		HTTP:  gnomeProxy("http"),
		HTTPS: gnomeProxy("https"),
		SOCKS: gnomeProxy("socks"),
	}
	if !proxy.detected() {
		return systemProxy{}
	}
	if ignore, ok := gsettingsValue("org.gnome.system.proxy", "ignore-hosts"); ok {
		proxy.NoProxy = splitProxyList(strings.NewReplacer("[", " ", "]", " ", "'", " ", "\"", " ").Replace(ignore))
	}
	proxy.Source = "GNOME 系统代理"
	return proxy
}

// gsettingsValue reads one key, returning the unquoted value gsettings prints.
func gsettingsValue(schema, key string) (string, bool) {
	output, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return "", false
	}
	value := strings.Trim(strings.TrimSpace(string(output)), "'\"")
	return value, value != ""
}

// gnomeProxy assembles one scheme's host and port into a proxy URL.
func gnomeProxy(scheme string) string {
	host, hostFound := gsettingsValue("org.gnome.system.proxy."+scheme, "host")
	port, portFound := gsettingsValue("org.gnome.system.proxy."+scheme, "port")
	if !hostFound || !portFound {
		return ""
	}
	urlScheme := "http"
	if scheme == "socks" {
		urlScheme = "socks5"
	}
	return proxyURL(urlScheme, host, port)
}
