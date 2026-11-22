//go:build linux || darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// terminalWidth returns the number of columns of the terminal f is, or 0.
func terminalWidth(f *os.File) int {
	var ws struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.cols)
}
