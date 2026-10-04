package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A daemon that accepts and never answers must not hang a best-effort sender
// that asked for a deadline.
func TestSendRequestWithTimeoutReturns(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mrv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, aerr := ln.Accept()
		if aerr == nil {
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	start := time.Now()
	_, err = SendRequestWith(sock, Request{Method: "account.limits"}, DialOptions{Timeout: 200 * time.Millisecond})
	if err == nil {
		t.Fatal("an unanswered request returned no error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}
