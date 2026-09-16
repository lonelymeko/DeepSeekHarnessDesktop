//go:build windows

package main

import "syscall"

// detachedProcessAttributes keeps the helper alive after the application exits,
// which is what it waits for before replacing the installed files.
func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
}
