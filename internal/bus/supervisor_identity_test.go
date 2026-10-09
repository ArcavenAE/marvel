package bus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/events"
)

// Tests for docs/design/bus-pidfile-identity.md, section 7. Every pid a test
// names is a child the test started: a real sleep it keeps, or one it started
// and reaped, whose number the kernel will not hand out again in the
// milliseconds a test runs. The process table (proctable_test.go) fails the
// test on a signal to anything else.

func startSleepChild(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// reapedPid is the number of a child that has exited and been waited for.
func reapedPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return pid
}

func writePidfile(t *testing.T, s *Supervisor, pid int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.pidFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSidecar(t *testing.T, s *Supervisor, pid int, id childproof.Identity) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.pidFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeIdentity(sidecarPath(s.pidFile), pid, id, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// brokerIdentity is what a nats-server running this supervisor's conf reads as.
func brokerIdentity(s *Supervisor, start string) childproof.Identity {
	return childproof.Identity{
		Start: start, Exe: "/opt/bin/nats-server",
		Argv: []string{"/opt/bin/nats-server", "-c", s.mgr.ConfPath()}, Pgid: 0,
	}
}

// standInListener answers on the supervisor's listen address, as a broker
// would, so a test can drive the "listener answers" rows without one.
func standInListener(t *testing.T, s *Supervisor) {
	t.Helper()
	l, err := net.Listen("tcp", s.mgr.bus.Listen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
}

// quiet makes a supervisor over a stand-in listener usable: no provisioning
// and a structural reader that reports no reading, as the real one does when
// the broker cannot be asked.
func quiet(s *Supervisor) {
	s.AfterReady = nil
	s.checkStructureFn = func(context.Context) (Structure, error) {
		return Structure{}, errors.New("no broker behind the stand-in listener")
	}
}

func eventsOfKind(r *events.Ring, k events.Kind) []events.Event {
	return r.Snapshot(events.Filter{Kind: k}, 0)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A pid the kernel gave to another process after the broker died passes the old
// probe. Here the pidfile names a process the test planted with a different
// start time: nothing is signalled, both files go, one event says why, and a
// fresh broker starts (design section 7, item 2).
func TestStartReadsAReusedPidAsStaleAndSignalsNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		reason string
		setup  func(t *testing.T, s *Supervisor, pt *procTable) int
	}{
		"another start time": {"start", func(t *testing.T, s *Supervisor, pt *procTable) int {
			pid := reapedPid(t)
			recorded := brokerIdentity(s, "darwin:100.000000")
			pt.plant(pid, brokerIdentity(s, "darwin:200.000001"))
			writePidfile(t, s, pid)
			writeSidecar(t, s, pid, recorded)
			return pid
		}},
		"another executable": {"exe", func(t *testing.T, s *Supervisor, pt *procTable) int {
			pid := reapedPid(t)
			live := brokerIdentity(s, "darwin:100.000000")
			live.Exe = "/bin/zsh"
			pt.plant(pid, live)
			writePidfile(t, s, pid)
			writeSidecar(t, s, pid, brokerIdentity(s, "darwin:100.000000"))
			return pid
		}},
		"other arguments": {"argv", func(t *testing.T, s *Supervisor, pt *procTable) int {
			pid := reapedPid(t)
			live := brokerIdentity(s, "darwin:100.000000")
			live.Argv = []string{"/opt/bin/nats-server", "-c", "/elsewhere.conf"}
			pt.plant(pid, live)
			writePidfile(t, s, pid)
			writeSidecar(t, s, pid, brokerIdentity(s, "darwin:100.000000"))
			return pid
		}},
		"the process is gone": {"dead", func(t *testing.T, s *Supervisor, _ *procTable) int {
			pid := reapedPid(t)
			writePidfile(t, s, pid)
			writeSidecar(t, s, pid, brokerIdentity(s, "darwin:100.000000"))
			return pid
		}},
		"the record names another pid": {"sidecar-pid", func(t *testing.T, s *Supervisor, pt *procTable) int {
			pid := reapedPid(t)
			other := reapedPid(t)
			pt.plant(pid, brokerIdentity(s, "darwin:100.000000"))
			writePidfile(t, s, pid)
			writeSidecar(t, s, other, brokerIdentity(s, "darwin:100.000000"))
			return pid
		}},
		"the record is corrupt": {"sidecar", func(t *testing.T, s *Supervisor, pt *procTable) int {
			pid := reapedPid(t)
			pt.plant(pid, brokerIdentity(s, "darwin:100.000000"))
			writePidfile(t, s, pid)
			if err := os.WriteFile(sidecarPath(s.pidFile), []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			return pid
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s, _, ring := newTestSupervisor(t, "")
			pt := tableOf(s)
			victim := tc.setup(t, s, pt)
			if err := s.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if got := pt.signalsTo(victim); len(got) != 0 {
				t.Errorf("the pid the pidfile named was signalled: %v", got)
			}
			stale := eventsOfKind(ring, events.KindBusPidfileStale)
			if len(stale) != 1 {
				t.Fatalf("bus.pidfile-stale emitted %d times, want 1; kinds %v", len(stale), kindsOf(ring))
			}
			if want := "reason " + tc.reason; !strings.Contains(stale[0].Message, want) || !strings.Contains(stale[0].Message, strconv.Itoa(victim)) {
				t.Errorf("event %q lacks %q and the pid %d", stale[0].Message, want, victim)
			}
			st := s.Status()
			if !st.Ready || st.PID == victim || st.PID == 0 || st.Pidfile != "proven" {
				t.Errorf("after the fresh start: %+v, want ready under a new pid with pidfile proven", st)
			}
			rec, err := readIdentityFile(sidecarPath(s.pidFile))
			if err != nil || rec.PID != st.PID {
				t.Errorf("identity record after the start = %+v, %v; want the new broker's pid %d", rec, err, st.PID)
			}
			if data, _ := os.ReadFile(s.pidFile); strings.TrimSpace(string(data)) != strconv.Itoa(st.PID) {
				t.Errorf("pidfile = %q, want %d", data, st.PID)
			}
		})
	}
}

// The same case against the kernel and not a fake: a real process the test
// started holds the pid, the record carries another start time, and the
// process is still running afterwards (design section 7, item 3).
func TestStartLeavesARealUnrelatedProcessAlone(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	pt := tableOf(s)
	victim := startSleepChild(t)
	live, err := childproof.ReadIdentity(victim.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	recorded := live
	recorded.Start = live.Start + "9"
	recorded.Exe, recorded.Argv = "/opt/bin/nats-server", []string{"nats-server", "-c", s.mgr.ConfPath()}
	writePidfile(t, s, victim.Process.Pid)
	writeSidecar(t, s, victim.Process.Pid, recorded)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := pt.signalsTo(victim.Process.Pid); len(got) != 0 {
		t.Errorf("the unrelated process was signalled: %v", got)
	}
	if err := syscall.Kill(victim.Process.Pid, 0); err != nil {
		t.Errorf("the unrelated process is gone: %v", err)
	}
	if after, err := childproof.ReadIdentity(victim.Process.Pid); err != nil || after.Start != live.Start {
		t.Errorf("the unrelated process now reads %+v, %v; want it unchanged", after, err)
	}
	if n := len(eventsOfKind(ring, events.KindBusPidfileStale)); n != 1 {
		t.Errorf("bus.pidfile-stale emitted %d times, want 1", n)
	}
}

// Not proven and the port is held: nothing is signalled, the files go, and the
// bus stays down with one event naming the pid and the remedy (section 4.2).
func TestStartRefusesAHeldPortWhenThePidfileProvesNothing(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	quiet(s)
	pt := tableOf(s)
	pid := reapedPid(t)
	pt.plant(pid, brokerIdentity(s, "darwin:200.000001"))
	writePidfile(t, s, pid)
	writeSidecar(t, s, pid, brokerIdentity(s, "darwin:100.000000"))
	standInListener(t, s)
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unproven") {
		t.Fatalf("Start = %v, want a refusal naming an unproven child", err)
	}
	if got := pt.signals(); len(got) != 0 {
		t.Errorf("signals sent: %v", got)
	}
	if fileExists(s.pidFile) || fileExists(sidecarPath(s.pidFile)) {
		t.Error("the pidfile or its identity record was left behind")
	}
	if n := len(eventsOfKind(ring, events.KindBusPidfileStale)); n != 1 {
		t.Errorf("bus.pidfile-stale emitted %d times, want 1", n)
	}
	un := eventsOfKind(ring, events.KindBusPidfileUnproven)
	if len(un) != 1 || !strings.Contains(un[0].Message, strconv.Itoa(pid)) || !strings.Contains(un[0].Message, s.mgr.bus.Listen) {
		t.Errorf("bus.pidfile-unproven = %+v, want one naming pid %d and %s", un, pid, s.mgr.bus.Listen)
	}
	if st := s.Status(); st.Ready || st.Pidfile != "stale" {
		t.Errorf("status = %+v, want not ready and pidfile stale", st)
	}
}

// A pidfile with a proven broker that answers is adopted, and the adoption
// SIGHUP is the only signal. A change between the proof and a later signal
// stops that signal (design section 7, item 4).
func TestAdoptionSignalsOnceAndEverySignalReProves(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	quiet(s)
	pt := tableOf(s)
	pid := reapedPid(t)
	id := brokerIdentity(s, "darwin:100.000000")
	id.Pgid = pid
	pt.plant(pid, id)
	writePidfile(t, s, pid)
	writeSidecar(t, s, pid, id)
	standInListener(t, s)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st := s.Status(); !st.Adopted || st.PID != pid || st.Pidfile != "proven" {
		t.Errorf("status = %+v, want adopted pid %d with pidfile proven", st, pid)
	}
	if got := pt.signals(); !slices.Equal(got, []sentSignal{{pid, syscall.SIGHUP}}) {
		t.Fatalf("signals = %v, want exactly one SIGHUP to %d", got, pid)
	}
	if s.child.Pid() != pid || !s.prober.Same(s.child) {
		t.Errorf("the adopted child = pid %d, want the proven pid %d", s.child.Pid(), pid)
	}
	// The process stops being the one proven. Reload and the structural
	// SIGHUP each read it again first and send nothing.
	pt.failReadsOf(pid)
	if err := s.Reload(); err == nil {
		t.Error("Reload succeeded for a process that is no longer the broker")
	}
	s.checkStructureFn = func(context.Context) (Structure, error) { return Structure{Authorized: false}, nil }
	s.checkStructure(context.Background())
	if got := pt.signals(); len(got) != 1 {
		t.Errorf("signals after the identity failed = %v, want only the adoption SIGHUP", got)
	}
}

// A proven broker that does not answer is ours and wedged: TERM then KILL to
// its group, then a fresh start. It is not a one-second sleep and a hope.
func TestStartTerminatesAProvenWedgedBrokerThenSpawns(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	s.grace = 50 * time.Millisecond
	pt := tableOf(s)
	pid := reapedPid(t)
	id := brokerIdentity(s, "darwin:100.000000")
	id.Pgid = pid
	pt.plant(pid, id)
	writePidfile(t, s, pid)
	writeSidecar(t, s, pid, id)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := pt.signalsTo(pid); !slices.Equal(got, []sentSignal{{-pid, syscall.SIGTERM}, {-pid, syscall.SIGKILL}}) {
		t.Errorf("signals to the wedged broker = %v, want TERM then KILL to -%d", got, pid)
	}
	if n := len(eventsOfKind(ring, events.KindBusPidfileStale)); n != 0 {
		t.Errorf("bus.pidfile-stale emitted for a proven broker")
	}
	if st := s.Status(); !st.Ready || st.PID == pid || st.Adopted {
		t.Errorf("status = %+v, want a fresh broker", st)
	}
}

// terminate (design section 7, item 5): a proven broker that leads its group
// gets TERM then KILL to the group; one that does not gets them alone; and a
// change between TERM and KILL, or an exit, stops the KILL.
func TestTerminateProvesBeforeEverySignal(t *testing.T) {
	for name, tc := range map[string]struct {
		leader bool
		onTERM func(pt *procTable, pid int)
		want   func(pid int) []sentSignal
	}{
		"leader": {true, nil, func(pid int) []sentSignal {
			return []sentSignal{{-pid, syscall.SIGTERM}, {-pid, syscall.SIGKILL}}
		}},
		"not a leader": {false, nil, func(pid int) []sentSignal {
			return []sentSignal{{pid, syscall.SIGTERM}, {pid, syscall.SIGKILL}}
		}},
		"another process after TERM": {true, func(pt *procTable, pid int) {
			pt.change(pid, brokerIdentity(&Supervisor{mgr: &Manager{}}, "darwin:999.000000"))
		}, func(pid int) []sentSignal { return []sentSignal{{-pid, syscall.SIGTERM}} }},
		"exits after TERM": {true, func(pt *procTable, pid int) { pt.failReadsOf(pid) }, func(pid int) []sentSignal {
			return []sentSignal{{-pid, syscall.SIGTERM}}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			pt := newProcTable(t)
			p, err := childproof.NewForTest(pt.read, pt.kill)
			if err != nil {
				t.Fatal(err)
			}
			s := &Supervisor{prober: p, grace: 40 * time.Millisecond}
			pid := reapedPid(t)
			id := childproof.Identity{Start: "darwin:100.000000", Exe: "/opt/bin/nats-server", Argv: []string{"nats-server", "-c", "/c"}, Pgid: pid}
			if !tc.leader {
				id.Pgid = pid + 1
			}
			pt.plant(pid, id)
			c, err := p.Prove(pid, id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.onTERM != nil {
				pt.onKill = func(p int, sig syscall.Signal) {
					if sig == syscall.SIGTERM {
						tc.onTERM(pt, abs(p))
					}
				}
			}
			s.terminate(c)
			if got := pt.signals(); !slices.Equal(got, tc.want(pid)) {
				t.Errorf("signals = %v, want %v", got, tc.want(pid))
			}
		})
	}
}

// A pidfile with no identity record is the first start after the upgrade
// (V11). Whatever the ruling, listener-answers rows differ and the others do
// not; the table runs every mode so the one not ruled stays tested until it is
// deleted (design section 7, item 6).
func TestLegacyPidfile(t *testing.T) {
	type outcome struct {
		err          string // substring of Start's error; empty means Start succeeds
		adopted      bool
		sidecar      bool
		pidfileLeft  bool
		unprovenEvts int
		pidfileState string
	}
	matching := func(s *Supervisor) childproof.Identity { return brokerIdentity(s, "darwin:100.000000") }
	notBroker := func(s *Supervisor) childproof.Identity {
		id := brokerIdentity(s, "darwin:100.000000")
		id.Exe, id.Argv = "/bin/sleep", []string{"sleep", "-c", s.mgr.ConfPath()}
		return id
	}
	for name, tc := range map[string]struct {
		mode  legacyMode
		live  func(s *Supervisor) childproof.Identity
		want  outcome
		hupAt bool // exactly one SIGHUP to the pid alone
	}{
		"a: a nats-server running this conf is adopted": {
			legacyAdoptOnExecMatch, matching,
			outcome{adopted: true, sidecar: true, pidfileLeft: true, pidfileState: "proven"},
			true,
		},
		"a: another executable is not adopted": {
			legacyAdoptOnExecMatch, notBroker,
			outcome{err: "stranger", pidfileState: "stale"},
			false,
		},
		"b: nothing is adopted or signalled": {
			legacyRefuseUnproven, matching,
			outcome{err: "unproven", pidfileLeft: true, unprovenEvts: 1, pidfileState: "legacy"},
			false,
		},
		"unruled: nothing is adopted or signalled, and the error names the ruling": {
			legacyUnruled, matching,
			outcome{err: "V11", pidfileLeft: true, unprovenEvts: 1, pidfileState: "legacy"},
			false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _, ring := newTestSupervisor(t, "")
			quiet(s)
			s.legacy = tc.mode
			pt := tableOf(s)
			pid := reapedPid(t)
			pt.plant(pid, tc.live(s))
			writePidfile(t, s, pid)
			standInListener(t, s)
			err := s.Start(context.Background())
			if tc.want.err == "" && err != nil {
				t.Fatalf("Start: %v", err)
			}
			if tc.want.err != "" && (err == nil || !strings.Contains(err.Error(), tc.want.err)) {
				t.Fatalf("Start = %v, want an error containing %q", err, tc.want.err)
			}
			wantSigs := []sentSignal(nil)
			if tc.hupAt {
				wantSigs = []sentSignal{{pid, syscall.SIGHUP}}
			}
			if got := pt.signals(); !slices.Equal(got, wantSigs) {
				t.Errorf("signals = %v, want %v", got, wantSigs)
			}
			st := s.Status()
			if st.Adopted != tc.want.adopted || st.Pidfile != tc.want.pidfileState {
				t.Errorf("status = %+v, want adopted=%v pidfile=%s", st, tc.want.adopted, tc.want.pidfileState)
			}
			if got := fileExists(sidecarPath(s.pidFile)); got != tc.want.sidecar {
				t.Errorf("identity record present = %v, want %v", got, tc.want.sidecar)
			}
			if got := fileExists(s.pidFile); got != tc.want.pidfileLeft {
				t.Errorf("pidfile present = %v, want %v", got, tc.want.pidfileLeft)
			}
			if n := len(eventsOfKind(ring, events.KindBusPidfileUnproven)); n != tc.want.unprovenEvts {
				t.Errorf("bus.pidfile-unproven emitted %d times, want %d", n, tc.want.unprovenEvts)
			}
			if tc.want.sidecar {
				rec, err := readIdentityFile(sidecarPath(s.pidFile))
				if err != nil || rec.PID != pid || rec.Start != "darwin:100.000000" {
					t.Errorf("the record written at adoption = %+v, %v", rec, err)
				}
			}
		})
	}
}

// With the listener silent a legacy pidfile is just a stale file under every
// mode: the pidfile goes, nothing is signalled, a fresh broker starts.
func TestLegacyPidfileWithNoListenerStartsFresh(t *testing.T) {
	for name, mode := range map[string]legacyMode{
		"a": legacyAdoptOnExecMatch, "b": legacyRefuseUnproven, "unruled": legacyUnruled,
	} {
		t.Run(name, func(t *testing.T) {
			s, _, _ := newTestSupervisor(t, "")
			s.legacy = mode
			pt := tableOf(s)
			pid := reapedPid(t)
			pt.plant(pid, brokerIdentity(s, "darwin:100.000000"))
			writePidfile(t, s, pid)
			if err := s.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if got := pt.signalsTo(pid); len(got) != 0 {
				t.Errorf("the legacy pid was signalled: %v", got)
			}
			if st := s.Status(); !st.Ready || st.PID == pid || st.Pidfile != "proven" {
				t.Errorf("status = %+v, want a fresh proven broker", st)
			}
		})
	}
}

// The ruling is not made, so a build that reaches ready must not still carry
// the placeholder. Skipped unless asked, because the draft carries it on
// purpose: MARVEL_REQUIRE_V11=1 go test ./internal/bus -run TestLegacyPidfileIsRuled.
func TestLegacyPidfileIsRuled(t *testing.T) {
	if os.Getenv("MARVEL_REQUIRE_V11") == "" {
		t.Skip("V11 is not ruled; set MARVEL_REQUIRE_V11=1 to require the constant")
	}
	if legacyPidfile == legacyUnruled {
		t.Fatal("legacyPidfile is still legacyUnruled: set it to the ruled value (services-shape-requirements.md section 6, V11)")
	}
}

// Stop(false) removes both files; Stop(true) keeps both for the successor.
func TestStopRemovesBothFilesAndKeepLeavesBoth(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fileExists(s.pidFile) || !fileExists(sidecarPath(s.pidFile)) {
		t.Fatal("a started broker left no pidfile and identity record")
	}
	pid := s.Status().PID
	s.Stop(true)
	if !fileExists(s.pidFile) || !fileExists(sidecarPath(s.pidFile)) {
		t.Error("Stop(true) removed a file the successor adopts from")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	s2, err := NewSupervisor(s.mgr, filepath.Dir(s.pidFile), filepath.Dir(s.logPath), events.NewRing(8))
	if err != nil {
		t.Fatal(err)
	}
	s2.dialTimeout = loadedBrokerWait
	attach(t, s2, tableOf(s))
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("the successor: %v", err)
	}
	s2.Stop(false)
	if fileExists(s2.pidFile) || fileExists(sidecarPath(s2.pidFile)) {
		t.Error("Stop(false) left the pidfile or its identity record")
	}
}

// A pidfile that names no process is no pidfile: nothing is read and nothing
// is signalled for it, whatever the text holds (carried from the probe-seam
// tests this file replaces).
func TestClassifyNeverReadsAPidItCannotName(t *testing.T) {
	for name, content := range map[string]string{
		"wider than 32 bits": "4294967297\n",
		"wraps to -1":        "4294967295\n",
		"wraps to -5":        "4294967291\n",
		"trailing text":      "12abc\n",
		"negative":           "-5\n",
		"minus one":          "-1\n",
		"zero":               "0\n",
		"two numbers":        "4242 99\n",
		"empty":              "",
		"past int32":         "2147483648\n",
	} {
		t.Run(name, func(t *testing.T) {
			pt := newProcTable(t)
			p, err := childproof.NewForTest(pt.read, pt.kill)
			if err != nil {
				t.Fatal(err)
			}
			s := &Supervisor{pidFile: filepath.Join(t.TempDir(), "nats-server.pid"), prober: p}
			if err := os.WriteFile(s.pidFile, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if v := s.classifyPidfile(); v.kind != "none" || v.pid != 0 {
				t.Errorf("a pidfile that names no process classified as %+v", v)
			}
			if got := pt.readPids(); len(got) != 0 {
				t.Errorf("read the identity of pid(s) %v from a pidfile that names none", got)
			}
		})
	}
}

func TestClassifyReadsTheRecordBeforeAnyProcess(t *testing.T) {
	pt := newProcTable(t)
	p, err := childproof.NewForTest(pt.read, pt.kill)
	if err != nil {
		t.Fatal(err)
	}
	s := &Supervisor{pidFile: filepath.Join(t.TempDir(), "nats-server.pid"), prober: p}
	pid := reapedPid(t)
	writePidfile(t, s, pid)
	if v := s.classifyPidfile(); v.kind != "legacy" || v.pid != pid {
		t.Errorf("a pidfile with no record classified as %+v, want legacy", v)
	}
	if got := pt.readPids(); len(got) != 0 {
		t.Errorf("a legacy pidfile read process identity %v before any proof", got)
	}
	id := childproof.Identity{Start: "darwin:1.000000", Exe: "/x/nats-server", Argv: []string{"nats-server", "-c", "/c"}}
	pt.plant(pid, id)
	if err := writeIdentity(sidecarPath(s.pidFile), pid, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if v := s.classifyPidfile(); v.kind != "proven" || v.child.Pid() != pid {
		t.Errorf("a matching record classified as %+v, want proven", v)
	}
	// The same record against a process that started later: stale, and why.
	pt.change(pid, childproof.Identity{Start: "darwin:2.000000", Exe: id.Exe, Argv: id.Argv})
	if v := s.classifyPidfile(); v.kind != "stale" || v.reason != "start" {
		t.Errorf("a changed start time classified as %+v, want stale for start", v)
	}
	if got := fmt.Sprint(pt.signals()); got != "[]" {
		t.Errorf("classifying signalled %s", got)
	}
}
