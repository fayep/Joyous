//go:build !windows

package main

// sockFD converts a raw socket descriptor to the type syscall's setsockopt
// calls take on this platform.
func sockFD(raw uintptr) int { return int(raw) }
