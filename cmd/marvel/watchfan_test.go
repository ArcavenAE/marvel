package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The rig for the watch across clusters: a fetch that blocks until the test
// releases it, one per cluster and sequence number, and every event the loop
// handles reported on a channel, so the tests take their order from channels
// and never from sleeps.

type fanKey struct {
	cluster string
	seq     uint64
}

type fanRig struct {
	t       *testing.T
	keys    chan byte
	ticks   chan time.Time
	frames  chan string
	events  chan fanEvent
	started chan fanKey
	done    chan struct{}

	mu      sync.Mutex
	release map[fanKey]chan fanFetch
	clock   time.Time
	seen    map[fanKey]bool // fetches known to have started, read by the test goroutine only
}

func newFanRig(t *testing.T, stale, timeout time.Duration, names ...string) *fanRig {
	t.Helper()
	setWidth(t, 200)
	r := &fanRig{
		t:       t,
		keys:    make(chan byte, 4),
		ticks:   make(chan time.Time, 16),
		frames:  make(chan string, 256),
		events:  make(chan fanEvent, 256),
		started: make(chan fanKey, 64),
		done:    make(chan struct{}),
		release: map[fanKey]chan fanFetch{},
		clock:   watchT0,
		seen:    map[fanKey]bool{},
	}
	var clusters []fanWatchCluster
	for _, n := range names {
		clusters = append(clusters, fanWatchCluster{name: n})
	}
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
	cfg := fanWatchConfig{
		clusters: clusters,
		interval: 2 * time.Second,
		stale:    stale,
		timeout:  timeout,
		fetch: func(c string, seq uint64, _ bool) fanFetch {
			ch := r.gate(fanKey{c, seq})
			r.started <- fanKey{c, seq}
			return <-ch
		},
		keyOf: func(string, string) string { return "" },
		now: func() time.Time {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.clock
		},
		out: func(frame string) {
			select {
			case r.frames <- frame:
			default:
			}
		},
		trace: func(e fanEvent) {
			select {
			case r.events <- e:
			default:
			}
		},
	}
	go func() { fanWatchLoop(ws, cfg, r.keys, r.ticks); close(r.done) }()
	t.Cleanup(func() {
		r.mu.Lock()
		for _, ch := range r.release {
			select {
			case ch <- fanFetch{rec: clusterRecord{Outcome: outcomeUnreachable, Cause: "test over"}}:
			default:
			}
		}
		r.mu.Unlock()
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

func (r *fanRig) gate(k fanKey) chan fanFetch {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.release[k] == nil {
		r.release[k] = make(chan fanFetch, 1)
	}
	return r.release[k]
}

func (r *fanRig) setNow(t time.Time) {
	r.mu.Lock()
	r.clock = t
	r.mu.Unlock()
}

func (r *fanRig) answer(c string, seq uint64, names ...string) {
	var rows []api.Session
	for _, n := range names {
		rows = append(rows, api.Session{Name: n, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning})
	}
	cnt := len(rows)
	r.gate(fanKey{c, seq}) <- fanFetch{
		rec:  clusterRecord{Cluster: c, Outcome: outcomeAnswered, Sessions: &cnt, Cause: fmt.Sprintf("%d sessions", cnt)},
		rows: rows,
	}
}

func (r *fanRig) waitStarted(c string, seq uint64) {
	r.t.Helper()
	want := fanKey{c, seq}
	timeout := time.After(2 * time.Second)
	for !r.seen[want] {
		select {
		case k := <-r.started:
			r.seen[k] = true
		case <-timeout:
			r.t.Fatalf("fetch %d of cluster %s never started", seq, c)
		}
	}
}

// waitEvent waits for the loop to handle an event the test accepts.
func (r *fanRig) waitEvent(what string, ok func(fanEvent) bool) fanEvent {
	r.t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-r.events:
			if ok(e) {
				return e
			}
		case <-timeout:
			r.t.Fatalf("the loop handled no %s within 2s", what)
			return fanEvent{}
		}
	}
}

func (r *fanRig) frame(what string, ok func(string) bool) string {
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

func (r *fanRig) tick() { r.ticks <- time.Time{} }

// redraw asks for a frame from what is on hand and returns it.
func (r *fanRig) redraw() string {
	r.t.Helper()
	r.drain()
	r.keys <- 'w'
	return r.frame("a frame after a key", func(string) bool { return true })
}

func (r *fanRig) drain() {
	for {
		select {
		case <-r.frames:
		default:
			return
		}
	}
}

// Cluster a's fetch 1 blocks. Its timer fires and frees a's slot, so fetch 2
// starts and shows. Fetch 1 then answers late, and its rows are dropped, while
// b's rows and as-of are untouched. This absorbs #822's
// TestStaleReplyDiscarded.
func TestStaleReplyDiscardedPerCluster(t *testing.T) {
	r := newFanRig(t, time.Hour, 20*time.Millisecond, "a", "b")
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.setNow(watchT0.Add(time.Second))
	r.answer("b", 1, "b-one")
	r.frame("b's rows", contains("b-one"))
	r.waitEvent("a's timer", func(e fanEvent) bool { return e.Kind == "timeout" && e.Cluster == "a" && e.Seq == 1 && e.Applied })

	r.setNow(watchT0.Add(5 * time.Second))
	r.tick()
	r.waitStarted("a", 2)
	r.answer("a", 2, "a-new")
	f := r.frame("a's fetch 2", contains("a-new"))
	if !strings.Contains(f, "b-one") {
		t.Errorf("b's rows are gone after a's fetch 2:\n%s", f)
	}

	r.answer("a", 1, "a-old")
	e := r.waitEvent("a's late fetch 1", func(e fanEvent) bool { return e.Kind == "result" && e.Cluster == "a" && e.Seq == 1 })
	if e.Applied {
		t.Error("a's late fetch 1 was applied after fetch 2 was shown")
	}
	f = r.redraw()
	if strings.Contains(f, "a-old") {
		t.Errorf("the late reply of fetch 1 is on screen:\n%s", f)
	}
	if !strings.Contains(f, "a-new") || !strings.Contains(f, "b-one") {
		t.Errorf("a's fetch 2 or b's rows are missing:\n%s", f)
	}
	if !strings.Contains(f, "cluster b: answered: 1 sessions, as of 12:00:01") {
		t.Errorf("b's as-of moved with a's:\n%s", f)
	}
}

// One cluster that never answers does not take the frame from the others: the
// first-answer wait is per cluster, not for the whole view.
func TestOneSlowClusterDoesNotBlankWatch(t *testing.T) {
	r := newFanRig(t, time.Hour, time.Hour, "a", "b")
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.answer("b", 1, "b-one")
	f := r.frame("b's rows", contains("b-one"))
	if strings.Contains(f, "waiting for the first answer from the daemon") {
		t.Errorf("the whole frame waits for a:\n%s", f)
	}
	if !strings.Contains(f, "cluster a: waiting for the first answer") {
		t.Errorf("a's own wait is not named:\n%s", f)
	}
}

// Each cluster shows the time its own rows were read, and a cluster that has
// not answered for the stale interval reads late on its own.
func TestEachClusterAgesOnItsOwnAsOf(t *testing.T) {
	r := newFanRig(t, 10*time.Millisecond, time.Hour, "a", "b")
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.setNow(watchT0.Add(time.Second))
	r.answer("a", 1, "a-one")
	r.frame("a's rows", contains("a-one"))
	r.setNow(watchT0.Add(9 * time.Second))
	r.answer("b", 1, "b-one")
	r.frame("b's rows", contains("b-one"))
	f := r.redraw()
	for _, want := range []string{
		"cluster a: answered: 1 sessions, as of 12:00:01",
		"cluster b: answered: 1 sessions, as of 12:00:09",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("missing %q:\n%s", want, f)
		}
	}

	// a's next fetch stays out; b answers again. a reads late with its own
	// as-of, and b's moves alone.
	r.setNow(watchT0.Add(11 * time.Second))
	r.tick()
	r.waitStarted("a", 2)
	r.waitStarted("b", 2)
	r.answer("b", 2, "b-two")
	r.waitEvent("a's stale mark", func(e fanEvent) bool { return e.Kind == "stale" && e.Cluster == "a" && e.Applied })
	f = r.redraw()
	for _, want := range []string{
		"cluster a: not answering, data as of 12:00:01",
		"cluster b: answered: 1 sessions, as of 12:00:11",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("missing %q:\n%s", want, f)
		}
	}
}

// q quits the view while a fetch is still blocked: the keys are never behind a
// daemon that does not answer.
func TestFanWatchQuitsWhileAFetchIsBlocked(t *testing.T) {
	r := newFanRig(t, time.Hour, time.Hour, "a", "b")
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.keys <- 'q'
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("q did not quit the view while the fetches were blocked")
	}
}

// An entry that shares a socket with another is polled with its stamp. Both
// reads together stay inside one timer, so a daemon that answers each call
// within the timer, but slowly, is not reported as silent: the stamp is the
// part that gives way.
func TestStampReadSharesTheFetchBudget(t *testing.T) {
	setWidth(t, 200)
	timer, backstop, delay := 600*time.Millisecond, 150*time.Millisecond, 400*time.Millisecond
	fastTimer(t, timer)
	old := fanWatchBackstop
	fanWatchBackstop = backstop
	t.Cleanup(func() { fanWatchBackstop = old })

	f := startSD(t, sdStart, []api.Session{row("w", "s1"), row("w", "s2")})
	f.delay = delay
	link := filepath.Join(filepath.Dir(f.sock), "alias.sock")
	if err := os.Symlink(f.sock, link); err != nil {
		t.Fatal(err)
	}
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: link})
	cfgFile, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var targets []clusterTarget
	for _, n := range []string{"a", "b"} {
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
	cfg := newFanWatchConfig(targets, params, time.Second)
	frames := make(chan string, 256)
	cfg.out = func(frame string) {
		select {
		case frames <- frame:
		default:
		}
	}
	keys := make(chan byte, 2)
	done := make(chan struct{})
	go func() { fanWatchLoop(newWatchState(cols), cfg, keys, make(chan time.Time)); close(done) }()
	t.Cleanup(func() { keys <- 'q'; <-done })

	deadline := time.After(10 * time.Second)
	var last string
	for {
		select {
		case last = <-frames:
			if strings.Contains(last, "cluster a: no_answer_in_time") {
				t.Fatalf("a read that answered within the timer was reported silent:\n%s", last)
			}
			if strings.Contains(last, "cluster a: answered") {
				return
			}
		case <-deadline:
			t.Fatalf("cluster a never reached answered:\n%s", last)
		}
	}
}

// A watch fetch runs on its own goroutine and can outlive the loop that
// started it, so it must not read fanOutTimeout, which a test's cleanup
// restores (#1093). The config keeps the timer it was built with: a fetch
// still in flight when the variable changes is bounded by the copy, and under
// -race the change is not a race.
func TestWatchFetchKeepsTheTimerItWasBuiltWith(t *testing.T) {
	old := fanOutTimeout
	fanOutTimeout = 200 * time.Millisecond
	t.Cleanup(func() { fanOutTimeout = old })
	f := startSD(t, sdStart, []api.Session{row("w", "s1")})
	f.delay = 400 * time.Millisecond
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	cfgFile, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	tg, err := resolveCluster(cfgFile, "a")
	if err != nil {
		t.Fatal(err)
	}
	params, err := json.Marshal(getRequest(fanOutResource, false))
	if err != nil {
		t.Fatal(err)
	}
	cfg := newFanWatchConfig([]clusterTarget{tg}, params, time.Second)
	got := make(chan fanFetch, 1)
	go func() { got <- cfg.fetch("a", 1, true) }()
	fanOutTimeout = 10 * time.Second // what a cleanup does while the fetch runs
	ff := <-got
	if ff.rec.Outcome != outcomeNoAnswer {
		t.Errorf("fetch outcome = %s (%s), want no_answer under the 200ms timer the config was built with", ff.rec.Outcome, ff.rec.Cause)
	}
}
