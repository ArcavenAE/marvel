package tmux

import (
	"os"
	"syscall"
	"testing"
)

// writeExecutable writes a fake tmux (or any script the test will exec) and
// fails the test if it cannot. Every test in this package that writes a script
// and then execs it goes through here (marvel#1554).
//
// The write holds syscall.ForkLock for reading, as the syscall package asks of
// code that opens a descriptor. Go's fork takes the lock for writing, so no
// child is forked while the script is open for writing. Without it, a child
// that a parallel test forks in that window inherits the write descriptor
// until it execs, and exec of the script fails with "text file busy". The
// same shape was fixed for one scripted driver in marvel#1293 by retrying the
// exec; here the window is closed instead, so no retry or sleep is needed.
func writeExecutable(t *testing.T, path string, body []byte) {
	t.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, body, 0o755)
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
}
