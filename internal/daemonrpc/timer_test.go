package daemonrpc

import (
	"errors"
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
	rig := startSSHRigWith(t, client.PublicKey(), true)
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

// openFDs counts this process's open file descriptors.
func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skipf("no /dev/fd: %v", err)
	}
	return len(ents)
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
	rig := startSSHRigWith(t, client.PublicKey(), true)
	trustRigHost(t, rig, rig.hostKey)
	opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, Timeout: testTimer}

	g0, fd0 := settle(0), openFDs(t)
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
	_, err = SendRequestWith(sock, Request{Method: "ping"}, DialOptions{})
	var bad *BadReplyError
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v (%T), want a BadReplyError", err, err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(1024)) {
		t.Errorf("err = %v, want it to name the 1024-byte limit", err)
	}
}
