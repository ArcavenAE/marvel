package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The reap reads a file whose name the seat can influence: it lives in the
// seat's own home. A FIFO there would block the open, and the reap runs under
// the controller's mutex, so it would stall every reconcile. A link there would
// make the daemon read a file the sandbox keeps from the seat. Neither may
// happen, and a name that is not the shape the adapter writes is not a launch
// file at all.

const launchFile = "stderr-20261103T050000.000000000Z.log"

func tailWithin(t *testing.T, home string) (string, error) {
	t.Helper()
	type result struct {
		tail string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		tail, err := GooseStderrTail(home)
		done <- result{tail, err}
	}()
	select {
	case r := <-done:
		return r.tail, r.err
	case <-time.After(10 * time.Second):
		t.Fatal("GooseStderrTail blocked")
		return "", nil
	}
}

func TestGooseStderrTailDoesNotBlockOnAFIFO(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(home, launchFile), 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	tail, _ := tailWithin(t, home)
	if tail != "" {
		t.Fatalf("a FIFO yielded the tail %q", tail)
	}
}

func TestGooseStderrTailRefusesALinkedFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("a file the seat may not read\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, launchFile)); err != nil {
		t.Fatal(err)
	}
	tail, err := tailWithin(t, home)
	if strings.Contains(tail, "may not read") {
		t.Fatalf("the tail read through a link: %q", tail)
	}
	if err == nil {
		t.Error("a link at the launch file was not reported")
	}
}

// A name that matches the glob but not the shape the adapter writes sorts
// after every real launch file, so choosing the newest by name alone lets a
// planted name win.
func TestGooseStderrTailOnlyReadsTheShapeTheAdapterWrites(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for name, body := range map[string]string{
		launchFile:       "real launch\n",
		"stderr-zzz.log": "planted by the seat\n",
	} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tail, err := tailWithin(t, home)
	if err != nil {
		t.Fatalf("GooseStderrTail: %v", err)
	}
	if !strings.Contains(tail, "real launch") || strings.Contains(tail, "planted") {
		t.Fatalf("tail = %q, want the launch file and not the planted name", tail)
	}
}

func TestGooseStderrNameMatchesItsOwnShape(t *testing.T) {
	t.Parallel()
	if !gooseStderrShape.MatchString(gooseStderrName()) {
		t.Fatalf("the name the adapter writes, %q, does not match the shape the reap reads", gooseStderrName())
	}
}
