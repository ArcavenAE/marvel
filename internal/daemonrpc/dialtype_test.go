package daemonrpc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// TestDialErrorTypes: a connect that times out is a TimeoutError in the
// connect phase; any other failed connect is an UnreachableError.
func TestDialErrorTypes(t *testing.T) {
	t.Parallel()
	var tmo *TimeoutError
	if err := dialError("a", &net.OpError{Op: "dial", Err: timeoutErr{}}); !errors.As(err, &tmo) || tmo.Phase != "connect" {
		t.Errorf("timeout: %v (%T), want a TimeoutError in phase connect", err, err)
	}
	var unr *UnreachableError
	if err := dialError("a", errors.New("connection refused")); !errors.As(err, &unr) || unr.Addr != "a" {
		t.Errorf("refused: %v (%T), want an UnreachableError for a", err, err)
	}
	if err := dialError("a", errors.New("connection refused")); errors.As(err, &tmo) {
		t.Errorf("refused reads as a timeout: %v", err)
	}
}

// closedPort returns a 127.0.0.1 port nothing listens on.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestEveryDialIsTypedUnreachable: a unix socket, tcp:// and mrvl://
// address that nothing listens on each fail with an UnreachableError.
func TestEveryDialIsTypedUnreachable(t *testing.T) {
	dir, err := os.MkdirTemp("", "dt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	_, pemBytes := newSigner(t)
	id := writeIdentity(t, pemBytes)
	for _, addr := range []string{
		filepath.Join(dir, "none.sock"),
		"tcp://" + closedPort(t),
		"mrvl://u@" + closedPort(t),
	} {
		_, err := SendRequestWith(addr, Request{Method: "ping"}, DialOptions{Identity: id, StrictHostKey: true})
		var unr *UnreachableError
		if !errors.As(err, &unr) {
			t.Errorf("%s: err = %v (%T), want an UnreachableError", addr, err, err)
		}
	}
}

// TestGarbageReplyIsBadReply: an answer that does not parse is a
// BadReplyError.
func TestGarbageReplyIsBadReply(t *testing.T) {
	dir, err := os.MkdirTemp("", "gr")
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
		buf := make([]byte, 512)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("not json\n"))
		_ = c.Close()
	}()
	_, err = SendRequestWith(sock, Request{Method: "ping"}, DialOptions{})
	var bad *BadReplyError
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v (%T), want a BadReplyError", err, err)
	}
}

// TestNoKeyIsNoIdentityError: with no identity, no marvel client key, no
// agent and no keys in the user's SSH directory, the dial fails with a
// NoIdentityError before any connect.
func TestNoKeyIsNoIdentityError(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	_, err := SendRequestWith("mrvl://u@"+closedPort(t), Request{Method: "ping"}, DialOptions{StrictHostKey: true})
	var noID *NoIdentityError
	if !errors.As(err, &noID) {
		t.Fatalf("err = %v (%T), want a NoIdentityError", err, err)
	}
}
