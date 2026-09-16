package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// decodeBatch reads one stream message from dec with a bound.
func decodeBatch(t *testing.T, dec *json.Decoder) (Response, EventsBatch) {
	t.Helper()
	type out struct {
		resp Response
		err  error
	}
	ch := make(chan out, 1)
	go func() {
		var resp Response
		err := dec.Decode(&resp)
		ch <- out{resp, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("decode stream message: %v", o.err)
		}
		var b EventsBatch
		if o.resp.Error == "" && len(o.resp.Result) > 0 {
			if err := json.Unmarshal(o.resp.Result, &b); err != nil {
				t.Fatalf("parse batch: %v", err)
			}
		}
		return o.resp, b
	case <-time.After(3 * time.Second):
		t.Fatal("no stream message within 3s")
	}
	return Response{}, EventsBatch{}
}

// startWatch runs handleRWCAs on one end of a pipe as caller c and sends
// the events.watch request from the other. It returns the client end, a
// decoder on it, and a channel that closes when the handler returns.
func startWatch(t *testing.T, d *Daemon, c caller, params string) (net.Conn, *json.Decoder, <-chan struct{}) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleRWCAs(server, c)
	}()
	req := `{"method":"events.watch"`
	if params != "" {
		req += `,"params":` + params
	}
	req += "}\n"
	if _, err := client.Write([]byte(req)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	return client, json.NewDecoder(client), done
}

// shortSocket returns a unix socket path short enough for the platform
// limit (104 bytes on macOS), which t.TempDir paths under a long test
// name exceed.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mw")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return within 3s")
	}
}

func TestEventsWatchStreamsBacklogThenLiveDeliveries(t *testing.T) {
	ring := events.NewRing(100)
	d := &Daemon{home: "/h", events: ring}
	ring.Emit(events.Event{Kind: events.KindSessionCreated, Message: "one"})
	ring.Emit(events.Event{Kind: events.KindSessionCreated, Message: "two"})

	client, dec, done := startWatch(t, d, localCaller(), `{"n":0}`)

	resp, first := decodeBatch(t, dec)
	if resp.DaemonHome != "/h" {
		t.Fatalf("stream message not stamped: %+v", resp)
	}
	if len(first.Events) != 2 || first.Events[1].Message != "two" {
		t.Fatalf("backlog = %+v, want the two pre-existing events", first.Events)
	}

	ring.Emit(events.Event{Kind: events.KindSessionCrashed, Message: "three"})
	_, live := decodeBatch(t, dec)
	if len(live.Events) != 1 || live.Events[0].Seq != 3 || live.Events[0].Message != "three" {
		t.Fatalf("live delivery = %+v, want Seq 3 only", live.Events)
	}
	if live.Dropped != 0 {
		t.Fatalf("Dropped=%d on a fresh stream, want 0", live.Dropped)
	}

	// Hanging up ends the stream and removes the watch from the ring.
	_ = client.Close()
	waitDone(t, done)
	if n := ring.Watchers(); n != 0 {
		t.Fatalf("ring still has %d watcher(s) after the client hung up", n)
	}
}

func TestEventsWatchResumesFromCursorWithoutDuplicates(t *testing.T) {
	ring := events.NewRing(100)
	d := &Daemon{home: "/h", events: ring}
	for i := 0; i < 5; i++ {
		ring.Emit(events.Event{Kind: events.KindSessionCreated})
	}
	// A follow client that already printed Seq 1..3 resumes from 3.
	client, dec, done := startWatch(t, d, localCaller(), `{"n":0,"since_seq":3}`)
	defer func() { _ = client.Close(); waitDone(t, done) }()

	_, backlog := decodeBatch(t, dec)
	if len(backlog.Events) != 2 || backlog.Events[0].Seq != 4 || backlog.Events[1].Seq != 5 {
		t.Fatalf("backlog after cursor 3 = %+v, want Seq 4,5", backlog.Events)
	}
	ring.Emit(events.Event{Kind: events.KindSessionCreated})
	_, live := decodeBatch(t, dec)
	if len(live.Events) != 1 || live.Events[0].Seq != 6 {
		t.Fatalf("live after backlog = %+v, want Seq 6 only", live.Events)
	}
}

func TestEventsWatchHonorsFilter(t *testing.T) {
	ring := events.NewRing(100)
	d := &Daemon{home: "/h", events: ring}
	client, dec, done := startWatch(t, d, localCaller(), `{"n":0,"kind":"session.crashed"}`)
	defer func() { _ = client.Close(); waitDone(t, done) }()

	if _, backlog := decodeBatch(t, dec); len(backlog.Events) != 0 {
		t.Fatalf("backlog = %+v, want empty", backlog.Events)
	}
	ring.Emit(events.Event{Kind: events.KindSessionCreated})
	ring.Emit(events.Event{Kind: events.KindSessionCrashed})
	_, live := decodeBatch(t, dec)
	if len(live.Events) != 1 || live.Events[0].Kind != events.KindSessionCrashed {
		t.Fatalf("filtered live delivery = %+v, want one session.crashed", live.Events)
	}
}

func TestEventsWatchRefusedByScopeSendsOneError(t *testing.T) {
	ring := events.NewRing(10)
	d := &Daemon{home: "/h", events: ring}
	client, dec, done := startWatch(t, d, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"}, "")
	defer func() { _ = client.Close() }()

	resp, _ := decodeBatch(t, dec)
	if resp.Error == "" {
		t.Fatal("credential-push scope was allowed to open an event stream")
	}
	waitDone(t, done)
	if n := ring.Watchers(); n != 0 {
		t.Fatalf("a refused request left %d watcher(s)", n)
	}
}

func TestEventsWatchEndsOnDaemonShutdown(t *testing.T) {
	ring := events.NewRing(10)
	d := &Daemon{home: "/h", events: ring}
	ctx, cancel := context.WithCancel(context.Background())
	d.ctx = ctx
	client, dec, done := startWatch(t, d, localCaller(), "")
	defer func() { _ = client.Close() }()
	decodeBatch(t, dec) // backlog

	cancel()
	waitDone(t, done)
}

// The client helper end to end over a real unix socket: backlog, live
// delivery, a clean stop from fn, and the fallback signal from a daemon
// that predates the method.
func TestWatchEventsWithOverUnixSocket(t *testing.T) {
	ring := events.NewRing(100)
	d := &Daemon{home: "/h", events: ring}
	ring.Emit(events.Event{Kind: events.KindSessionCreated, Message: "pre"})

	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.handleConn(conn)
		}
	}()

	stop := errors.New("enough")
	var got []events.Event
	var homes []string
	err = WatchEventsWith(sock, json.RawMessage(`{"n":0}`), DialOptions{}, func(resp *Response, b EventsBatch) error {
		homes = append(homes, resp.DaemonHome)
		got = append(got, b.Events...)
		if len(got) == 1 {
			ring.Emit(events.Event{Kind: events.KindSessionCrashed, Message: "live"})
			return nil
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("WatchEventsWith returned %v, want fn's stop error", err)
	}
	if len(got) != 2 || got[0].Message != "pre" || got[1].Message != "live" {
		t.Fatalf("stream delivered %+v, want pre then live", got)
	}
	if homes[0] != "/h" {
		t.Fatalf("first message DaemonHome=%q, want /h", homes[0])
	}
	// The server side notices the hang-up and drops the watch.
	deadline := time.Now().Add(3 * time.Second)
	for ring.Watchers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := ring.Watchers(); n != 0 {
		t.Fatalf("ring still has %d watcher(s) after the client returned", n)
	}
}

func TestWatchEventsWithReportsUnsupportedDaemon(t *testing.T) {
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req Request
		_ = json.NewDecoder(conn).Decode(&req)
		// What a daemon before this method answers.
		_ = json.NewEncoder(conn).Encode(Response{Error: "unknown method: " + req.Method})
	}()

	err = WatchEventsWith(sock, nil, DialOptions{}, func(*Response, EventsBatch) error {
		t.Fatal("fn called on an unsupported daemon")
		return nil
	})
	if !errors.Is(err, ErrWatchUnsupported) {
		t.Fatalf("got %v, want ErrWatchUnsupported", err)
	}
}
