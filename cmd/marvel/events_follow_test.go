package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/events"
)

// serveWatchStream answers one events.watch request with the given
// batches, then closes, the way a daemon that is shutting down would.
// It speaks the wire shape only; the daemon side of the stream is
// covered in internal/daemon.
func serveWatchStream(t *testing.T, batches []daemon.EventsBatch) (sock string, gotReq <-chan daemon.Request) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mf")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock = filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	reqCh := make(chan daemon.Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req daemon.Request
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		reqCh <- req
		enc := json.NewEncoder(conn)
		for _, b := range batches {
			data, _ := json.Marshal(b)
			if err := enc.Encode(daemon.Response{Result: data, DaemonHome: "/h"}); err != nil {
				return
			}
		}
	}()
	return sock, reqCh
}

// The stream path prints every batch through printEventBatch and
// nothing else on stdout: a batch of the same events renders the same
// bytes the poll path rendered, and the gap note stays off stdout.
func TestFollowStreamRendersBatchesThroughPrintEventBatch(t *testing.T) {
	ts := time.Date(2026, 9, 16, 4, 5, 6, 0, time.UTC)
	backlog := []events.Event{
		{Seq: 4, Timestamp: ts, Kind: events.KindSessionCreated, Severity: events.SeverityInfo, Workspace: "ws", Team: "t", Role: "r", Session: "ws/t-r-g1-0", Message: "spawned"},
		{Seq: 5, Timestamp: ts.Add(time.Second), Kind: events.KindHealthCheckFailed, Severity: events.SeverityWarning, Workspace: "ws", Team: "t", Message: "no heartbeat", Actor: "pid=1 socket=/s"},
	}
	live := []events.Event{
		{Seq: 6, Timestamp: ts.Add(2 * time.Second), Kind: events.KindSessionCrashed, Severity: events.SeverityWarning, Workspace: "ws", Session: "ws/t-r-g1-0", Message: "exit 1"},
	}
	sock, gotReq := serveWatchStream(t, []daemon.EventsBatch{
		{Events: backlog},
		{}, // keepalive: prints nothing
		{Events: live, Dropped: 3, ResumeFrom: 2},
	})

	// The daemon-home check on the first message only warns on stderr,
	// so the fake's "/h" costs nothing on stdout whatever this machine's
	// ~/.marvel is.
	oldSock := socketPath
	socketPath = sock
	t.Cleanup(func() { socketPath = oldSock })

	var out bytes.Buffer
	var seenSince uint64
	err := followStream(3, func(since uint64) json.RawMessage {
		seenSince = since
		raw, _ := json.Marshal(map[string]any{"n": 0, "since_seq": since})
		return raw
	}, func(evs []events.Event, header bool) {
		if header {
			t.Error("follow batches must never reprint the header")
		}
		printEventBatch(&out, evs, false)
	})
	if err == nil || !strings.Contains(err.Error(), "daemon closed the connection") {
		t.Fatalf("stream end error = %v, want the daemon-closed message", err)
	}
	if seenSince != 3 {
		t.Fatalf("stream opened with cursor %d, want 3", seenSince)
	}
	req := <-gotReq
	if req.Method != daemon.MethodEventsWatch {
		t.Fatalf("request method %q, want %q", req.Method, daemon.MethodEventsWatch)
	}
	if !strings.Contains(string(req.Params), `"since_seq":3`) {
		t.Fatalf("request params %s carry no cursor", req.Params)
	}

	var want bytes.Buffer
	printEventBatch(&want, backlog, false)
	printEventBatch(&want, live, false)
	if out.String() != want.String() {
		t.Fatalf("stream output differs from printEventBatch of the same batches:\n--- got ---\n%s--- want ---\n%s", out.String(), want.String())
	}
	if strings.Contains(out.String(), "note:") {
		t.Fatal("gap note leaked onto stdout")
	}
}

// printEventBatch is the one renderer, and its shape is what scripts
// parse: pin the columns and the actor suffix so a change is deliberate.
func TestPrintEventBatchShape(t *testing.T) {
	ts := time.Date(2026, 9, 16, 4, 5, 6, 0, time.UTC)
	var out bytes.Buffer
	printEventBatch(&out, []events.Event{
		{Timestamp: ts, Kind: events.KindSessionCreated, Workspace: "ws", Team: "t", Message: "m"},
		{Timestamp: ts, Kind: events.KindSessionCrashed, Severity: events.SeverityWarning, Workspace: "ws", Session: "ws/a", Message: "m", Actor: "pid=1 socket=/s"},
	}, true)
	got := out.String()
	for _, want := range []string{
		"TIME      SEV      KIND             SESSION  MESSAGE\n",
		"04:05:06  info     session.created  ws/t     m\n",
		"04:05:06  warning  session.crashed  ws/a     m [by pid=1 socket=/s]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}
