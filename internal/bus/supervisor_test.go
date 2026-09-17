package bus

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
)

func TestComputeBackoffMatchesRoleSchedule(t *testing.T) {
	t.Parallel()
	want := []time.Duration{30 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for n, w := range want {
		if got := computeBackoff(n); got != w {
			t.Errorf("computeBackoff(%d) = %s, want %s", n, got, w)
		}
	}
	if got := computeBackoff(60); got != restartBackoffMax {
		t.Errorf("computeBackoff(60) = %s, want cap %s (shift overflow)", got, restartBackoffMax)
	}
}

// freePort asks the kernel for a free loopback port and releases it; the
// broker binds it a moment later. Racy in theory, fine for a local test.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func kindsOf(r *events.Ring) []events.Kind {
	snap := r.Snapshot(events.Filter{}, 0)
	out := make([]events.Kind, 0, len(snap))
	for _, e := range snap {
		out = append(out, e.Kind)
	}
	return out
}

func hasKind(r *events.Ring, k events.Kind) bool {
	for _, got := range kindsOf(r) {
		if got == k {
			return true
		}
	}
	return false
}

// newTestSupervisor renders a managed bus for a fresh temp layout on a free
// port and returns a supervisor with a short backoff and dial timeout.
// Skips without nats-server: the contract under test is with the binary.
func newTestSupervisor(t *testing.T, hub string) (*Supervisor, *Manager, *events.Ring) {
	t.Helper()
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir, HubURL: hub}
	live := &teams{{Name: "ops", Workspace: "aae-orc"}}
	m, err := NewManager(dir, "t"+strconv.Itoa(freePort(t)), rb, live, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	ring := events.NewRing(64)
	s, err := NewSupervisor(m, filepath.Join(root, "run"), filepath.Join(root, "log"), ring)
	if err != nil {
		t.Fatal(err)
	}
	s.backoff = func(int) time.Duration { return 200 * time.Millisecond }
	s.dialTimeout = 5 * time.Second
	s.leafPoll = 200 * time.Millisecond
	// What the daemon wires: provisioning as the admin identity. Since the
	// structural-health contract (aae-orc-vy6k7) a bare broker is not
	// ready, so a supervisor under test provisions like the real one; a
	// test that wants a different AfterReady overrides it before Start.
	s.AfterReady = func() error {
		admin := m.Admin()
		_, err := Provision(context.Background(), m.URL(), admin.Name, admin.Password)
		return err
	}
	m.Reloader = s
	t.Cleanup(func() { s.Stop(false) })
	return s, m, ring
}

func TestSupervisorStartsReloadsAndStopsWithDaemon(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Ready() {
		t.Fatal("not ready after Start")
	}
	st := s.Status()
	if st.PID == 0 || st.Adopted || st.Leaf != "n/a" || !st.Managed {
		t.Errorf("status = %+v", st)
	}
	pidData, err := os.ReadFile(s.pidFile)
	if err != nil || strings.TrimSpace(string(pidData)) != strconv.Itoa(st.PID) {
		t.Errorf("pidfile = %q err=%v, want %d", pidData, err, st.PID)
	}
	if !hasKind(ring, events.KindBusStarted) {
		t.Errorf("no bus.started; kinds %v", kindsOf(ring))
	}

	// A team change regenerates and SIGHUPs the live broker.
	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "aae-orc"})
	if changed, err := m.Regenerate(); err != nil || !changed {
		t.Fatalf("regenerate: changed=%v err=%v", changed, err)
	}
	if !hasKind(ring, events.KindBusReloaded) {
		t.Errorf("no bus.reloaded after a team change; kinds %v", kindsOf(ring))
	}
	if syscall.Kill(st.PID, 0) != nil {
		t.Fatal("broker died on SIGHUP")
	}

	// Stop without keep ends the broker and removes the pidfile.
	s.Stop(false)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && syscall.Kill(st.PID, 0) == nil {
		time.Sleep(50 * time.Millisecond)
	}
	if syscall.Kill(st.PID, 0) == nil {
		t.Error("broker still alive after Stop(false)")
	}
	if _, err := os.Stat(s.pidFile); !os.IsNotExist(err) {
		t.Error("pidfile left behind after Stop(false)")
	}
	if !hasKind(ring, events.KindBusStopped) {
		t.Errorf("no bus.stopped; kinds %v", kindsOf(ring))
	}
}

func TestSupervisorKeepThenAdoptAcrossDaemons(t *testing.T) {
	s1, m, _ := newTestSupervisor(t, "")
	if err := s1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s1.Status().PID
	s1.Stop(true) // reexec / --keep-bus posture
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("Stop(true) killed the broker it was told to keep")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// The successor finds the pidfile and the listener and adopts.
	ring := events.NewRing(16)
	s2, err := NewSupervisor(m, filepath.Dir(s1.pidFile), filepath.Dir(s1.logPath), ring)
	if err != nil {
		t.Fatal(err)
	}
	s2.dialTimeout = 5 * time.Second
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := s2.Status()
	if !st.Adopted || st.PID != pid || !st.Ready {
		t.Errorf("successor status = %+v, want adopted pid %d", st, pid)
	}
	s2.Stop(false)
	if syscall.Kill(pid, 0) == nil {
		time.Sleep(time.Second)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Error("adopted broker survived Stop(false)")
	}
}

func TestSupervisorRefusesStrangerOnThePort(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	// Something that is not ours answers on the listen address.
	l, err := net.Listen("tcp", s.mgr.bus.Listen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	err = s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "stranger") {
		t.Fatalf("Start with a stranger on the port: err=%v, want refusal", err)
	}
	if s.Ready() {
		t.Error("ready after refusing")
	}
}

func TestSupervisorRefusesLeafConfWithoutSeedSource(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "nats-leaf://127.0.0.1:1")
	m.hasLeafSeed = func() bool { return true }
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), LeafSeedEnv) {
		t.Fatalf("Start with a leaf block and no Env: err=%v, want refusal naming %s", err, LeafSeedEnv)
	}
}

func TestSupervisorRestartsCrashedBrokerUnderBackoff(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s.Status().PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Status(); st.Ready && st.PID != pid {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := s.Status()
	if !st.Ready || st.PID == pid || st.Restarts != 1 {
		t.Fatalf("after crash: %+v (old pid %d), want ready under a new pid with restarts=1", st, pid)
	}
	if !hasKind(ring, events.KindBusCrashed) {
		t.Errorf("no bus.crashed; kinds %v", kindsOf(ring))
	}
	if syscall.Kill(st.PID, 0) != nil {
		t.Error("restarted broker not alive")
	}
}

func TestSupervisorReportsUnenrolledHubOnce(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "nats-leaf://127.0.0.1:1")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, k := range kindsOf(ring) {
		if k == events.KindBusLeafUnenrolled {
			n++
		}
	}
	if n != 1 {
		t.Errorf("bus.leaf.unenrolled emitted %d times, want 1; kinds %v", n, kindsOf(ring))
	}
	if st := s.Status(); st.Leaf != "unenrolled" {
		t.Errorf("leaf = %q, want unenrolled", st.Leaf)
	}
	// A hub with no seed never gets bus.leaf.down: no link was ever promised.
	time.Sleep(500 * time.Millisecond)
	if hasKind(ring, events.KindBusLeafDown) {
		t.Error("bus.leaf.down emitted for an unenrolled hub")
	}
}

func TestNewSupervisorNamesMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:1", StoreDir: t.TempDir()}
	m, err := NewManager(t.TempDir(), "kinu", rb, &teams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewSupervisor(m, t.TempDir(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "nats-server not found") || !strings.Contains(err.Error(), "bus.managed: false") {
		t.Fatalf("err = %v, want a missing-binary error naming the way out", err)
	}
}

// TestRestartCarriesTheSeedIntoTheChildEnvironment: a broker started with
// no seed runs local-only; the seed arrives (credential put), the conf gains
// the leaf remote, and Restart starts a new process with the seed in its
// environment. The seed is in that environment and nowhere on disk.
func TestRestartCarriesTheSeedIntoTheChildEnvironment(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "nats-leaf://127.0.0.1:1")
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	have := false
	m.hasLeafSeed = func() bool { return have }
	s.Env = func() []string {
		if !have {
			return nil
		}
		return []string{LeafSeedEnv + "=" + string(seed)}
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := s.Status().PID
	if !hasKind(ring, events.KindBusLeafUnenrolled) {
		t.Fatal("no bus.leaf.unenrolled before the seed")
	}

	have = true
	if changed, err := m.Regenerate(); err != nil || !changed {
		t.Fatalf("regenerate with seed: changed=%v err=%v", changed, err)
	}
	if err := s.Restart("credential.put bus/leaf"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	st := s.Status()
	if !st.Ready || st.PID == first || st.Restarts != 0 {
		t.Fatalf("after restart: %+v (first pid %d); want ready, new pid, restarts uncounted", st, first)
	}
	if st.Leaf == "unenrolled" {
		t.Error("leaf still reads unenrolled after the seed arrived")
	}
	// The new process has the variable; the old one did not. Read the
	// child's environment from the kernel rather than trusting our own env.
	out, err := exec.Command("ps", "-E", "-o", "command=", "-p", strconv.Itoa(st.PID)).Output()
	if err == nil && !strings.Contains(string(out), LeafSeedEnv+"=") {
		t.Errorf("child environment lacks %s:\n%s", LeafSeedEnv, out)
	}
	// Nothing on disk carries the seed.
	for _, f := range []string{s.mgr.ConfPath(), filepath.Join(s.mgr.Dir(), AuthName), s.logPath} {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), string(seed)) {
			t.Errorf("seed found in %s", f)
		}
	}
	if syscall.Kill(first, 0) == nil {
		t.Error("previous broker still alive after restart")
	}
}

// TestRestartWhileDownIsAStart: a restart asked for during a crash outage
// does not wait out the backoff; it starts the broker now.
func TestRestartWhileDownIsAStart(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	s.backoff = func(int) time.Duration { return time.Hour }
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s.Status().PID
	_ = syscall.Kill(pid, syscall.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && s.Ready() {
		time.Sleep(50 * time.Millisecond)
	}
	if s.Ready() {
		t.Fatal("still ready after SIGKILL")
	}
	if err := s.Restart("test"); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); !st.Ready || st.PID == pid {
		t.Fatalf("after restart during outage: %+v", st)
	}
}
