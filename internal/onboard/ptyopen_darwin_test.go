//go:build darwin

package onboard

import (
	"bytes"
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
	for _, req := range []uintptr{0x20007454, 0x20007452} { // TIOCPTYGRANT, TIOCPTYUNLK
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), req, 0); e != 0 {
			m.Close()
			return nil, nil, e
		}
	}
	var name [128]byte
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), 0x40807453, uintptr(unsafe.Pointer(&name[0]))); e != 0 { // TIOCPTYGNAME
		m.Close()
		return nil, nil, e
	}
	s, err := os.OpenFile(string(name[:bytes.IndexByte(name[:], 0)]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}
