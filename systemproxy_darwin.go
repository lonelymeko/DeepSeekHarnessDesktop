//go:build darwin

package main

import "os/exec"

// detectSystemProxy reads the proxy SystemConfiguration publishes for the
// current network set, through `scutil --proxy`, its command-line view. It
// returns an empty report when the command is unavailable or names no proxy.
func detectSystemProxy() systemProxy {
	output, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return systemProxy{}
	}
	return parseScutilProxy(string(output))
}
