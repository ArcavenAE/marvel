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

// scratchEnv keeps a test from reaching the operator's home, the daemon or
// the tmux the seat runs in. The reader does not touch any of them; the
// children slice 3 starts will, and these tests set the pattern.
func scratchEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MARVEL_SOCKET", "")
	t.Setenv("TMUX", "")
}

func startReader(t *testing.T) (r *keyReader, stdinW int) {
	t.Helper()
	scratchEnv(t)
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
	if spun := r.waits.Load() - before; spun != 0 {
		t.Errorf("a paused reader waited %d times in 150ms, want none", spun)
	}
	if readableNow(t, r.wakeR) {
		t.Error("the wake byte is still in the pipe after the pause was acknowledged")
	}
	r.Resume()
	time.Sleep(50 * time.Millisecond)
	if readableNow(t, r.wakeR) {
		t.Error("a wake byte is left in the pipe after Resume")
	}
	// Residual: a paused reader that spun without ever calling waitReadable
	// (a busy loop on a flag) is not seen by this count, and CPU time was
	// dropped as a check because getrusage covers the whole test process.
	// Running and idle, the reader sits in one wait. An undrained wake byte
	// would make that wait return at once, again and again.
	before = r.waits.Load()
	time.Sleep(150 * time.Millisecond)
	if spun := r.waits.Load() - before; spun > 1 {
		t.Errorf("an idle reader waited %d times in 150ms after Resume, want at most one in flight", spun)
	}
}

func TestIdleReaderDoesNotSpin(t *testing.T) {
	r, _ := startReader(t)
	time.Sleep(50 * time.Millisecond)
	before := r.waits.Load()
	if before == 0 {
		t.Fatal("the running reader never waited for input")
	}
	time.Sleep(150 * time.Millisecond)
	if spun := r.waits.Load() - before; spun > 1 {
		t.Errorf("an idle reader waited %d times in 150ms, want at most one in flight", spun)
	}
}

// A reader holds at most the one byte it read. Taking more from stdin than it
// can hand over would eat keystrokes meant for the child that gets the
// terminal while it is paused.
func TestKeyReaderTakesOneByteAtATime(t *testing.T) {
	r, w := startReader(t)
	writeBytes(t, w, "kyz")
	time.Sleep(100 * time.Millisecond) // the reader has read k and waits to hand it over
	within(t, "Pause with a key waiting", r.Pause)

	stdin := r.stdinFD()
	if !readableNow(t, stdin) {
		t.Fatal("nothing is left on stdin: the reader took more than one byte")
	}
	buf := make([]byte, 4)
	n, err := unix.Read(stdin, buf)
	if err != nil || string(buf[:n]) != "yz" {
		t.Fatalf("stdin held %q (err %v), want yz", buf[:n], err)
	}
	r.Resume()
	if got := recvKey(t, r); got != 'k' {
		t.Fatalf("got %q, want the held key k", got)
	}
}

func TestWaitReadableRetriesInterruptedPollWithoutSelect(t *testing.T) {
	prevPoll, prevSel := pollFn, selectFn
	calls, selects := 0, 0
	pollFn = func(fds []unix.PollFd, ms int) (int, error) {
		calls++
		if calls == 1 {
			return -1, unix.EINTR
		}
		return prevPoll(fds, ms)
	}
	selectFn = func(n int, r, w, e *unix.FdSet, tv *unix.Timeval) (int, error) {
		selects++
		return prevSel(n, r, w, e, tv)
	}
	t.Cleanup(func() { pollFn, selectFn = prevPoll, prevSel })

	inR, inW := pipePair(t)
	wR, _ := pipePair(t)
	writeBytes(t, inW, "x")
	in, woke, err := waitReadable(inR, wR, time.Second)
	if err != nil || !in || woke {
		t.Fatalf("in=%v woke=%v err=%v, want input only", in, woke, err)
	}
	if calls < 2 || selects != 0 {
		t.Errorf("poll calls=%d select calls=%d, want the interrupted poll retried and select unused", calls, selects)
	}
}

func TestWaitReadableKeepsTheTimeoutAcrossAnInterruptedPoll(t *testing.T) {
	prev := pollFn
	var waits []int
	pollFn = func(fds []unix.PollFd, ms int) (int, error) {
		waits = append(waits, ms)
		if len(waits) == 1 {
			time.Sleep(60 * time.Millisecond)
			return -1, unix.EINTR
		}
		return prev(fds, ms)
	}
	t.Cleanup(func() { pollFn = prev })

	inR, _ := pipePair(t)
	wR, _ := pipePair(t)
	start := time.Now()
	in, woke, err := waitReadable(inR, wR, 200*time.Millisecond)
	if err != nil || in || woke {
		t.Fatalf("in=%v woke=%v err=%v, want a timeout", in, woke, err)
	}
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Errorf("the wait took %v after an interrupted poll, want about the 200ms asked for", took)
	}
	if len(waits) < 2 || waits[1] >= waits[0] {
		t.Errorf("poll was asked for %v ms, want the retry to ask for less than the first", waits)
	}
}

func TestWaitReadableZeroTimeoutHasNoBound(t *testing.T) {
	inR, inW := pipePair(t)
	wR, _ := pipePair(t)
	type result struct{ in, woke bool }
	out := make(chan result, 1)
	go func() {
		in, woke, _ := waitReadable(inR, wR, 0)
		out <- result{in, woke}
	}()
	select {
	case got := <-out:
		t.Fatalf("a zero timeout returned at once with %+v, want it to wait", got)
	case <-time.After(100 * time.Millisecond):
	}
	writeBytes(t, inW, "x")
	select {
	case got := <-out:
		if !got.in {
			t.Errorf("woke without input: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the wait did not end when input arrived")
	}
}

func TestKeyReaderPauseAndResumeAreIdempotent(t *testing.T) {
	r, w := startReader(t)
	within(t, "Resume without a Pause", r.Resume)
	within(t, "first Pause", r.Pause)
	within(t, "second Pause", r.Pause)
	r.Resume()
	within(t, "second Resume", r.Resume)
	writeBytes(t, w, "a")
	if got := recvKey(t, r); got != 'a' {
		t.Fatalf("got %q, want a", got)
	}
}

func TestCloseReleasesTheWakePipe(t *testing.T) {
	r, _ := startReader(t)
	if r.wakeR <= 0 {
		t.Fatal("the reader has no wake pipe")
	}
	wake := r.wakeR
	var before unix.Stat_t
	if err := unix.Fstat(wake, &before); err != nil {
		t.Fatal(err)
	}
	within(t, "Close", r.Close)
	// The number may be reused by a descriptor another test opened; then it
	// names a different file, and the pipe's own inode is gone.
	var after unix.Stat_t
	if err := unix.Fstat(wake, &after); err == nil && after.Ino == before.Ino {
		t.Errorf("the wake pipe (inode %d) is still open after Close", before.Ino)
	} else if err != nil && !errors.Is(err, unix.EBADF) {
		t.Errorf("fstat on the wake pipe after Close = %v, want EBADF or another file", err)
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

// A wake with no pause behind it, such as a byte a pause left in the pipe,
// must not park the reader: nothing would ever Resume it, and every key after
// it would be stranded.
func TestSpuriousWakeDoesNotParkTheReader(t *testing.T) {
	r, w := startReader(t)
	if _, err := unix.Write(r.wakeW, []byte{1}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	writeBytes(t, w, "k")
	if got := recvKey(t, r); got != 'k' {
		t.Fatalf("got %q after a stray wake byte, want k", got)
	}
}

// A pause and a resume, then a key, again and again with a receiver that is
// always ready. A request left over from one pause must not park the reader
// at a later key: that strands every key, q included, until Close.
func TestKeyReaderLosesNoKeyAcrossManyPauseCycles(t *testing.T) {
	const cycles = 150000
	r, w := startReader(t)
	failed := make(chan int, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < cycles; i++ {
			r.Pause()
			r.Resume()
			if _, err := unix.Write(w, []byte{'k'}); err != nil {
				failed <- -1
				return
			}
			select {
			case <-r.Keys():
			case <-time.After(2 * time.Second):
				failed <- i
				return
			}
		}
	}()
	select {
	case <-finished:
	case <-time.After(90 * time.Second):
		t.Fatal("150000 pause cycles did not finish in 90s")
	}
	select {
	case i := <-failed:
		t.Fatalf("the key after pause cycle %d never arrived within 2s: the reader parked with nothing to resume it", i)
	default:
	}
}
