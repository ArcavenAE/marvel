package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tempSocket keeps the path short: a Unix socket address is capped near 104
// bytes on Darwin, which t.TempDir() paths exceed. Same shape the daemon tests
// use.
func tempSocket(t *testing.T, tag string) string {
	t.Helper()
	p := filepath.Join(os.TempDir(), fmt.Sprintf("marvel-shim-%s-%d.sock", tag, os.Getpid()))
	_ = os.Remove(p)
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// TestBroadcasterDrainsSlowSubscriberOnClose guards the defect the spike's
// signal-2 run found: without a drain wait, child exit tore down the pump
// goroutines mid-queue and a lagging consumer silently lost the tail.
func TestBroadcasterDrainsSlowSubscriberOnClose(t *testing.T) {
	sock := tempSocket(t, "drain")
	ln, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	bc := newBroadcaster()
	go bc.serve(ln)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	waitFor(t, func() bool { return bc.clientCount() == 1 })

	const lines = 2000
	var want bytes.Buffer
	for i := 0; i < lines; i++ {
		chunk := []byte(fmt.Sprintf("line %06d padding-padding-padding\n", i))
		want.Write(chunk)
		bc.publish(chunk)
	}

	// close() must not return until the subscriber has been served, so a reader
	// that only starts afterwards still sees every byte.
	done := make(chan struct{})
	go func() {
		bc.close()
		close(done)
	}()

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("stream truncated or reordered: got %d bytes, want %d", len(got), want.Len())
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not return after the subscriber drained")
	}
}

// TestBroadcasterFanOut checks that two subscribers each get the full byte
// stream, since marvel plus a diagnostic viewer is the expected shape.
func TestBroadcasterFanOut(t *testing.T) {
	sock := tempSocket(t, "fanout")
	ln, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	bc := newBroadcaster()
	go bc.serve(ln)

	conns := make([]net.Conn, 2)
	for i := range conns {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		conns[i] = c
	}
	waitFor(t, func() bool { return bc.clientCount() == len(conns) })

	payload := []byte("\x1b[32mhello\x1b[0m\n")
	bc.publish(payload)
	bc.close()

	for i, c := range conns {
		got, err := io.ReadAll(c)
		if err != nil {
			t.Fatalf("subscriber %d: %v", i, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("subscriber %d got %q, want %q", i, got, payload)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
