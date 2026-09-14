//go:build windows

package main

import "os/exec"

// windowsInternetSettingsKey holds the per-user WinINET proxy configuration,
// which is what every browser and Electron app on the machine reads.
const windowsInternetSettingsKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// detectSystemProxy reads the WinINET proxy configuration through `reg query`.
// It returns an empty report when the command is unavailable or proxying is off.
func detectSystemProxy() systemProxy {
	output, err := exec.Command("reg", "query", windowsInternetSettingsKey).Output()
	if err != nil {
		return systemProxy{}
	}
	return parseWindowsProxyRegistry(string(output))
}
