//go:build linux || darwin

package pty

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Pair is a pseudo-terminal: the master the daemon reads and writes, and
// the slave the attach command gets as its terminal.
type Pair struct {
	Master, Slave *os.File
}

// Open opens a pty with x/sys alone, so the daemon needs no pty library
// and no cgo. The per-OS step, ptsName, grants and unlocks the master and
// names its slave.
func Open() (*Pair, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	// A non-blocking master joins the runtime poller, so closing it ends a
	// read in progress; a blocking one would leave the reader stuck.
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("set pty master non-blocking: %w", err)
	}
	master := os.NewFile(uintptr(fd), "/dev/ptmx")
	name, err := ptsName(fd)
	if err != nil {
		_ = master.Close()
		return nil, err
	}
	sfd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = master.Close()
		return nil, fmt.Errorf("open pty slave %s: %w", name, err)
	}
	return &Pair{Master: master, Slave: os.NewFile(uintptr(sfd), name)}, nil
}

// Resize sets the window size the slave reports. The kernel signals the
// foreground process group, so the attached client redraws. It goes through
// SyscallConn: File.Fd would flip the master to blocking mode and race with a
// close.
func (p *Pair) Resize(rows, cols uint16) error {
	rc, err := p.Master.SyscallConn()
	if err != nil {
		return fmt.Errorf("set pty size: %w", err)
	}
	ws := &unix.Winsize{Row: rows, Col: cols}
	var ioerr error
	if err := rc.Control(func(fd uintptr) {
		ioerr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, ws) //nolint:gosec // a file descriptor number fits an int
	}); err != nil {
		return fmt.Errorf("set pty size: %w", err)
	}
	if ioerr != nil {
		return fmt.Errorf("set pty size: %w", ioerr)
	}
	return nil
}
