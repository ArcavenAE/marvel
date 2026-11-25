//go:build linux

package daemon

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// ptsName unlocks the master and returns the path of its slave.
func ptsName(master int) (string, error) {
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		return "", fmt.Errorf("unlock pty: %w", err)
	}
	n, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		return "", fmt.Errorf("name pty: %w", err)
	}
	return fmt.Sprintf("/dev/pts/%d", n), nil
}
