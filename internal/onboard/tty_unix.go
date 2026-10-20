//go:build linux || darwin

package onboard

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal (isatty): a character device alone is not enough, /dev/null is one.
func isTerminal(f *os.File) bool {
	var buf [128]byte
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlReadTermios, uintptr(unsafe.Pointer(&buf[0])))
	return e == 0
}

// writable reports whether the current user may write path (access(2) with W_OK).
func writable(path string) bool { return syscall.Access(path, 2) == nil }
