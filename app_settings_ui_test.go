package main

import (
	"strings"
	"testing"
)

func TestInjectDesktopSettings(t *testing.T) {
	document := []byte("<!doctype html><body><div id=\"root\"></div></body>")
	result := string(injectDesktopSettings(document, "darwin"))
	for _, expected := range []string{
		`id="dsh-desktop-settings-toggle"`,
		`id="dsh-desktop-settings"`,
		`id="dsh-desktop-settings-backdrop"`,
		`id="dsh-desktop-settings-proxy"`,
		`网络请求走系统代理`,
		`GetDesktopNetworkState`,
		`SetNetworkUseSystemProxy`,
		`GetDeepSeekOverview`,
		`DEEPSEEK_USER_TOKEN`,
		`/user/balance`,
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("injected settings surface lacks %q", expected)
		}
	}
	if strings.Index(result, "dsh-desktop-settings-toggle") > strings.Index(result, "</body>") {
		t.Fatalf("the settings surface must be injected before the closing body")
	}
}

func TestInjectDesktopSettingsAnchorsToInjectedChrome(t *testing.T) {
	document := []byte("<!doctype html><body><div id=\"root\"></div></body>")
	mac := string(injectDesktopSettings(document, "darwin"))
	if !strings.Contains(mac, "#dsh-desktop-titlebar #dsh-desktop-settings-toggle{position:absolute;top:0;right:8px;width:36px;height:44px") {
		t.Fatalf("macOS must anchor the gear inside the injected title bar: %s", mac)
	}
	windows := string(injectDesktopSettings(document, "windows"))
	if !strings.Contains(windows, "#dsh-desktop-titlebar #dsh-desktop-settings-toggle{position:absolute;top:0;right:116px;width:36px;height:32px") {
		t.Fatalf("Windows must anchor the gear clear of its window controls: %s", windows)
	}
	// Linux gets no injected title bar, so the gear must fall back to the
	// floating rule rather than being anchored to an element that never exists.
	linux := string(injectDesktopSettings(document, "linux"))
	if strings.Contains(linux, "#dsh-desktop-titlebar #dsh-desktop-settings-toggle") {
		t.Fatalf("a platform without injected chrome must not anchor the gear: %s", linux)
	}
	if !strings.Contains(linux, "#dsh-desktop-settings-toggle{position:fixed;top:12px;right:12px") {
		t.Fatalf("Linux must keep the floating gear rule: %s", linux)
	}
}

func TestInjectDesktopChromeReservesGearSpaceOnMacOS(t *testing.T) {
	document := []byte("<!doctype html><body><div id=\"root\"></div></body>")
	mac := string(injectDesktopChrome(document, "darwin"))
	// The gear occupies right:8px..44px in the title bar, so the drag region
	// must stop before it or the gear becomes undraggable chrome.
	if !strings.Contains(mac, "left:78px;right:48px") {
		t.Fatalf("macOS drag region does not reserve the gear's corner: %s", mac)
	}
}
