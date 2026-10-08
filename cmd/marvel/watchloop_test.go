package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

var watchT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// loopRig runs watchLoop against a fetch the test controls: a fetch returns
// when the test sends a result, and stays blocked until it does, like a
// daemon that is not answering.
type loopRig struct {
	t       *testing.T
	keys    chan byte
	ticks   chan time.Time
	results chan watchData
	frames  chan string
	done    chan struct{}
	calls   atomic.Int32
}

func newLoopRig(t *testing.T, deadline time.Duration) *loopRig {
	t.Helper()
	setWidth(t, 200)
	r := &loopRig{
		t:       t,
		keys:    make(chan byte, 1),
		ticks:   make(chan time.Time, 8),
		results: make(chan watchData, 8),
		frames:  make(chan string, 64),
		done:    make(chan struct{}),
	}
	ws := newWatchState(nil)
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws.columns = cols
	cfg := watchLoopConfig{
		interval: time.Second,
		deadline: deadline,
		fetch: func() watchData {
			r.calls.Add(1)
			return <-r.results
		},
		now: func() time.Time { return watchT0 },
		out: func(frame string) {
			select {
			case r.frames <- frame:
			default:
			}
		},
	}
	go func() { watchLoop(ws, cfg, r.keys, r.ticks); close(r.done) }()
	t.Cleanup(func() {
		// Release any fetch still blocked, then ask the loop to stop.
		for i := 0; i < 4; i++ {
			select {
			case r.results <- watchData{err: errors.New("test over")}:
			default:
			}
		}
		select {
		case r.keys <- 'q':
		default:
		}
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
		}
	})
	return r
}

func (r *loopRig) good(names ...string) watchData {
	var ss []api.Session
	for _, n := range names {
		ss = append(ss, api.Session{Name: n, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"})
	}
	return watchData{sessions: ss, at: watchT0}
}

// frame waits for a frame the test accepts.
func (r *loopRig) frame(what string, ok func(string) bool) string {
	r.t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case f := <-r.frames:
			if ok(f) {
				return f
			}
		case <-timeout:
			r.t.Fatalf("no frame with %s within 2s", what)
			return ""
		}
	}
}

func contains(s string) func(string) bool {
	return func(f string) bool { return strings.Contains(f, s) }
}

func (r *loopRig) late() string {
	r.t.Helper()
	return r.frame("the late banner", contains("daemon not answering, data as of 12:00:00"))
}

// q and Ctrl-C quit whatever the daemon is doing: with the first fetch never
// answering, the key loop still reads them.
func TestWatchQuitsWhileTheFetchIsBlocked(t *testing.T) {
	for name, key := range map[string]byte{"q": 'q', "ctrl-c": 3} {
		t.Run(name, func(t *testing.T) {
			r := newLoopRig(t, 20*time.Millisecond)
			r.keys <- key
			select {
			case <-r.done:
			case <-time.After(2 * time.Second):
				t.Fatal("the key did not quit the view while the fetch was blocked")
			}
		})
	}
}

// Before the first answer the view draws a frame that says it is waiting,
// not the disconnected state, which is for a fetch that failed.
func TestWatchDrawsAFrameBeforeTheFirstFetchReturns(t *testing.T) {
	r := newLoopRig(t, time.Hour)
	f := r.frame("a waiting line", contains("waiting for the first answer"))
	if strings.Contains(f, "disconnected") {
		t.Errorf("a first fetch still in flight is not a disconnect:\n%s", f)
	}
}

// A fetch that runs past its deadline leaves the last good data on screen
// under a banner that names when the data is from.
func TestWatchShowsAStaleBannerWhenAFetchIsLate(t *testing.T) {
	r := newLoopRig(t, 20*time.Millisecond)
	r.results <- r.good("agent-0")
	r.frame("the first table", contains("agent-0"))

	r.ticks <- watchT0 // the next fetch never answers
	f := r.late()
	if !strings.Contains(f, "agent-0") {
		t.Errorf("the late frame should keep the last good table:\n%s", f)
	}
}

// When the late fetch finally answers, the banner goes and the new data shows.
func TestWatchClearsTheBannerWhenTheLateFetchReturns(t *testing.T) {
	r := newLoopRig(t, 20*time.Millisecond)
	r.results <- r.good("agent-0")
	r.frame("the first table", contains("agent-0"))
	r.ticks <- watchT0
	r.late()

	r.results <- r.good("agent-1")
	f := r.frame("the fresh table", contains("agent-1"))
	if strings.Contains(f, "not answering") {
		t.Errorf("the banner should clear once the fetch answers:\n%s", f)
	}

	// The slot is free again: the next tick fetches.
	r.ticks <- watchT0
	r.results <- r.good("agent-2")
	r.frame("the next fetch's table", contains("agent-2"))
}

// Ticks while a fetch is in flight do not start another: the view has one
// fetch outstanding at most, so a dead daemon does not pile up connections.
func TestWatchKeepsOneFetchInFlight(t *testing.T) {
	r := newLoopRig(t, 20*time.Millisecond)
	r.results <- r.good("agent-0")
	r.frame("the first table", contains("agent-0"))
	r.ticks <- watchT0
	r.late()
	r.ticks <- watchT0
	r.ticks <- watchT0
	time.Sleep(50 * time.Millisecond)
	if got := r.calls.Load(); got != 2 {
		t.Errorf("fetch called %d times, want 2 (the first, and the one still in flight)", got)
	}
}

// A sort key redraws from what is on hand; it neither waits on the blocked
// fetch nor starts another.
func TestWatchSortKeyRedrawsWithoutAFetch(t *testing.T) {
	r := newLoopRig(t, 20*time.Millisecond)
	r.results <- r.good("agent-0")
	r.frame("the first table", contains("agent-0"))
	r.ticks <- watchT0
	r.late()

	r.keys <- 's'
	r.frame("the new sort label", contains("sort: state asc"))
	if got := r.calls.Load(); got != 2 {
		t.Errorf("fetch called %d times after a sort key, want 2", got)
	}
}

// A fetch that fails is still the disconnected state, with the last known
// table under it.
func TestWatchShowsDisconnectedWhenAFetchFails(t *testing.T) {
	r := newLoopRig(t, time.Hour)
	r.results <- r.good("agent-0")
	r.frame("the first table", contains("agent-0"))
	r.ticks <- watchT0
	r.results <- watchData{err: errors.New("refused"), at: watchT0}
	f := r.frame("the disconnected banner", contains("daemon disconnected"))
	if !strings.Contains(f, "last known state") || !strings.Contains(f, "agent-0") {
		t.Errorf("the disconnected frame should keep the last known table:\n%s", f)
	}
}

// With nothing in flight, a sort key still redraws from what is on hand and
// starts no fetch: only the tick fetches.
func TestWatchSortKeyWhenIdleDoesNotFetch(t *testing.T) {
	r := newLoopRig(t, time.Hour)
	r.results <- r.good("agent-0")
	r.frame("the first table", contains("agent-0"))

	r.keys <- 's'
	r.frame("the new sort label", contains("sort: state asc"))
	time.Sleep(50 * time.Millisecond)
	if got := r.calls.Load(); got != 1 {
		t.Errorf("fetch called %d times after a sort key with nothing in flight, want 1", got)
	}
}

// The loop tests above use a fake fetch. This one runs the real
// fetchWatchData with the header on against a daemon that accepts the
// connection and never answers, so the blocked read is the header's own
// (collectHeader runs first, before the listing is requested). The view must
// still say the daemon is not answering and still quit on q, which is the
// property the off-the-key-loop fetch exists for.
func TestWatchQuitsWhileTheRealHeaderReadIsBlocked(t *testing.T) {
	setWidth(t, 200)
	home, err := os.MkdirTemp("", "mv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })

	sock := filepath.Join(home, "silent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var accepted atomic.Int32
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	var fetches sync.WaitGroup
	t.Cleanup(func() {
		// Closing the held connections lets the blocked fetch goroutine end,
		// and the globals restored above must not change under it.
		_ = ln.Close()
		mu.Lock()
		for _, c := range held {
			_ = c.Close()
		}
		mu.Unlock()
		fetches.Wait()
	})
	t.Setenv(config.SocketEnv, sock)

	keys := make(chan byte, 1)
	frames := make(chan string, 64)
	done := make(chan struct{})
	ws := newWatchScreen(nil, false, false)
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws.columns = cols
	cfg := watchLoopConfig{
		interval: time.Second,
		deadline: 30 * time.Millisecond,
		fetch: func() watchData {
			defer fetches.Done()
			return fetchWatchData(ws.showHeader)
		},
		now: time.Now,
		out: func(frame string) {
			select {
			case frames <- frame:
			default:
			}
		},
	}
	fetches.Add(1) // the loop starts exactly one fetch and the silent daemon keeps it open
	go func() { watchLoop(ws, cfg, keys, make(chan time.Time)); close(done) }()

	if !ws.showHeader {
		t.Fatal("the watch screen does not ask for the header, so this test would not reach the header read")
	}
	timeout := time.After(2 * time.Second)
	for stale := false; !stale; {
		select {
		case f := <-frames:
			stale = strings.Contains(f, "daemon not answering, no data yet")
		case <-timeout:
			t.Fatal("no stale banner within 2s while the header read was blocked")
		}
	}
	if accepted.Load() != 1 {
		t.Errorf("the silent daemon accepted %d connection(s), want 1: the header read, with the listing not yet requested", accepted.Load())
	}
	keys <- 'q'
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("q did not quit the view while the header read was blocked")
	}
}
