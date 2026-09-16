//go:build !windows

package main

import "syscall"

// detachedProcessAttributes starts the helper in its own session, so it
// survives this process exiting — which is precisely what it waits for.
func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
