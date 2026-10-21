package daemonrpc

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testTimer is the per-call timer the tests set: short, so a test runs in
// well under a second, with the plan's 2 s of slack on top as the bound.
const testTimer = 300 * time.Millisecond

// callWithin runs call in a goroutine and fails the test if it has not
// returned within testTimer plus 2 s. A call that never returns is the
// failure on the parent, where nothing bounds the exchange after connect.
func callWithin(t *testing.T, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(testTimer + 2*time.Second):
		t.Fatalf("the call did not return within the %s timer plus 2s", testTimer)
		return nil
	}
}

// silentTCP accepts connections and never writes, holding each open until
// the test ends.
func silentTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 16)
	t.Cleanup(func() {
		_ = ln.Close()
		close(held)
		for c := range held {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c
		}
	}()
	return ln.Addr().String()
}

func wantTimeout(t *testing.T, err error, phase string) {
	t.Helper()
	var tmo *TimeoutError
	if !errors.As(err, &tmo) {
		t.Fatalf("err = %v (%T), want a TimeoutError", err, err)
	}
	if tmo.Phase != phase {
		t.Errorf("Phase = %q, want %q (err: %v)", tmo.Phase, phase, err)
	}
}

// TestTimerBoundsSilentBanner: a server that accepts TCP and never sends
// its banner is bounded by the timer, in the handshake phase.
func TestTimerBoundsSilentBanner(t *testing.T) {
	resetKnownHosts(t)
	_, pemBytes := newSigner(t)
	addr := "mrvl://u@" + silentTCP(t)
	opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: testTimer}
	err := callWithin(t, func() error {
		_, err := SendRequestWith(addr, Request{Method: "ping"}, opts)
		return err
	})
	wantTimeout(t, err, "handshake")
}

// TestTimerBoundsSilentSession: a server that completes the handshake and
// never answers the request is bounded by the timer, in the reply phase.
func TestTimerBoundsSilentSession(t *testing.T) {
	resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRigWith(t, client.PublicKey(), rigSilentReply)
	trustRigHost(t, rig, rig.hostKey)
	opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: testTimer}
	err := callWithin(t, func() error {
		_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
		return err
	})
	wantTimeout(t, err, "reply")
}

// TestTimerBoundsSilentUnixDaemon: the issue's case, a unix socket that
// listens and never accepts or answers.
func TestTimerBoundsSilentUnixDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "su")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	err = callWithin(t, func() error {
		_, err := SendRequestWith(sock, Request{Method: "ping"}, DialOptions{Timeout: testTimer})
		return err
	})
	wantTimeout(t, err, "reply")
}

// openFDs counts this process's open file descriptors. It lists the names
// without stat'ing them: os.ReadDir stats each entry, and on darwin that
// fails in a test binary with "bad file descriptor". An unreadable table
// fails the test rather than skipping it, because a skip lets the leak check
// pass without running.
func openFDs(t *testing.T) int {
	t.Helper()
	d, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatalf("open /dev/fd: %v", err)
	}
	defer func() { _ = d.Close() }()
	names, err := d.Readdirnames(-1)
	if err != nil {
		t.Fatalf("read /dev/fd: %v", err)
	}
	return len(names)
}

// settle waits, with a bounded number of yields, for the goroutine count to
// fall to at most want, and returns the last count seen.
func settle(want int) int {
	n := runtime.NumGoroutine()
	for i := 0; i < 200 && n > want; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

// TestTimerFreesEverything: once the timer has fired on a silent session,
// no goroutine and no file descriptor the call opened is left.
func TestTimerFreesEverything(t *testing.T) {
	resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRigWith(t, client.PublicKey(), rigSilentReply)
	trustRigHost(t, rig, rig.hostKey)
	opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: testTimer}

	runtime.GC()
	g0, fd0 := runtime.NumGoroutine(), openFDs(t)
	err := callWithin(t, func() error {
		_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
		return err
	})
	wantTimeout(t, err, "reply")
	if g := settle(g0); g > g0 {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines: %d before the call, %d after it\n%s", g0, g, buf[:runtime.Stack(buf, true)])
	}
	if fd := openFDs(t); fd > fd0 {
		t.Errorf("open file descriptors: %d before the call, %d after it", fd0, fd)
	}
}

// TestOversizedReplyIsBadReply: a reply past the byte bound fails with a
// BadReplyError whose cause names the limit.
func TestOversizedReplyIsBadReply(t *testing.T) {
	old := maxReplyBytes
	maxReplyBytes = 1024
	t.Cleanup(func() { maxReplyBytes = old })

	dir, err := os.MkdirTemp("", "ob")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		buf := make([]byte, 512)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte(`{"result":"` + strings.Repeat("x", 4096) + `"}` + "\n"))
	}()
	err = callWithin(t, func() error {
		_, err := SendRequestWith(sock, Request{Method: "ping"}, DialOptions{})
		return err
	})
	var bad *BadReplyError
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v (%T), want a BadReplyError", err, err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(1024)) {
		t.Errorf("err = %v, want it to name the 1024-byte limit", err)
	}
}

// TestTimerBoundsStalledAuth: a server that accepts the host key exchange
// and never decides on the offered key is bounded in the auth phase.
func TestTimerBoundsStalledAuth(t *testing.T) {
	resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRigWith(t, client.PublicKey(), rigStallAuth)
	trustRigHost(t, rig, rig.hostKey)
	opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: testTimer}
	err := callWithin(t, func() error {
		_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
		return err
	})
	wantTimeout(t, err, "auth")
}

// TestTimerBoundsStalledShell: a server that authenticates and never
// answers the shell request is bounded too; the session setup comes before
// the request is written.
func TestTimerBoundsStalledShell(t *testing.T) {
	resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRigWith(t, client.PublicKey(), rigStallShell)
	trustRigHost(t, rig, rig.hostKey)
	opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: testTimer}
	err := callWithin(t, func() error {
		_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
		return err
	})
	wantTimeout(t, err, "reply")
}

type closeCounter struct{ n chan struct{} }

func (c closeCounter) Close() error { c.n <- struct{}{}; return nil }

// TestEnterAfterFireClosesAtOnce: a connection registered after the timer
// has fired is closed at once, so a dial that finishes late cannot outlive
// the timer.
func TestEnterAfterFireClosesAtOnce(t *testing.T) {
	ex := startExchange(time.Nanosecond)
	defer ex.stop()
	for i := 0; ; i++ {
		ex.mu.Lock()
		fired := ex.fired != ""
		ex.mu.Unlock()
		if fired {
			break
		}
		if i > 1000 {
			t.Fatal("a 1ns timer had not fired")
		}
		time.Sleep(time.Millisecond)
	}
	c := closeCounter{n: make(chan struct{}, 1)}
	ex.enter(phaseReply, c)
	select {
	case <-c.n:
	default:
		t.Fatal("a connection registered after the timer fired was not closed")
	}
}

// TestConnectTimeoutIsTimeLeft: the connect gets the time left on the
// timer, capped at its own limit; with no timer, its own limit.
func TestConnectTimeoutIsTimeLeft(t *testing.T) {
	var none *exchange
	if got := none.connectTimeout(7 * time.Second); got != 7*time.Second {
		t.Errorf("no timer: %s, want the limit 7s", got)
	}
	ex := &exchange{deadline: time.Now().Add(time.Hour)}
	if got := ex.connectTimeout(7 * time.Second); got != 7*time.Second {
		t.Errorf("an hour left, limit 7s: %s, want 7s", got)
	}
	ex = &exchange{deadline: time.Now().Add(2 * time.Second)}
	if got := ex.connectTimeout(7 * time.Second); got > 2*time.Second || got <= 0 {
		t.Errorf("2s left, limit 7s: %s, want at most 2s", got)
	}
	if got := ex.connectTimeout(0); got > 2*time.Second || got <= 0 {
		t.Errorf("2s left, no limit: %s, want at most 2s", got)
	}
	ex = &exchange{deadline: time.Now().Add(-time.Second)}
	if got := ex.connectTimeout(0); got <= 0 {
		t.Errorf("past the deadline: %s, want a positive duration so the dial fails at once", got)
	}
}

// TestExpiredTimerMakesNoConnect: once the timer has run out, the SSH
// dial's connect gets no time of its own, so no TCP connection is made.
// The listener's first accept must be the control connection made after
// the call returns.
func TestExpiredTimerMakesNoConnect(t *testing.T) {
	_, pemBytes := newSigner(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	_, err = SendRequestWith("mrvl://u@"+ln.Addr().String(), Request{Method: "ping"},
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: time.Nanosecond})
	wantTimeout(t, err, "connect")

	ctl, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ctl.Close() }()
	if _, err := ctl.Write([]byte("control")); err != nil {
		t.Fatal(err)
	}
	first := <-accepted
	defer func() { _ = first.Close() }()
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 7)
	n, _ := io.ReadFull(first, buf)
	if string(buf[:n]) != "control" {
		t.Errorf("the first connection accepted read %q, want the control connection's \"control\": the dial connected after its timer ran out", buf[:n])
	}
}

// TestBoundedReaderStopsAtTheLimit pins the edge of the cap: a reply under
// the limit is read whole and ends in EOF, and a reply at or over it is
// read up to the limit and no further, then refused with the too-large
// error.
func TestBoundedReaderStopsAtTheLimit(t *testing.T) {
	const limit = 1000
	read := func(n int) (int, error) {
		r := newBoundedReader(strings.NewReader(strings.Repeat("x", n)), limit)
		buf := make([]byte, 4096)
		total := 0
		for i := 0; ; i++ {
			k, err := r.Read(buf)
			total += k
			if err != nil {
				return total, err
			}
			// A read that makes no progress, or a loop that outlasts the
			// data, is a failure to end the stream, not a pass.
			if k == 0 || i > 4*n/len(buf)+4 {
				t.Fatalf("a reply of %d bytes: read %d bytes with no end (k=%d, err=%v)", n, total, k, err)
			}
		}
	}
	got, err := read(limit - 1)
	if got != limit-1 || err != io.EOF {
		t.Errorf("a reply one byte under: read %d bytes, err %v; want %d and EOF", got, err, limit-1)
	}
	for _, n := range []int{limit, limit + 1, 5 * limit} {
		got, err = read(n)
		if got != limit {
			t.Errorf("a reply of %d bytes: read %d; want exactly %d", n, got, limit)
		}
		if !errors.Is(err, errReplyTooLarge) {
			t.Errorf("a reply of %d bytes ended with %v; want errReplyTooLarge", n, err)
		}
	}
}
