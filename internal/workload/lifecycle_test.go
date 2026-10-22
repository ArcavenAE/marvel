package workload

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/backoff"
	"github.com/arcavenae/marvel/internal/childproof"
)

// recDriver records every Driver call. hook, when set, runs at the top of
// each call and of each ProcessSpec func, with the name of the call.
type recDriver struct {
	mu       sync.Mutex
	calls    []string
	starts   []StartInfo
	exits    []Exit
	readyErr error
	ticks    atomic.Int32
	hook     func(where string)
}

func (d *recDriver) at(where string) {
	if d.hook != nil {
		d.hook(where)
	}
}

func (d *recDriver) Ready(ctx context.Context, c ChildInfo) error {
	d.at("Ready")
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "Ready")
	return d.readyErr
}

func (d *recDriver) Started(ctx context.Context, s StartInfo) {
	d.at("Started")
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "Started")
	d.starts = append(d.starts, s)
}

func (d *recDriver) Tick(ctx context.Context) {
	d.at("Tick")
	if _, ok := ctx.Deadline(); !ok {
		panic("Tick ran with no deadline")
	}
	d.ticks.Add(1)
}

func (d *recDriver) Exited(e Exit) {
	d.at("Exited")
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "Exited")
	d.exits = append(d.exits, e)
}

func (d *recDriver) startsCopy() []StartInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.starts)
}

func (d *recDriver) exitsCopy() []Exit {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.exits)
}

// clock is an injected restart wait: every wait asked for is recorded, and
// the wait ends only when the test fires it.
type clock struct {
	mu    sync.Mutex
	waits []time.Duration
	fire  chan time.Time
	at    time.Time
}

func newClock() *clock {
	return &clock{fire: make(chan time.Time, 1), at: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC)}
}

func (c *clock) after(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	return c.fire
}

func (c *clock) waitsCopy() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.waits)
}

var testSchedule = backoff.Schedule{Initial: 30 * time.Millisecond, Max: 120 * time.Millisecond}

func procSpec(t *testing.T) ProcessSpec {
	t.Helper()
	bin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	dir := t.TempDir()
	return ProcessSpec{
		Name:      "test",
		Binary:    bin,
		Args:      []string{"60"},
		LogPath:   filepath.Join(dir, "child.log"),
		PidFile:   filepath.Join(dir, "child.pid"),
		Grace:     300 * time.Millisecond,
		AdoptPoll: 25 * time.Millisecond,
	}
}

// newProc returns a Process over spec with a recording driver, the real
// prober and an injected clock, and stops it when the test ends.
//
// It also records every child the Process starts, proven from the handle in
// BeforePidFile. After Stop, a recorded child that is still the same process
// was lost by the code under test: Stop ends only the child Process watches,
// and a lost child leads its own group, so it would outlive the test. The
// cleanup fails the test and Terminates it, re-proving it first so it never
// reaches a reused pid.
func newProc(t *testing.T, spec ProcessSpec) (*Process, *recDriver, *clock) {
	t.Helper()
	prober := childproof.New()
	var mu sync.Mutex
	var started []childproof.Child
	before := spec.BeforePidFile
	spec.BeforePidFile = func(cmd *exec.Cmd) error {
		if child, err := prober.Spawned(cmd); err == nil {
			mu.Lock()
			started = append(started, child)
			mu.Unlock()
		}
		if before != nil {
			return before(cmd)
		}
		return nil
	}
	// Registered before the Stop below, so it runs after it.
	t.Cleanup(func() {
		mu.Lock()
		children := slices.Clone(started)
		mu.Unlock()
		for _, child := range children {
			if !prober.Same(child) {
				continue
			}
			t.Errorf("child pid %d outlived its Process; ending it", child.Pid())
			if err := Terminate(prober, child, spec.Grace); err != nil {
				t.Errorf("end the lost child pid %d: %v", child.Pid(), err)
			}
		}
	})
	d := &recDriver{}
	c := newClock()
	p := New(spec, d, prober, testSchedule)
	p.now = func() time.Time { return c.at }
	p.after = c.after
	t.Cleanup(func() { p.Stop(false) })
	return p, d, c
}

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// killPid ends a test child. A pid at or below 1 fails the test instead:
// kill(0) is this process's own group and kill(-1) is every process the user
// owns.
func killPid(t *testing.T, pid int) {
	t.Helper()
	if pid <= 1 {
		t.Fatalf("no child pid to kill (%d)", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
}

// Spawn starts the child, runs Ready then Started, and the snapshot shows a
// ready, proven, spawned child of generation 1.
func TestSpawnRunsReadyThenStarted(t *testing.T) {
	p, d, _ := newProc(t, procSpec(t))
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	st := p.Snapshot()
	if !st.Ready || st.Pid == 0 || st.Adopted || st.Gen != 1 || st.PidState != PidStateProven {
		t.Errorf("snapshot = %+v, want ready, a pid, spawned, gen 1, pidfile proven", st)
	}
	if !p.prober.Same(st.Child) {
		t.Error("the snapshot's child is not the proven running child")
	}
	starts := d.startsCopy()
	if len(starts) != 1 || starts[0].How != StartSpawned || starts[0].Child.Gen != 1 || starts[0].Child.Env == nil {
		t.Errorf("starts = %+v, want one Spawned start of gen 1 with its environment", starts)
	}
	if want := []string{"Ready", "Started"}; !slices.Equal(d.calls, want) {
		t.Errorf("calls = %v, want %v", d.calls, want)
	}
}

// A crash is reported with the restart count and the schedule's wait, and
// nothing is spawned until the wait ends. Then the child is respawned as a
// Restarted start with the generation bumped.
func TestCrashRespawnsOnTheScheduleWithTheGenerationBumped(t *testing.T) {
	p, d, c := newProc(t, procSpec(t))
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := p.Snapshot().Pid
	killPid(t, first)
	until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })

	e := d.exitsCopy()[0]
	if e.Requested || e.Restarts != 1 || e.RetryIn != testSchedule.Wait(1) || e.Err == nil {
		t.Errorf("exit = %+v, want a crash, restart 1, retry in %s, with its error", e, testSchedule.Wait(1))
	}
	st := p.Snapshot()
	if st.Ready || st.Pid != 0 || st.Restarts != 1 || !st.BackoffUntil.Equal(c.at.Add(testSchedule.Wait(1))) {
		t.Errorf("snapshot in the wait = %+v, want down, restarts 1, backoff until %s", st, c.at.Add(testSchedule.Wait(1)))
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(d.startsCopy()); n != 1 {
		t.Fatalf("%d starts before the wait ended, want 1", n)
	}

	c.fire <- c.at
	until(t, "the respawn", func() bool { return len(d.startsCopy()) == 2 })
	s := d.startsCopy()[1]
	if s.How != StartRestarted || s.Restarts != 1 || s.Reason != "" || s.Child.Gen != 2 || s.Child.Child.Pid() == first {
		t.Errorf("respawn = %+v, want Restarted, restart 1, no reason, gen 2, a new pid", s)
	}
	if st := p.Snapshot(); !st.Ready || st.Gen != 2 || !st.BackoffUntil.IsZero() {
		t.Errorf("snapshot after the respawn = %+v, want ready, gen 2, no backoff", st)
	}
	if got := c.waitsCopy(); !slices.Equal(got, []time.Duration{testSchedule.Wait(1)}) {
		t.Errorf("waits = %v, want one of %s", got, testSchedule.Wait(1))
	}
}

// Stop(keep=true) leaves the child running and its pidfile in place, and
// reports no exit.
func TestStopKeepLeavesTheChildAndItsPidfile(t *testing.T) {
	spec := procSpec(t)
	p, d, _ := newProc(t, spec)
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	child := p.Snapshot().Child
	t.Cleanup(func() { _ = Terminate(p.prober, child, 0) })
	p.Stop(true)
	if !p.prober.Same(child) {
		t.Error("the child did not survive Stop(keep=true)")
	}
	if _, err := os.Stat(spec.PidFile); err != nil {
		t.Errorf("pidfile after Stop(keep=true): %v", err)
	}
	if n := len(d.exitsCopy()); n != 0 {
		t.Errorf("%d exits reported, want none", n)
	}
}

// Stop ends the child and reports a requested exit. A second Stop does
// nothing, and a Spawn after Stop is refused.
func TestStopEndsTheChildAndReportsARequestedExit(t *testing.T) {
	p, d, _ := newProc(t, procSpec(t))
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	child := p.Snapshot().Child
	p.Stop(false)
	if p.prober.Same(child) {
		t.Error("the child is still running after Stop")
	}
	if ex := d.exitsCopy(); len(ex) != 1 || !ex[0].Requested {
		t.Errorf("exits = %+v, want one requested exit", ex)
	}
	p.Stop(false)
	if n := len(d.exitsCopy()); n != 1 {
		t.Errorf("a second Stop reported again: %d exits", n)
	}
	if err := p.Spawn(context.Background()); !errors.Is(err, ErrStopped) {
		t.Errorf("Spawn after Stop = %v, want ErrStopped", err)
	}
}

// Adopt takes a child the driver proved; it is reported as Adopted with no
// environment, and its exit is seen through the identity poll as a crash.
func TestAdoptAProvenChildAndSeeItExit(t *testing.T) {
	spec := procSpec(t)
	cmd := exec.Command(spec.Binary, "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p, d, _ := newProc(t, spec)
	// Proven before the reaper starts: Spawned reads the command's state,
	// which Wait writes.
	child, err := p.prober.Spawned(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-reaped })
	if err := p.Adopt(context.Background(), child); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if st := p.Snapshot(); !st.Adopted || !st.Ready || st.Pid != cmd.Process.Pid {
		t.Errorf("snapshot = %+v, want adopted, ready, pid %d", st, cmd.Process.Pid)
	}
	if s := d.startsCopy(); len(s) != 1 || s[0].How != StartAdopted || s[0].Child.Env != nil {
		t.Errorf("starts = %+v, want one Adopted start with no environment", s)
	}

	killPid(t, cmd.Process.Pid)
	<-reaped
	until(t, "the adopted child's exit", func() bool { return len(d.exitsCopy()) == 1 })
	if e := d.exitsCopy()[0]; e.Requested || e.Restarts != 1 {
		t.Errorf("exit = %+v, want a crash with restart 1", e)
	}
}

// Stop on an adopted child ends it and reports a requested exit.
func TestStopOnAnAdoptedChildIsARequestedExit(t *testing.T) {
	spec := procSpec(t)
	cmd := exec.Command(spec.Binary, "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p, d, _ := newProc(t, spec)
	// Proven before the reaper starts: Spawned reads the command's state,
	// which Wait writes.
	child, err := p.prober.Spawned(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-reaped })
	if err := p.Adopt(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	p.Stop(false)
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the adopted child was not ended by Stop")
	}
	if e := d.exitsCopy(); len(e) != 1 || !e[0].Requested {
		t.Errorf("exits = %+v, want one requested exit", e)
	}
}

// Restart is requested, not a crash: the old child ends with a requested
// exit, the new one starts with the reason, and the crash count stays.
func TestRestartIsRequestedAndNotCountedAsACrash(t *testing.T) {
	p, d, c := newProc(t, procSpec(t))
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := p.Snapshot().Child
	if err := p.Restart("new seed"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if p.prober.Same(old) {
		t.Error("the old child survived Restart")
	}
	if e := d.exitsCopy(); len(e) != 1 || !e[0].Requested || e[0].Restarts != 0 {
		t.Errorf("exits = %+v, want one requested exit with restarts 0", e)
	}
	s := d.startsCopy()
	if len(s) != 2 || s[1].How != StartRestarted || s[1].Reason != "new seed" || s[1].Restarts != 0 || s[1].Child.Gen != 2 {
		t.Errorf("starts = %+v, want a Restarted start with the reason, restarts 0, gen 2", s)
	}
	if st := p.Snapshot(); st.Restarts != 0 || !st.Ready {
		t.Errorf("snapshot = %+v, want ready with restarts 0", st)
	}
	if w := c.waitsCopy(); len(w) != 0 {
		t.Errorf("a requested restart waited %v, want no backoff", w)
	}
}

// A Restart during the wait after a crash becomes the start, and the wait no
// longer spawns a second child.
func TestRestartDuringTheWaitBecomesTheStart(t *testing.T) {
	p, d, c := newProc(t, procSpec(t))
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	killPid(t, p.Snapshot().Pid)
	until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })
	if err := p.Restart("operator"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	c.fire <- c.at
	time.Sleep(100 * time.Millisecond)
	s := d.startsCopy()
	if len(s) != 2 || s[1].Reason != "operator" {
		t.Errorf("starts = %+v, want the restart as the only start after the crash", s)
	}
}

// A Spawn or an Adopt during the wait after a crash becomes the start, and the
// wait no longer spawns a second child: the child it took stays the one
// watched, and Stop ends it (review 5595287694).
func TestSpawnOrAdoptDuringTheWaitBecomesTheStart(t *testing.T) {
	for _, how := range []string{"Spawn", "Adopt"} {
		t.Run(how, func(t *testing.T) {
			spec := procSpec(t)
			p, d, c := newProc(t, spec)
			if err := p.Spawn(context.Background()); err != nil {
				t.Fatal(err)
			}
			killPid(t, p.Snapshot().Pid)
			until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })

			switch how {
			case "Spawn":
				if err := p.Spawn(context.Background()); err != nil {
					t.Fatalf("Spawn during the wait: %v", err)
				}
			case "Adopt":
				cmd := exec.Command(spec.Binary, "60")
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				child, err := p.prober.Spawned(cmd)
				if err != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal(err)
				}
				reaped := make(chan struct{})
				go func() { _ = cmd.Wait(); close(reaped) }()
				t.Cleanup(func() { _ = cmd.Process.Kill(); <-reaped })
				if err := p.Adopt(context.Background(), child); err != nil {
					t.Fatalf("Adopt during the wait: %v", err)
				}
			}
			taken := p.Snapshot().Child
			if st := p.Snapshot(); !st.BackoffUntil.IsZero() {
				t.Errorf("backoff until %s after the %s, want none", st.BackoffUntil, how)
			}

			c.fire <- c.at
			time.Sleep(150 * time.Millisecond) // a respawn, were there one, has no other signal
			if n := len(d.startsCopy()); n != 2 {
				t.Errorf("%d starts after the wait ended, want 2: the wait spawned a child over the one %s took", n, how)
			}
			if st := p.Snapshot(); st.Pid != taken.Pid() {
				t.Errorf("the watched child changed from pid %d to pid %d after the wait", taken.Pid(), st.Pid)
			}
			p.Stop(false)
			if p.prober.Same(taken) {
				t.Errorf("the child %s took is still running after Stop", how)
			}
		})
	}
}

// A Spawn that fails during the wait after a crash leaves the respawn armed:
// when the wait ends, the respawn starts the child and the snapshot's
// BackoffUntil, which named it as due, is cleared by that start.
func TestFailedSpawnDuringTheWaitKeepsTheRespawn(t *testing.T) {
	spec := procSpec(t)
	var fail atomic.Bool
	spec.BeforePidFile = func(*exec.Cmd) error {
		if fail.Load() {
			return errors.New("planted BeforePidFile failure")
		}
		return nil
	}
	p, d, c := newProc(t, spec)
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := p.Snapshot().Pid
	killPid(t, first)
	until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })

	fail.Store(true)
	if err := p.Spawn(context.Background()); err == nil {
		t.Fatal("Spawn during the wait succeeded with BeforePidFile failing")
	}
	fail.Store(false)
	if st := p.Snapshot(); st.BackoffUntil.IsZero() {
		t.Fatal("the failed Spawn cleared the respawn's due time")
	}

	c.fire <- c.at
	until(t, "the respawn after the wait", func() bool { return len(d.startsCopy()) == 2 })
	st := p.Snapshot()
	if st.Pid <= 1 || st.Pid == first || !st.Ready || !st.BackoffUntil.IsZero() {
		t.Errorf("after the respawn: pid %d (first %d), ready %v, backoff until %s; want a new ready child and no wait", st.Pid, first, st.Ready, st.BackoffUntil)
	}
	if s := d.startsCopy(); s[1].How != StartRestarted {
		t.Errorf("second start = %v, want %v", s[1].How, StartRestarted)
	}
}

func TestRestartBeforeStartIsAnErrorAndStopIsANoOp(t *testing.T) {
	p, d, _ := newProc(t, procSpec(t))
	if err := p.Restart("early"); err == nil {
		t.Error("Restart before any start succeeded")
	}
	p.Stop(false)
	if len(d.calls) != 0 {
		t.Errorf("calls = %v, want none", d.calls)
	}
	if err := p.Spawn(context.Background()); err != nil {
		t.Errorf("Spawn after a Stop that came before any start: %v", err)
	}
}

// A Ready error fails Spawn; the child keeps running and is watched, not ready.
func TestReadyErrorFailsSpawnAndTheChildIsStillWatched(t *testing.T) {
	p, d, _ := newProc(t, procSpec(t))
	d.readyErr = errors.New("listener did not answer")
	if err := p.Spawn(context.Background()); err == nil || err.Error() != "listener did not answer" {
		t.Fatalf("Spawn = %v, want the Ready error", err)
	}
	st := p.Snapshot()
	if st.Ready || st.Pid == 0 {
		t.Errorf("snapshot = %+v, want a running child that is not ready", st)
	}
	if len(d.startsCopy()) != 0 {
		t.Error("Started ran after a Ready error")
	}
	killPid(t, st.Pid)
	until(t, "the exit of the unready child", func() bool { return len(d.exitsCopy()) == 1 })
}

// Once the parent ends, the loop stops watching, and Stop still ends the
// child: a daemon cancels its context before it stops its services.
func TestStopAfterTheParentEndsStillEndsTheChild(t *testing.T) {
	p, d, _ := newProc(t, procSpec(t))
	ctx, cancel := context.WithCancel(context.Background())
	if err := p.Spawn(ctx); err != nil {
		t.Fatal(err)
	}
	child := p.Snapshot().Child
	cancel()
	time.Sleep(50 * time.Millisecond)
	p.Stop(false)
	if p.prober.Same(child) {
		t.Error("the child survived Stop after the parent ended")
	}
	if e := d.exitsCopy(); len(e) != 1 || !e[0].Requested {
		t.Errorf("exits = %+v, want one requested exit", e)
	}
}

// The lock rule, single goroutine: at every Driver call and every
// ProcessSpec func the Process mutex is free. Each takes it with TryLock and
// releases it at once. This shows the lock was free at each scripted call; it
// does not prove the rule for paths the script does not reach.
func TestNoDriverCallOrSpecFuncRunsUnderTheProcessLock(t *testing.T) {
	spec := procSpec(t)
	spec.TickEvery = 20 * time.Millisecond
	var p *Process
	var mu sync.Mutex
	var held []string
	seen := map[string]bool{}
	check := func(where string) {
		mu.Lock()
		defer mu.Unlock()
		seen[where] = true
		if !p.mu.TryLock() {
			held = append(held, where)
			return
		}
		p.mu.Unlock()
	}
	spec.MintedEnv = func() []string { check("MintedEnv"); return nil }
	spec.Check = func([]string) error { check("Check"); return nil }
	spec.BeforePidFile = func(*exec.Cmd) error { check("BeforePidFile"); return nil }
	p, d, c := newProc(t, spec)
	d.hook = check

	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	until(t, "a tick", func() bool { return d.ticks.Load() > 0 })
	killPid(t, p.Snapshot().Pid)
	until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })
	c.fire <- c.at
	until(t, "the respawn", func() bool { return len(d.startsCopy()) == 2 })
	if err := p.Restart("lock rule"); err != nil {
		t.Fatal(err)
	}
	p.Stop(false)

	mu.Lock()
	defer mu.Unlock()
	for _, w := range []string{"Ready", "Started", "Tick", "Exited", "MintedEnv", "Check", "BeforePidFile"} {
		if !seen[w] {
			t.Errorf("%s never ran, so the rule was not checked there", w)
		}
	}
	if len(held) != 0 {
		t.Errorf("the Process lock was held during %v", held)
	}
}

// The lock rule, concurrent: a crash, Snapshot and Tick run together. Run
// under -race (just test-race) it shows the shared state is guarded.
func TestCrashSnapshotAndTickRunTogether(t *testing.T) {
	spec := procSpec(t)
	spec.TickEvery = 5 * time.Millisecond
	p, d, c := newProc(t, spec)
	if err := p.Spawn(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = p.Snapshot()
				}
			}
		}()
	}
	killPid(t, p.Snapshot().Pid)
	until(t, "the crash", func() bool { return len(d.exitsCopy()) == 1 })
	c.fire <- c.at
	until(t, "the respawn", func() bool { return len(d.startsCopy()) == 2 })
	until(t, "ticks", func() bool { return d.ticks.Load() > 2 })
	close(stop)
	wg.Wait()
}

// Terminate sends SIGTERM and returns once the child is gone; a child that
// ignores SIGTERM is killed after the grace. The shell writes a marker once its
// trap is set, and is proven only then: /bin/sh may exec into another shell
// first, which changes its identity.
func TestTerminateEndsAChildAndKillsOneThatIgnoresTerm(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	const grace = 400 * time.Millisecond
	prober := childproof.New()
	for name, ignores := range map[string]bool{"ends on TERM": false, "ignores TERM": true} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "ready")
			cmd := exec.Command("sleep", "60")
			if ignores {
				cmd = exec.Command(sh, "-c", `trap '' TERM; echo > "$1"; sleep 60 & wait`, "sh", marker)
			}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if ignores {
				until(t, "the shell's marker", func() bool { _, err := os.Stat(marker); return err == nil })
			}
			child, err := prober.Spawned(cmd)
			if err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal(err)
			}
			reaped := make(chan struct{})
			go func() { _ = cmd.Wait(); close(reaped) }()
			// Through Terminate, which re-proves the leader first: once it
			// is reaped its pid and group may name another process.
			t.Cleanup(func() { _ = Terminate(prober, child, 50*time.Millisecond); <-reaped })

			began := time.Now()
			if err := Terminate(prober, child, grace); err != nil {
				t.Fatalf("Terminate: %v", err)
			}
			took := time.Since(began)
			select {
			case <-reaped:
			case <-time.After(5 * time.Second):
				t.Fatal("the child outlived Terminate")
			}
			if ignores && took < grace {
				t.Errorf("Terminate returned after %s, before the %s grace, on a child that ignores TERM", took, grace)
			}
			if !ignores && took >= grace {
				t.Errorf("Terminate took %s on a child that ends on TERM, want under the %s grace", took, grace)
			}
		})
	}
}

// Terminate signals nothing when the child is no longer the one proven.
func TestTerminateSignalsNothingWhenTheChildChanged(t *testing.T) {
	var sent []syscall.Signal
	pr, err := childproof.NewForTest(
		func(int) (childproof.Identity, error) { return childproof.Identity{Start: "later"}, nil },
		func(_ int, sig syscall.Signal) error { sent = append(sent, sig); return nil })
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep")
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	stale, err := childproof.NewForTest(
		func(int) (childproof.Identity, error) {
			return childproof.Identity{Start: "earlier", Ppid: os.Getpid()}, nil
		},
		func(int, syscall.Signal) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	child, err := stale.Spawned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := Terminate(pr, child, 50*time.Millisecond); err == nil {
		t.Error("Terminate succeeded on a child that is no longer the one proven")
	}
	if len(sent) != 0 {
		t.Errorf("signals sent: %v, want none", sent)
	}
}
