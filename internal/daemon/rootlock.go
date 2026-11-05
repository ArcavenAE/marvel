package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockRoot takes the lock that makes one marvel root one daemon, or reports
// that another daemon holds it. lockPath is <root>/run/daemon.lock, set from
// the resolved layout and from nothing else: no flag and no env var can move
// it, because moving it would let two daemons share a root again.
//
// Two daemons under one root share the bus store and the broker's pid file,
// and nothing in --socket, --pidfile or --state-bolt ties them together, so
// the second one stopped the first one's broker (marvel#883). The pid file
// guard keys on --pidfile and the socket lock on --socket; this is the guard
// on the root itself. docs/design/marvel-home.md says why one root is one
// cluster.
//
// The shape is socketlock.go's: flock, never removed, released by the kernel
// when the holder dies by any means, so a crash leaves nothing stale. It is
// the same type as the socket lock, because the two differ only in the path.
func lockRoot(lockPath string) (*socketLock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open root lock %s: %w", lockPath, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			// <root>/run/daemon.lock: the root is two levels up.
			root := filepath.Dir(filepath.Dir(lockPath))
			return nil, fmt.Errorf(
				"another marvel daemon is already running under %s "+
					"(lock held on %s). One root is one cluster; start this "+
					"daemon with its own --marvel-home or MARVEL_HOME",
				root, lockPath)
		}
		return nil, fmt.Errorf("lock %s: %w", lockPath, err)
	}

	return &socketLock{file: f, path: lockPath}, nil
}
