//go:build windows

package main

import "syscall"

// sockFD converts a raw socket descriptor to the type syscall's setsockopt
// calls take on this platform.
func sockFD(raw uintptr) syscall.Handle { return syscall.Handle(raw) }
