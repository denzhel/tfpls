//go:build linux

package onboard

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// openPty returns the master and the slave side of a new pseudo-terminal.
func openPty() (master, slave *os.File, err error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), 0x40045431, uintptr(unsafe.Pointer(&unlock))); e != 0 { // TIOCSPTLCK
		m.Close()
		return nil, nil, e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), 0x80045430, uintptr(unsafe.Pointer(&n))); e != 0 { // TIOCGPTN
		m.Close()
		return nil, nil, e
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}
