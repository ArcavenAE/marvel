//go:build darwin

package daemon

import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ptsName grants and unlocks the master and returns the path of its slave.
// x/sys has no wrapper for TIOCPTYGNAME, which fills a caller buffer, so the
// ioctl goes through the standard library's syscall package.
func ptsName(master int) (string, error) {
	if err := unix.IoctlSetInt(master, unix.TIOCPTYGRANT, 0); err != nil {
		return "", fmt.Errorf("grant pty: %w", err)
	}
	if err := unix.IoctlSetInt(master, unix.TIOCPTYUNLK, 0); err != nil {
		return "", fmt.Errorf("unlock pty: %w", err)
	}
	var buf [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(master), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))) //nolint:gosec // the ioctl writes the slave name into buf
	if errno != 0 {
		return "", fmt.Errorf("name pty: %w", errno)
	}
	n := bytes.IndexByte(buf[:], 0)
	if n <= 0 {
		return "", fmt.Errorf("name pty: the kernel returned no name")
	}
	return string(buf[:n]), nil
}
