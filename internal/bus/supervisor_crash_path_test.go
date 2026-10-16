package bus

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/events"
)

// These tests record what bus.Supervisor does today on its crash path, so the
// move of that lifecycle into workload.Process (restructure plan, Move 2) can
// be shown to keep it. They assert the ordered bus.* kinds of each scenario.
// The bus.crashed message is pinned by kind, severity and the "restart #n"
// shape, not by its full text.

var crashedShape = regexp.MustCompile(`^nats-server pid \d+ exited \(.*\); restart #(\d+) in \S+, sessions hold until it is back$`)

// lifecycleEvents is the started, crashed and stopped events in the order the
// ring saw them.
func lifecycleEvents(r *events.Ring) []events.Event {
	var out []events.Event
	for _, e := range r.Snapshot(events.Filter{}, 0) {
		switch e.Kind {
		case events.KindBusStarted, events.KindBusCrashed, events.KindBusStopped:
			out = append(out, e)
		}
	}
	return out
}

func lifecycleKinds(r *events.Ring) []events.Kind {
	var out []events.Kind
	for _, e := range lifecycleEvents(r) {
		out = append(out, e.Kind)
	}
	return out
}

func waitUntil(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// startedCount is how many bus.started events the ring holds. The broker is
// ready a moment before its bus.started is emitted, so a test that reads the
// ring waits on this and not on Status.
func startedCount(r *events.Ring) int {
	n := 0
	for _, k := range lifecycleKinds(r) {
		if k == events.KindBusStarted {
			n++
		}
	}
	return n
}

func startedCrash(t *testing.T, s *Supervisor) int {
	t.Helper()
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s.Status().PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestCrashRespawnAndReadyEmitCrashedThenStarted(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	pid := startedCrash(t, s)
	waitUntil(t, "the second bus.started", func() bool { return startedCount(ring) >= 2 && s.Status().PID != pid })

	evs := lifecycleEvents(ring)
	want := []events.Kind{events.KindBusStarted, events.KindBusCrashed, events.KindBusStarted}
	if got := lifecycleKinds(ring); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	crashed := evs[1]
	if crashed.Severity != events.SeverityWarning {
		t.Errorf("bus.crashed severity = %v, want warning", crashed.Severity)
	}
	if m := crashedShape.FindStringSubmatch(crashed.Message); m == nil || m[1] != "1" {
		t.Errorf("bus.crashed message = %q, want the shape %q with restart #1", crashed.Message, crashedShape)
	}
	if evs[2].Severity != events.SeverityInfo || !strings.Contains(evs[2].Message, "restarted (#1)") {
		t.Errorf("second bus.started = %v %q, want info naming restarted (#1)", evs[2].Severity, evs[2].Message)
	}
}

// Stop during the backoff wait ends the restart: nothing is spawned and the
// ring ends on the crash and the stop.
func TestStopDuringTheBackoffWaitSpawnsNothing(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	s.backoff = func(int) time.Duration { return 500 * time.Millisecond }
	startedCrash(t, s)
	waitUntil(t, "bus.crashed", func() bool { return slices.Contains(lifecycleKinds(ring), events.KindBusCrashed) })
	if st := s.Status(); st.Ready {
		t.Fatalf("status during the wait = %+v, want not ready", st)
	}

	s.Stop(false)
	time.Sleep(1200 * time.Millisecond) // past the wait: a respawn, were there one, has no other signal
	if st := s.Status(); st.Ready || st.PID != 0 || st.Restarts != 1 {
		t.Errorf("status after Stop = %+v, want down, no pid, restarts 1", st)
	}
	for _, k := range lifecycleKinds(ring)[1:] {
		if k == events.KindBusStarted {
			t.Errorf("a broker was started after Stop: kinds %v", lifecycleKinds(ring))
		}
	}
}

// A respawn that fails goes round again, counted as the next restart, and the
// broker is ready once a respawn works.
func TestFailedRespawnRetriesThenBecomesReady(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := m.ConfPath()
	if err := os.Rename(conf, conf+".away"); err != nil {
		t.Fatal(err)
	}
	pid := s.Status().PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "a second bus.crashed", func() bool {
		n := 0
		for _, k := range lifecycleKinds(ring) {
			if k == events.KindBusCrashed {
				n++
			}
		}
		return n >= 2
	})
	if err := os.Rename(conf+".away", conf); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the second bus.started", func() bool { return startedCount(ring) >= 2 })

	evs := lifecycleEvents(ring)
	kinds := lifecycleKinds(ring)
	if kinds[0] != events.KindBusStarted || kinds[len(kinds)-1] != events.KindBusStarted {
		t.Fatalf("kinds = %v, want the first and last to be bus.started", kinds)
	}
	var crashes []events.Event
	for _, e := range evs {
		if e.Kind == events.KindBusCrashed {
			crashes = append(crashes, e)
		}
	}
	for i, c := range crashes {
		if c.Severity != events.SeverityWarning {
			t.Errorf("crash %d severity = %v, want warning", i+1, c.Severity)
		}
		m := crashedShape.FindStringSubmatch(c.Message)
		if m == nil || m[1] != string(rune('1'+i)) {
			t.Errorf("crash %d message = %q, want the shape with restart #%d", i+1, c.Message, i+1)
		}
	}
	if !strings.Contains(crashes[1].Message, "read "+conf) {
		t.Errorf("the second crash does not name the failed respawn: %q", crashes[1].Message)
	}
	last := evs[len(evs)-1]
	if !strings.Contains(last.Message, "restarted (#") {
		t.Errorf("final bus.started = %q, want a restarted (#n) start", last.Message)
	}
	if st := s.Status(); st.Restarts != len(crashes) {
		t.Errorf("Restarts = %d, want %d, one per crash event", st.Restarts, len(crashes))
	}
}

// Restart that cannot respawn reports it and leaves the broker down.
func TestRestartThatCannotRespawnLeavesTheBrokerDown(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf := m.ConfPath()
	if err := os.Rename(conf, conf+".away"); err != nil {
		t.Fatal(err)
	}
	err := s.Restart("test")
	if err == nil || !strings.Contains(err.Error(), "read "+conf) {
		t.Fatalf("Restart = %v, want an error naming the unreadable conf", err)
	}
	if st := s.Status(); st.Ready || st.PID != 0 {
		t.Errorf("status = %+v, want down with no pid", st)
	}
}

func TestListenerThatNeverAnswersFailsTheStart(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	script := filepath.Join(t.TempDir(), "mute-broker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.binary = script
	s.dialTimeout = 300 * time.Millisecond
	// The script becomes "sleep 30" after the supervisor recorded its
	// identity, so Stop cannot prove it is the broker and leaves it. This test
	// started it, so this test ends it.
	var spawned atomic.Int64
	register := s.onSpawn
	s.onSpawn = func(pid int) {
		spawned.Store(int64(pid))
		register(pid)
	}
	t.Cleanup(func() { endSpawnedChild(t, int(spawned.Load())) })
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not answer on") {
		t.Fatalf("Start = %v, want the listener timeout", err)
	}
	if s.Ready() {
		t.Error("ready after a listener that never answered")
	}
	if slices.Contains(lifecycleKinds(ring), events.KindBusStarted) {
		t.Errorf("bus.started after a failed start: %v", lifecycleKinds(ring))
	}
}

// endSpawnedChild ends a child this test's supervisor started and reaps it, so
// no "sleep 30" outlives the test. It fails the test if the child is still
// there after a few seconds.
func endSpawnedChild(t *testing.T, pid int) {
	t.Helper()
	if pid == 0 {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("TERM to the child it started, pid %d: %v", pid, err)
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ws syscall.WaitStatus
		got, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
		if got == pid || errors.Is(err, syscall.ECHILD) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the child it started, pid %d, is still running after TERM", pid)
}

func TestProvisioningErrorFailsTheStartAfterTheListenerIsUp(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	s.AfterReady = func() error { return errors.New("provision refused") }
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "provisioning failed") || !strings.Contains(err.Error(), "provision refused") {
		t.Fatalf("Start = %v, want the provisioning error", err)
	}
	if s.Ready() {
		t.Error("ready after provisioning failed")
	}
	if slices.Contains(lifecycleKinds(ring), events.KindBusStarted) {
		t.Errorf("bus.started after a failed start: %v", lifecycleKinds(ring))
	}
}

// fakeBroker plants a process the prober can prove, signals recorded and
// delivered nowhere.
type fakeBroker struct {
	t      *testing.T
	mu     sync.Mutex
	id     childproof.Identity
	gone   bool
	sent   []sentSignal
	killOK bool
	onTerm func(*fakeBroker)
	// failSig makes a signal of this number fail, as a permission error would.
	failSig syscall.Signal
}

func (f *fakeBroker) read(pid int) (childproof.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone {
		return childproof.Identity{}, syscall.ESRCH
	}
	return f.id, nil
}

func (f *fakeBroker) kill(pid int, sig syscall.Signal) error {
	f.mu.Lock()
	f.sent = append(f.sent, sentSignal{pid, sig})
	onTerm := f.onTerm
	ok := (sig != syscall.SIGKILL || f.killOK) && (f.failSig == 0 || sig != f.failSig)
	f.mu.Unlock()
	if sig == syscall.SIGTERM && onTerm != nil {
		onTerm(f)
	}
	if !ok {
		return errors.New("operation not permitted")
	}
	return nil
}

func (f *fakeBroker) exit() {
	f.mu.Lock()
	f.gone = true
	f.mu.Unlock()
}

func (f *fakeBroker) signals() []sentSignal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentSignal(nil), f.sent...)
}

func newFakeBroker(t *testing.T) (*fakeBroker, *Supervisor, childproof.Child, int) {
	t.Helper()
	f := &fakeBroker{t: t}
	p, err := childproof.NewForTest(f.read, f.kill)
	if err != nil {
		t.Fatal(err)
	}
	pid := reapedPid(t)
	f.id = childproof.Identity{Start: "darwin:100.000000", Exe: "/opt/bin/nats-server", Argv: []string{"nats-server", "-c", "/c"}, Pgid: pid}
	c, err := p.Prove(pid, f.id)
	if err != nil {
		t.Fatal(err)
	}
	return f, &Supervisor{prober: p}, c, pid
}

// With no grace set the stop waits the default grace, not none: a broker that
// takes a moment to exit after TERM is not killed.
func TestTerminateWithNoGraceWaitsTheDefaultBeforeKilling(t *testing.T) {
	f, s, c, pid := newFakeBroker(t)
	f.onTerm = func(f *fakeBroker) { time.AfterFunc(250*time.Millisecond, f.exit) }
	start := time.Now()
	s.terminate(c)
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("terminate returned after %v, before the broker exited", took)
	}
	if got, want := f.signals(), []sentSignal{{-pid, syscall.SIGTERM}}; !slices.Equal(got, want) {
		t.Errorf("signals = %v, want only TERM", got)
	}
}

// A kill that fails is logged and ends the stop; nothing else is signalled.
func TestTerminateReportsAKillItCouldNotSend(t *testing.T) {
	f, s, c, pid := newFakeBroker(t)
	s.grace = 40 * time.Millisecond
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s.terminate(c)

	if got, want := f.signals(), []sentSignal{{-pid, syscall.SIGTERM}, {-pid, syscall.SIGKILL}}; !slices.Equal(got, want) {
		t.Errorf("signals = %v, want TERM then KILL", got)
	}
	if !strings.Contains(logs.String(), "not killing nats-server pid") {
		t.Errorf("log = %q, want the failed kill reported", logs.String())
	}
}

// The wait ends and the stop flag is already set, but the context is not
// cancelled: the second guard is what keeps the respawn from happening. The
// test holds the supervisor lock across the end of the wait, so the flag is set
// before onCrash can take the lock.
func TestWaitThatEndsWithStopSetSpawnsNothing(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	s.backoff = func(int) time.Duration { return 400 * time.Millisecond }
	startedCrash(t, s)
	waitUntil(t, "bus.crashed", func() bool { return slices.Contains(lifecycleKinds(ring), events.KindBusCrashed) })

	s.mu.Lock()
	s.stopping = true
	time.Sleep(700 * time.Millisecond) // the wait ends while onCrash cannot take the lock
	s.mu.Unlock()
	time.Sleep(700 * time.Millisecond)

	if st := s.Status(); st.Ready {
		t.Errorf("status = %+v, want a broker that was not respawned", st)
	}
	if n := startedCount(ring); n != 1 {
		t.Errorf("bus.started events = %d after the wait ended with the stop flag set, want the one from the first start: %v", n, lifecycleKinds(ring))
	}
}

// A failed SIGHUP to an adopted broker is logged and the start goes on: the
// adopted broker becomes the ready one.
func TestAdoptedBrokerThatCannotBeSignalledIsStillAdopted(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	quiet(s)
	f := &fakeBroker{t: t, failSig: syscall.SIGHUP}
	p, err := childproof.NewForTest(f.read, f.kill)
	if err != nil {
		t.Fatal(err)
	}
	s.prober = p
	s.grace = 40 * time.Millisecond
	pid := reapedPid(t)
	f.id = brokerIdentity(s, "darwin:100.000000")
	f.id.Pgid = pid
	writePidfile(t, s, pid)
	writeSidecar(t, s, pid, f.id)
	standInListener(t, s)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start = %v, want the failed SIGHUP to be logged and the adoption to go on", err)
	}
	if st := s.Status(); !st.Adopted || !st.Ready || st.PID != pid {
		t.Errorf("status = %+v, want adopted, ready, pid %d", st, pid)
	}
	if !strings.Contains(logs.String(), "reload adopted nats-server pid") {
		t.Errorf("log = %q, want the failed reload reported", logs.String())
	}
	started := eventsOfKind(ring, events.KindBusStarted)
	if len(started) != 1 || !strings.Contains(started[0].Message, "adopted") {
		t.Errorf("bus.started events = %+v, want one naming the adoption", started)
	}
}
