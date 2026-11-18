package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// Enter in the watch across clusters: each row resolves against the daemon
// status of its own cluster, read inside the poll, and Enter itself calls no
// daemon. The fake daemon answers get, version and daemon.status, and counts
// each, on a unix socket in a scratch directory; the remote case reaches the
// same fake through the localhost ssh front of the same-daemon tests. Nothing
// here touches tmux, the real marvel home or a live daemon.

const fakeTmuxSocket = "mvl-fan-test"

type stFake struct {
	sock     string
	mu       sync.Mutex
	statuses int
	gets     int
	failing  bool // every request is answered with an error
}

func (f *stFake) setFailing(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = v
}

func startStFake(t *testing.T, rows []api.Session) *stFake {
	t.Helper()
	dir, err := os.MkdirTemp("", "st")
	if err != nil {
		t.Fatal(err)
	}
	// The directory is left in the run's scratch TMPDIR; the test removes nothing.
	// The socket is TMPDIR/stNNN/d.sock, so TMPDIR has to be short: a unix
	// socket path over 104 characters cannot be bound.
	f := &stFake{sock: filepath.Join(dir, "d.sock")}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				var req daemonrpc.Request
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				var data []byte
				f.mu.Lock()
				if f.failing {
					f.mu.Unlock()
					_ = json.NewEncoder(c).Encode(daemonrpc.Response{Error: "the fake daemon is failing"})
					return
				}
				switch req.Method {
				case "version":
					data, _ = json.Marshal(map[string]any{"version": "x", "started_at": sdStart, "pid": 4243})
				case "daemon.status":
					f.statuses++
					data, _ = json.Marshal(daemon.DaemonStatus{TmuxSocket: fakeTmuxSocket})
				default:
					f.gets++
					data, _ = json.Marshal(rows)
				}
				f.mu.Unlock()
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: data})
			}()
		}
	}()
	return f
}

func (f *stFake) counts() (statuses, gets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses, f.gets
}

func stRows() []api.Session {
	return []api.Session{{Workspace: "w", Name: "s1", PaneID: "%5"}}
}

// fanEnterRig runs the real fanWatchLoop over the named clusters of the
// client config, with polls the test triggers by hand.
type fanEnterRig struct {
	t      *testing.T
	keys   chan byte
	ticks  chan time.Time
	frames chan string
}

func startFanEnter(t *testing.T, names ...string) *fanEnterRig {
	t.Helper()
	t.Setenv("MARVEL_SOCKET", "")
	t.Setenv("TMUX", "")
	setWidth(t, 200)
	fastTimer(t, 3*time.Second)
	cfgFile, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var targets []clusterTarget
	for _, n := range names {
		tg, err := resolveCluster(cfgFile, n)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, tg)
	}
	params, err := json.Marshal(getRequest(fanOutResource, false))
	if err != nil {
		t.Fatal(err)
	}
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
	ws.exec = func(argv []string) error {
		t.Errorf("Enter ran %v; it may only plan", argv)
		return nil
	}
	cfg := newFanWatchConfig(targets, params, time.Second)
	r := &fanEnterRig{t: t, keys: make(chan byte, 8), ticks: make(chan time.Time, 8), frames: make(chan string, 256)}
	cfg.out = func(frame string) {
		select {
		case r.frames <- frame:
		default:
		}
	}
	done := make(chan struct{})
	go func() { fanWatchLoop(ws, cfg, r.keys, r.ticks); close(done) }()
	t.Cleanup(func() { r.keys <- 'q'; <-done })
	return r
}

// frameWith returns the first frame that holds want, or fails with the last.
func (r *fanEnterRig) frameWith(what string, ok func(string) bool) string {
	r.t.Helper()
	deadline := time.After(10 * time.Second)
	var last string
	for {
		select {
		case last = <-r.frames:
			if ok(last) {
				return last
			}
		case <-deadline:
			r.t.Fatalf("no frame with %s within 10s; the last was:\n%s", what, last)
			return ""
		}
	}
}

// enter selects the first row and presses Enter, and returns the frame that
// shows what Enter said.
func (r *fanEnterRig) enter() string {
	r.t.Helper()
	r.frameWith("the row", func(f string) bool { return strings.Contains(f, "s1") })
	r.keys <- 'j'
	r.keys <- 13
	return r.frameWith("an Enter answer", func(f string) bool {
		return strings.Contains(f, "plan:") || strings.Contains(f, "print only:") || strings.Contains(f, "cannot plan")
	})
}

func TestFanEnterDoesNotSayNoDaemonToResolveAgainst(t *testing.T) {
	f := startStFake(t, stRows())
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	frame := startFanEnter(t, "a").enter()
	if strings.Contains(frame, "no daemon to resolve against") {
		t.Errorf("Enter on a cluster-set row reports the unwired resolver:\n%s", frame)
	}
}

func TestFanEnterOnALocalRowGivesARunPlan(t *testing.T) {
	f := startStFake(t, stRows())
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	frame := startFanEnter(t, "a").enter()
	if !strings.Contains(frame, "plan: ") || !strings.Contains(frame, fakeTmuxSocket) {
		t.Errorf("a local row should give a run plan naming the cluster's tmux socket %q:\n%s", fakeTmuxSocket, frame)
	}
}

func TestFanEnterOnARemoteRowIsPrintOnly(t *testing.T) {
	f := startStFake(t, stRows())
	addr, key := sshFront(t, map[string]string{"/run/d.sock": f.sock})
	clusterConfig(t, config.Cluster{Name: "r", Server: "ssh://u@" + addr + "/run/d.sock", Identity: identityFile(t)})
	trustHostKey(t, addr, key)
	frame := startFanEnter(t, "r").enter()
	if !strings.Contains(frame, "print only: ") || strings.Contains(frame, "plan: ") {
		t.Errorf("a remote row should be print-only, never a run plan:\n%s", frame)
	}
}

// The held status is dropped when its cluster fails, so Enter does not plan
// against a socket the cluster may no longer have, and it is read again by the
// first poll that answers after the failure.
func TestFanStatusIsDroppedOnFailureAndReadAgainOnRecovery(t *testing.T) {
	f := startStFake(t, stRows())
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	r := startFanEnter(t, "a")
	r.frameWith("the row", func(fr string) bool { return strings.Contains(fr, "s1") })
	// waitStatuses waits up to 2s for the count to reach at least n, and
	// returns the count it ends on.
	waitStatuses := func(n int) int {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			if s, _ := f.counts(); s >= n {
				return s
			}
			select {
			case <-deadline:
				s, _ := f.counts()
				return s
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	// The first poll reads the status once.
	if s := waitStatuses(1); s != 1 {
		t.Errorf("the first poll read daemon.status %d times, want once", s)
	}
	// The cluster fails: the held status goes, and Enter says so and plans nothing.
	f.setFailing(true)
	r.ticks <- time.Time{}
	r.frameWith("cluster a failing", func(fr string) bool {
		return strings.Contains(fr, "cluster a:") && !strings.Contains(fr, "cluster a: answered")
	})
	r.keys <- 'j'
	r.keys <- 13
	frame := r.frameWith("an Enter answer", func(fr string) bool {
		return strings.Contains(fr, "plan:") || strings.Contains(fr, "print only:") || strings.Contains(fr, "cannot plan")
	})
	if !strings.Contains(frame, "no daemon status yet") || strings.Contains(frame, "plan: ") {
		t.Errorf("after the cluster failed, Enter should say no daemon status yet and plan nothing:\n%s", frame)
	}
	// It answers again: the next poll reads the status once more.
	f.setFailing(false)
	r.ticks <- time.Time{}
	if s := waitStatuses(2); s != 2 {
		t.Errorf("after recovery daemon.status was read %d times in all, want 2", s)
	}
	// One more poll of the recovered cluster, which holds its status again, so
	// a third read would show.
	_, before := f.counts()
	r.ticks <- time.Time{}
	deadline := time.After(10 * time.Second)
	for {
		if _, g := f.counts(); g > before {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the poll after recovery never reached the daemon")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if s, _ := f.counts(); s != 2 {
		t.Errorf("the status was read again while held: %d reads in all, want 2", s)
	}
}

func TestFanStatusIsHeldAndEnterCallsNoDaemon(t *testing.T) {
	f := startStFake(t, stRows())
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	r := startFanEnter(t, "a")
	r.frameWith("the row", func(fr string) bool { return strings.Contains(fr, "s1") })
	// Two more polls of a cluster that keeps answering.
	for i := 0; i < 2; i++ {
		_, before := f.counts()
		r.ticks <- time.Time{}
		deadline := time.After(10 * time.Second)
		for {
			if _, g := f.counts(); g > before {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("poll %d never reached the daemon", i+1)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	statuses, gets := f.counts()
	if statuses != 1 {
		t.Errorf("daemon.status was read %d times over %d polls of one steady cluster, want once", statuses, gets)
	}
	r.enter()
	if s2, g2 := f.counts(); s2 != statuses || g2 != gets {
		t.Errorf("Enter called the daemon: status %d to %d, get %d to %d", statuses, s2, gets, g2)
	}
}
