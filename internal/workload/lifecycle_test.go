package workload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/pidfile"
)

// fixture is a Process whose child is this test binary re-run as a sleeper, so
// the lifecycle runs against a real process, a real pidfile and the real
// identity prober. The listener "answers" while the child it last saw is
// alive.
type fixture struct {
	t      *testing.T
	dir    string
	pidf   string
	prober *childproof.Prober

	mu       sync.Mutex
	current  int // the pid the listener answers for
	spawned  []int
	readies  []string
	crashes  []int
	stopped  []int
	firsts   atomic.Int32
	polls    atomic.Int32
	removed  atomic.Int32
	removedP atomic.Int32
	stale    []Verdict

	cfg Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), prober: childproof.New()}
	f.pidf = filepath.Join(f.dir, "child.pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = Config{
		Name: "svc", Label: "sleeper", Listen: "test:0", LogPath: filepath.Join(f.dir, "child.log"),
		PidFile: f.pidf, Prober: f.prober,
		Spec: func() (ProcessSpec, error) {
			return ProcessSpec{
				Name: "svc", Binary: exe, Args: []string{"-test.run=TestHelperProcess"},
				MintedEnv: func() []string { return []string{helperEnv + "=sleep"} },
				LogPath:   filepath.Join(f.dir, "child.log"),
			}, nil
		},
		Backoff:     func(int) time.Duration { return 20 * time.Millisecond },
		DialTimeout: 2 * time.Second,
		AdoptPoll:   20 * time.Millisecond,
		Grace:       2 * time.Second,
		Listener: func(time.Duration) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.current != 0 && syscall.Kill(f.current, 0) == nil
		},
		Classify: f.classify,
		RemoveIdentity: func() {
			f.removed.Add(1)
			_ = os.Remove(f.pidf)
		},
		RemovePidFile: func() {
			f.removedP.Add(1)
			_ = os.Remove(f.pidf)
		},
		Unproven: func(pid int, why string) error { return errors.New("unproven: " + why) },
		Stranger: func() error { return errors.New("stranger") },
		OnStale: func(v Verdict) {
			f.mu.Lock()
			f.stale = append(f.stale, v)
			f.mu.Unlock()
		},
		OnSpawnPid: func(pid int) {
			f.mu.Lock()
			f.current = pid
			f.spawned = append(f.spawned, pid)
			f.mu.Unlock()
			t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		},
		OnReady: func(_ context.Context, how string) {
			f.mu.Lock()
			f.readies = append(f.readies, how)
			f.mu.Unlock()
		},
		OnCrash: func(pid int, _ error, _ int, _ time.Duration) {
			f.mu.Lock()
			f.crashes = append(f.crashes, pid)
			f.mu.Unlock()
		},
		OnStopped: func(pid int) {
			f.mu.Lock()
			f.stopped = append(f.stopped, pid)
			f.mu.Unlock()
		},
		PollFirst: func() { f.firsts.Add(1) },
		Poll:      func(context.Context) { f.polls.Add(1) },
		PollEvery: 10 * time.Millisecond,
	}
	return f
}

// classify proves the pidfile against the live process, the way a driver
// with an identity record would: a pid whose identity reads is proven, no
// pidfile is none.
func (f *fixture) classify() Verdict {
	pid := pidfile.Read(f.pidf)
	if pid == 0 {
		return Verdict{Kind: PidNone}
	}
	id, err := childproof.ReadIdentityForTest(pid)
	if err != nil {
		return Verdict{Pid: pid, Kind: PidStale, Reason: "dead"}
	}
	c, err := f.prober.Prove(pid, id)
	if err != nil {
		return Verdict{Pid: pid, Kind: PidStale, Reason: "unproven"}
	}
	return Verdict{Pid: pid, Kind: PidProven, Child: c}
}

func (f *fixture) lastPid() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.spawned) == 0 {
		return 0
	}
	return f.spawned[len(f.spawned)-1]
}

func (f *fixture) spawnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.spawned)
}

func (f *fixture) readyList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.readies...)
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestProcessStartSpawnsAChildAndRecordsItsPidfile(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(false)
	st := p.State()
	if !st.Ready || st.Adopted || st.Pid == 0 || st.Pid != f.lastPid() || st.PidState != PidProven {
		t.Errorf("state = %+v, want ready, spawned, proven, pid %d", st, f.lastPid())
	}
	if got := pidfile.Read(f.pidf); got != st.Pid {
		t.Errorf("pidfile holds %d, want %d", got, st.Pid)
	}
	if st.Child.Pid() != st.Pid || !f.prober.Same(st.Child) {
		t.Errorf("the proven child = pid %d, want %d", st.Child.Pid(), st.Pid)
	}
	if got := f.readyList(); len(got) != 1 || got[0] != "started" {
		t.Errorf("OnReady saw %v, want one \"started\"", got)
	}
}

func TestProcessRestartsACrashedChildUnderBackoff(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(false)
	first := p.State().Pid
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a second child to be ready", func() bool {
		st := p.State()
		return st.Ready && st.Pid != 0 && st.Pid != first
	})
	st := p.State()
	if st.Restarts != 1 {
		t.Errorf("restarts = %d, want 1", st.Restarts)
	}
	f.mu.Lock()
	crashes := append([]int(nil), f.crashes...)
	f.mu.Unlock()
	if len(crashes) != 1 || crashes[0] != first {
		t.Errorf("OnCrash saw %v, want the one exit of pid %d", crashes, first)
	}
	got := f.readyList()
	if len(got) != 2 || got[0] != "started" || !strings.HasPrefix(got[1], "restarted (#1)") {
		t.Errorf("OnReady saw %v, want started then restarted (#1)", got)
	}
}

func TestProcessRestartReplacesTheChildWithoutCountingACrash(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(false)
	first := p.State().Pid
	if err := p.Restart("seed changed"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	st := p.State()
	if !st.Ready || st.Pid == first || st.Restarts != 0 {
		t.Errorf("state after Restart = %+v, want ready on a new pid with no crash counted", st)
	}
	eventually(t, "the old child to be gone", func() bool { return !alive(first) })
	f.mu.Lock()
	crashes := len(f.crashes)
	f.mu.Unlock()
	if crashes != 0 {
		t.Errorf("a requested restart was reported as %d crash(es)", crashes)
	}
	got := f.readyList()
	if len(got) != 2 || got[1] != "restarted (seed changed)" {
		t.Errorf("OnReady saw %v, want started then restarted (seed changed)", got)
	}
}

func TestProcessRestartBeforeStartRefuses(t *testing.T) {
	f := newFixture(t)
	err := NewProcess(f.cfg).Restart("x")
	if err == nil || !strings.Contains(err.Error(), "restart before start") {
		t.Errorf("Restart before Start = %v, want a restart-before-start refusal", err)
	}
}

func TestProcessStopTerminatesTheChildAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := p.State().Pid
	p.Stop(false)
	eventually(t, "the child to be gone", func() bool { return !alive(pid) })
	if st := p.State(); st.Ready || st.Pid != 0 {
		t.Errorf("state after Stop = %+v, want not ready and no pid", st)
	}
	f.mu.Lock()
	stopped := append([]int(nil), f.stopped...)
	f.mu.Unlock()
	if len(stopped) != 1 || stopped[0] != pid {
		t.Errorf("OnStopped saw %v, want the one stop of pid %d", stopped, pid)
	}
	if f.removed.Load() != 1 {
		t.Errorf("RemoveIdentity ran %d times, want 1", f.removed.Load())
	}
	p.Stop(false)
	if f.removed.Load() != 1 || len(f.stopped) != 1 {
		t.Errorf("a second Stop was not a no-op: removed %d, stopped %v", f.removed.Load(), f.stopped)
	}
}

func TestProcessStopKeepLeavesTheChildForASuccessorToAdopt(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := p.State().Pid
	p.Stop(true)
	if !alive(pid) {
		t.Fatalf("Stop(keep) ended pid %d", pid)
	}
	if got := pidfile.Read(f.pidf); got != pid {
		t.Errorf("the pidfile holds %d after Stop(keep), want %d for the successor", got, pid)
	}
	if f.removed.Load() != 0 {
		t.Errorf("Stop(keep) removed the identity files %d times", f.removed.Load())
	}

	succ := NewProcess(f.cfg)
	if err := succ.Start(context.Background()); err != nil {
		t.Fatalf("successor Start: %v", err)
	}
	defer succ.Stop(false)
	st := succ.State()
	if !st.Adopted || st.Pid != pid || st.PidState != PidProven || !st.Ready {
		t.Errorf("successor state = %+v, want pid %d adopted, proven and ready", st, pid)
	}
	if f.spawnCount() != 1 {
		t.Errorf("the successor spawned a second child (%d spawns), want to adopt", f.spawnCount())
	}
	if got := f.readyList(); got[len(got)-1] != "adopted" {
		t.Errorf("OnReady saw %v, want the last to be adopted", got)
	}
}

func TestProcessAnAdoptedChildThatExitsIsACrash(t *testing.T) {
	f := newFixture(t)
	first := NewProcess(f.cfg)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := first.State().Pid
	first.Stop(true)

	succ := NewProcess(f.cfg)
	if err := succ.Start(context.Background()); err != nil {
		t.Fatalf("successor Start: %v", err)
	}
	defer succ.Stop(false)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a respawned child", func() bool {
		st := succ.State()
		return st.Ready && st.Pid != pid && !st.Adopted
	})
	f.mu.Lock()
	crashes := append([]int(nil), f.crashes...)
	f.mu.Unlock()
	if len(crashes) != 1 || crashes[0] != pid {
		t.Errorf("OnCrash saw %v, want the exit of adopted pid %d", crashes, pid)
	}
}

func TestProcessRefusesAPortAnsweredByAStranger(t *testing.T) {
	f := newFixture(t)
	f.cfg.Listener = func(time.Duration) bool { return true }
	err := NewProcess(f.cfg).Start(context.Background())
	if err == nil || err.Error() != "stranger" {
		t.Errorf("Start = %v, want the stranger refusal", err)
	}
	if f.spawnCount() != 0 {
		t.Errorf("Start spawned %d child(ren) onto a port a stranger holds", f.spawnCount())
	}
}

func TestProcessAStalePidfileIsRemovedAndNothingIsSignalled(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.pidf, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.cfg.Classify = func() Verdict { return Verdict{Pid: os.Getpid(), Kind: PidStale, Reason: "exe"} }
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(false)
	if len(f.stale) != 1 || f.stale[0].Reason != "exe" {
		t.Errorf("OnStale saw %+v, want the one exe verdict", f.stale)
	}
	if f.removed.Load() == 0 {
		t.Error("a stale pidfile was not removed")
	}
	if !alive(os.Getpid()) {
		t.Error("the pid in a stale pidfile was signalled")
	}
}

func TestProcessAStalePidfileOnAnAnsweringPortIsUnproven(t *testing.T) {
	f := newFixture(t)
	f.cfg.Classify = func() Verdict { return Verdict{Pid: 99999, Kind: PidStale, Reason: "start"} }
	f.cfg.Listener = func(time.Duration) bool { return true }
	err := NewProcess(f.cfg).Start(context.Background())
	if err == nil || err.Error() != "unproven: the pidfile does not prove it is ours" {
		t.Errorf("Start = %v, want the unproven refusal", err)
	}
}

func TestProcessALegacyPidfileWithASilentPortIsRemovedThenSpawned(t *testing.T) {
	f := newFixture(t)
	f.cfg.Classify = func() Verdict { return Verdict{Pid: 99999, Kind: PidLegacy} }
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(false)
	if f.removedP.Load() != 1 {
		t.Errorf("RemovePidFile ran %d times, want 1", f.removedP.Load())
	}
	if f.spawnCount() != 1 {
		t.Errorf("spawns = %d, want 1", f.spawnCount())
	}
}

func TestProcessALegacyPidfileOnAnAnsweringPortIsUnproven(t *testing.T) {
	f := newFixture(t)
	f.cfg.Classify = func() Verdict { return Verdict{Pid: 99999, Kind: PidLegacy} }
	f.cfg.Listener = func(time.Duration) bool { return true }
	err := NewProcess(f.cfg).Start(context.Background())
	if err == nil || err.Error() != "unproven: its pidfile has no identity record" {
		t.Errorf("Start = %v, want the unproven refusal", err)
	}
}

func TestProcessAChildThatNeverAnswersFailsTheStartNamingTheLog(t *testing.T) {
	f := newFixture(t)
	f.cfg.DialTimeout = 300 * time.Millisecond
	f.cfg.Listener = func(time.Duration) bool { return false }
	p := NewProcess(f.cfg)
	err := p.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not answer on test:0 within 300ms") || !strings.Contains(err.Error(), f.cfg.LogPath) {
		t.Errorf("Start = %v, want a did-not-answer error naming the listener, the window and the log", err)
	}
	p.Stop(false)
}

func TestProcessAfterReadyFailureFailsTheStart(t *testing.T) {
	f := newFixture(t)
	f.cfg.AfterReady = func() error { return errors.New("no stream") }
	p := NewProcess(f.cfg)
	err := p.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listener up but provisioning failed: no stream") {
		t.Errorf("Start = %v, want the provisioning failure", err)
	}
	p.Stop(false)
}

func TestProcessRunsItsPollHooksOnTheWatchLoopUntilStopped(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eventually(t, "several polls", func() bool { return f.polls.Load() >= 3 })
	if got := f.firsts.Load(); got != 1 {
		t.Errorf("PollFirst ran %d times, want 1", got)
	}
	p.Stop(false)
	after := f.polls.Load()
	time.Sleep(60 * time.Millisecond)
	if f.polls.Load() != after {
		t.Error("Poll kept running after Stop")
	}
}

func TestProcessSignalReachesAReadyChildAndNothingElse(t *testing.T) {
	f := newFixture(t)
	p := NewProcess(f.cfg)
	if _, running, err := p.Signal(syscall.SIGCONT); running || err != nil {
		t.Errorf("Signal before Start = running %v, err %v, want neither", running, err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(false)
	pid, running, err := p.Signal(syscall.SIGCONT)
	if !running || err != nil || pid != p.State().Pid {
		t.Errorf("Signal = pid %d running %v err %v, want the ready child's pid and no error", pid, running, err)
	}
	if !alive(pid) {
		t.Error("a signal ended the child")
	}
}
