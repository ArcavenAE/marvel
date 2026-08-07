package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// socketLock is an advisory lock held for as long as a daemon owns a
// control socket path. Its purpose is to make the unlink-then-listen
// sequence safe.
//
// Before it, Start removed the socket unconditionally and discarded the
// error, so a second daemon starting on the same path took it from the
// first, and a second daemon EXITING on the same path unlinked the
// survivor's socket and left it alive, holding state, and unreachable
// by its own address. Nothing in the survivor's log recorded it. The
// daemon did not know it had been stranded.
//
// flock rather than a connect-probe: a probe has a TOCTOU window
// between "nothing answered" and "I bound", and two daemons starting
// together can both pass it. The kernel releases an flock when the
// holding process dies by any means, so a crashed daemon does not leave
// the path permanently unusable, which a lock file holding a pid would.
//
// This is separate from the pid-file guard. That one is HOME-scoped and
// checks a recorded pid; this one is scoped to the socket path itself,
// wherever the operator put it. See docs/design/daemon-isolation.md.
//
// No build tag. syscall.Flock is unix-only, and so is this package
// already: it imports internal/runtime, which needs syscall.Mkfifo.
// Marvel ships linux and darwin (.goreleaser.yml), and tmux, the session
// substrate, does not exist off unix. A fallback here would document a
// portability the tree does not have.
type socketLock struct {
	file *os.File
	path string
}

// lockSocketPath takes the lock guarding socketPath, or reports who has
// it. The lock file sits beside the socket and is never removed: after
// release, an unlocked lock file is exactly as safe as no lock file, and
// removing it would race a daemon that has just opened it.
func lockSocketPath(socketPath string) (*socketLock, error) {
	lockPath := socketPath + ".lock"

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open socket lock %s: %w", lockPath, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf(
				"another marvel daemon is already using socket %s "+
					"(lock held on %s). Stop it, or start this one with a "+
					"different --socket", socketPath, lockPath)
		}
		return nil, fmt.Errorf("lock %s: %w", lockPath, err)
	}

	return &socketLock{file: f, path: lockPath}, nil
}

// release drops the lock. The kernel would do this at process exit
// anyway; doing it explicitly means a Detach followed by a re-Start in
// the same process (which reexec does not do, but tests do) works.
func (l *socketLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
