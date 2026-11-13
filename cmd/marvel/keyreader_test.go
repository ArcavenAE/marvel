package main

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pipePair is a pipe both ends of which the test closes.
func pipePair(t *testing.T) (r, w int) {
	t.Helper()
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(p[0]); _ = unix.Close(p[1]) })
	return p[0], p[1]
}

func writeBytes(t *testing.T, fd int, s string) {
	t.Helper()
	if _, err := unix.Write(fd, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

// readableNow is a poll on fd with no wait, by the real poll.
func readableNow(t *testing.T, fd int) bool {
	t.Helper()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	if err != nil {
		t.Fatal(err)
	}
	return n > 0 && fds[0].Revents&unix.POLLIN != 0
}

func recvKey(t *testing.T, r *keyReader) byte {
	t.Helper()
	select {
	case k := <-r.Keys():
		return k
	case <-time.After(2 * time.Second):
		t.Fatal("no key within 2s")
		return 0
	}
}

func noKey(t *testing.T, r *keyReader, within time.Duration) {
	t.Helper()
	select {
	case k := <-r.Keys():
		t.Fatalf("got key %q, want none", k)
	case <-time.After(within):
	}
}

// within runs f and fails the test if it does not return in a second.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("%s did not return within 1s", what)
	}
}

func startReader(t *testing.T) (r *keyReader, stdinW int) {
	t.Helper()
	in, w := pipePair(t)
	kr, err := newKeyReader(in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kr.Close)
	return kr, w
}

func TestWaitReadableReportsInputWakeAndTimeout(t *testing.T) {
	cases := []struct {
		name          string
		input, wake   bool
		wantIn, wantW bool
	}{
		{"nothing", false, false, false, false},
		{"input", true, false, true, false},
		{"wake", false, true, false, true},
		{"both", true, true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inR, inW := pipePair(t)
			wR, wW := pipePair(t)
			if c.input {
				writeBytes(t, inW, "x")
			}
			if c.wake {
				writeBytes(t, wW, "w")
			}
			in, woke, err := waitReadable(inR, wR, 50*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if in != c.wantIn || woke != c.wantW {
				t.Errorf("in=%v woke=%v, want in=%v woke=%v", in, woke, c.wantIn, c.wantW)
			}
		})
	}
}

func TestWaitReadableFallsBackToSelect(t *testing.T) {
	for name, poll := range map[string]func([]unix.PollFd, int) (int, error){
		"poll says the descriptor is invalid": func(fds []unix.PollFd, _ int) (int, error) {
			for i := range fds {
				fds[i].Revents = unix.POLLNVAL
			}
			return len(fds), nil
		},
		"poll fails": func([]unix.PollFd, int) (int, error) { return -1, unix.EBADF },
	} {
		t.Run(name, func(t *testing.T) {
			prevPoll, prevSel := pollFn, selectFn
			selects := 0
			pollFn = poll
			selectFn = func(n int, r, w, e *unix.FdSet, tv *unix.Timeval) (int, error) {
				selects++
				return prevSel(n, r, w, e, tv)
			}
			t.Cleanup(func() { pollFn, selectFn = prevPoll, prevSel })

			inR, inW := pipePair(t)
			wR, _ := pipePair(t)
			writeBytes(t, inW, "x")
			in, woke, err := waitReadable(inR, wR, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if selects == 0 {
				t.Error("select was not used after poll failed")
			}
			if !in || woke {
				t.Errorf("in=%v woke=%v, want input only", in, woke)
			}
		})
	}
}

func TestReadinessHelperRejectsNonTty(t *testing.T) {
	r, _ := pipePair(t)
	if err := requireTerminal(r); !errors.Is(err, errNeedTerminal) {
		t.Errorf("requireTerminal(pipe) = %v, want errNeedTerminal", err)
	}
}

func TestKeyReaderDeliversKeysInOrder(t *testing.T) {
	r, w := startReader(t)
	writeBytes(t, w, "abc")
	for _, want := range []byte("abc") {
		if got := recvKey(t, r); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	writeBytes(t, w, "d")
	if got := recvKey(t, r); got != 'd' {
		t.Fatalf("got %q, want d", got)
	}
}

func TestKeyReaderPauseConsumesNothing(t *testing.T) {
	r, w := startReader(t)
	writeBytes(t, w, "x")
	if got := recvKey(t, r); got != 'x' {
		t.Fatalf("got %q, want x", got)
	}
	within(t, "Pause", r.Pause)

	writeBytes(t, w, "y")
	noKey(t, r, 100*time.Millisecond)
	stdin := r.stdinFD()
	if !readableNow(t, stdin) {
		t.Fatal("the byte written after the pause was taken from stdin")
	}
	buf := make([]byte, 1)
	if n, err := unix.Read(stdin, buf); err != nil || n != 1 || buf[0] != 'y' {
		t.Fatalf("read %d %q %v, want the byte y still on stdin", n, buf, err)
	}

	r.Resume()
	writeBytes(t, w, "z")
	if got := recvKey(t, r); got != 'z' {
		t.Fatalf("after Resume got %q, want z", got)
	}
}

func TestKeyReaderResumeDeliversWhatWaited(t *testing.T) {
	r, w := startReader(t)
	within(t, "Pause", r.Pause)
	writeBytes(t, w, "q")
	noKey(t, r, 50*time.Millisecond)
	r.Resume()
	if got := recvKey(t, r); got != 'q' {
		t.Fatalf("got %q, want q", got)
	}
}

// A key read while nobody receives is held, not lost, and does not stop the
// pause: the watch loop is not receiving while it hands the terminal over.
func TestKeyReaderPauseWithAnUndeliveredKey(t *testing.T) {
	r, w := startReader(t)
	writeBytes(t, w, "k")
	time.Sleep(100 * time.Millisecond) // the reader has read k and waits to hand it over
	within(t, "Pause with a key waiting", r.Pause)
	noKey(t, r, 50*time.Millisecond)
	r.Resume()
	if got := recvKey(t, r); got != 'k' {
		t.Fatalf("got %q, want the held key k", got)
	}
}

func TestKeyReaderPauseResumeCycles(t *testing.T) {
	r, w := startReader(t)
	for i, k := range []byte("123") {
		within(t, "Pause", r.Pause)
		r.Resume()
		writeBytes(t, w, string(k))
		if got := recvKey(t, r); got != k {
			t.Fatalf("cycle %d: got %q, want %q", i, got, k)
		}
	}
}

func TestWakeByteIsDrainedAndTheReaderDoesNotSpin(t *testing.T) {
	r, _ := startReader(t)
	if r.wakeR <= 0 {
		t.Fatal("the reader has no wake pipe")
	}
	time.Sleep(50 * time.Millisecond)
	if r.waits.Load() == 0 {
		t.Fatal("the running reader never waited for input")
	}
	within(t, "Pause", r.Pause)
	before := r.waits.Load()
	time.Sleep(150 * time.Millisecond)
	if spun := r.waits.Load() - before; spun > 2 {
		t.Errorf("a paused reader waited %d times in 150ms, want no spin", spun)
	}
	if readableNow(t, r.wakeR) {
		t.Error("the wake byte is still in the pipe after the pause was acknowledged")
	}
	r.Resume()
	time.Sleep(50 * time.Millisecond)
	if readableNow(t, r.wakeR) {
		t.Error("a wake byte is left in the pipe after Resume")
	}
}

func TestKeyReaderCloseStopsIt(t *testing.T) {
	for _, paused := range []bool{false, true} {
		r, _ := startReader(t)
		if paused {
			within(t, "Pause", r.Pause)
		}
		within(t, "Close", r.Close)
		select {
		case <-r.done:
		case <-time.After(time.Second):
			t.Errorf("paused=%v: the reader goroutine did not stop after Close", paused)
		}
	}
}
