package tmux

import (
	"syscall"
	"testing"
	"time"
)

// marvel#1554: a fake tmux that is still open for writing when a forked child
// execs cannot itself be exec'd (ETXTBSY). A child that Go forks while the
// file is open inherits the write descriptor until it execs. Go's fork takes
// syscall.ForkLock for writing, and code that opens a descriptor is meant to
// hold it for reading, so a file written under the read lock cannot be open at
// the moment of any fork. The race could not be hit on macOS (400 and 6000
// rounds of forking beside fakeTmux, no ETXTBSY), so this test pins the
// mechanism: while a fork is in progress, a fake tmux is not written.
func TestFakeTmuxIsNotWrittenWhileAForkIsInProgress(t *testing.T) {
	syscall.ForkLock.Lock() // a fork in progress holds this for writing
	released := false
	release := func() {
		if !released {
			released = true
			syscall.ForkLock.Unlock()
		}
	}
	defer release()

	written := make(chan struct{})
	go func() {
		fakeTmux(t)
		close(written)
	}()

	select {
	case <-written:
		t.Fatal("fakeTmux wrote the script while a fork held the fork lock; a child forked now would inherit its write descriptor")
	case <-time.After(300 * time.Millisecond):
	}

	release()
	select {
	case <-written:
	case <-time.After(5 * time.Second):
		t.Fatal("fakeTmux did not finish after the fork lock was released")
	}
}
