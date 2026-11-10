package runtime

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The type check after the open is what refuses a file that is not a regular
// one. The open flags alone do not: a FIFO with no writer opens without
// blocking and then reads as an empty file, and a character device does the
// same, so without the check either would come back as a clean empty tail.

func TestReadGooseStderrTailRefusesAFIFO(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), launchFile)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	tail, _, err := readGooseStderrTail(path)
	if err == nil {
		t.Fatalf("a FIFO was read as a file, tail %q", tail)
	}
}

func TestReadGooseStderrTailRefusesACharacterDevice(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(os.DevNull); err != nil {
		t.Skipf("no %s here: %v", os.DevNull, err)
	}
	tail, _, err := readGooseStderrTail(os.DevNull)
	if err == nil {
		t.Fatalf("a character device was read as a file, tail %q", tail)
	}
}
