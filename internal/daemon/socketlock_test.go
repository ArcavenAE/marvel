package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this lock closes: a second daemon on the same path took the
// socket from the first with no guard message, because the pid-file
// guard is HOME-scoped and the socket was machine-global. Verified
// 2026-08-06 in an isolated rig. See aae-orc-t6da.
func TestSocketLockRefusesASecondHolder(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "marvel.sock")

	first, err := lockSocketPath(sock)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	t.Cleanup(first.release)

	second, err := lockSocketPath(sock)
	if err == nil {
		second.release()
		t.Fatal("second lock succeeded; it must not")
	}

	// The error has to send the operator somewhere useful, since the
	// failure it replaces produced no message at all.
	for _, want := range []string{sock, "--socket"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// A released lock is reacquirable, which is what makes a daemon restart
// on the same path work.
func TestSocketLockReacquirableAfterRelease(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "marvel.sock")

	first, err := lockSocketPath(sock)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	first.release()

	second, err := lockSocketPath(sock)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	second.release()
}

// Two different socket paths are independent. Without this the lock
// would reintroduce the machine-global coupling it exists to remove.
func TestSocketLockIsPerPath(t *testing.T) {
	dir := t.TempDir()

	a, err := lockSocketPath(filepath.Join(dir, "a.sock"))
	if err != nil {
		t.Fatalf("lock a: %v", err)
	}
	t.Cleanup(a.release)

	b, err := lockSocketPath(filepath.Join(dir, "b.sock"))
	if err != nil {
		t.Fatalf("lock b, which must not collide with a: %v", err)
	}
	t.Cleanup(b.release)
}

// release must not delete the lock file. Removing it would race a
// daemon that has already opened it and is about to flock, which would
// hand the lock to two processes at once.
func TestSocketLockFileSurvivesRelease(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "marvel.sock")

	l, err := lockSocketPath(sock)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	l.release()

	if _, err := os.Stat(sock + ".lock"); err != nil {
		t.Fatalf("lock file gone after release: %v", err)
	}
}

// release on a nil or already-released lock is a no-op, because
// shutdown runs it unconditionally including on the TCP path where no
// lock was ever taken.
func TestSocketLockReleaseIsSafeWhenUnheld(t *testing.T) {
	var nilLock *socketLock
	nilLock.release()

	l, err := lockSocketPath(filepath.Join(t.TempDir(), "marvel.sock"))
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	l.release()
	l.release()
}
