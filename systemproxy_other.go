//go:build !darwin && !windows && !linux

package main

// detectSystemProxy reports no proxy on platforms this shell does not package
// for, so the shared plan logic still compiles and simply follows the
// environment instead.
func detectSystemProxy() systemProxy { return systemProxy{} }
