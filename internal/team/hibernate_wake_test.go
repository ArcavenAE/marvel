package team

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// These tests start real sleep panes on the package's scratch tmux server, as
// the other reconcile tests do, to see what a wake spawns and in what order.

func wakeRoles() []api.Role {
	rt := api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}
	return []api.Role{
		{Name: "supervisor", Replicas: 1, Runtime: rt, HealthCheck: &api.HealthCheck{Timeout: 30 * time.Second}},
		{Name: "worker", Replicas: 1, Runtime: rt},
	}
}

type wakeFixture struct {
	t     *testing.T
	store *api.Store
	ctrl  *Controller
	clock *testClock
	ring  *events.Ring
	ws    string
}

func newWakeFixture(t *testing.T, ws string) *wakeFixture {
	t.Helper()
	store, mgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	_ = mgr
	f := &wakeFixture{t: t, store: store, ctrl: ctrl, clock: newTestClock(listEpoch), ring: events.NewRing(0), ws: ws}
	ctrl.now = f.clock.Now
	ctrl.Events = f.ring
	createTeamFixture(t, store, ws, "squad", wakeRoles())
	// A team that was hibernated with no seats left: marked, held at the start
	// line, as it is after a reboot.
	if err := store.UpdateTeam(ws+"/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureHold
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: f.clock.Now(), Actor: "op", Source: "cli", Mode: api.HibernateModeHandoff}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *wakeFixture) alive(role string) int {
	return api.CountAlive(f.store.ListSessionsByTeamRole(f.ws, "squad", role))
}

func (f *wakeFixture) wake() {
	f.t.Helper()
	if err := f.ctrl.Wake(f.ws+"/squad", WakeOptions{Actor: "op", Source: "cli"}); err != nil {
		f.t.Fatalf("Wake: %v", err)
	}
}

func (f *wakeFixture) heartbeat(role string) {
	f.t.Helper()
	for _, s := range f.store.ListSessionsByTeamRole(f.ws, "squad", role) {
		if err := f.store.UpdateSession(s.Key(), func(live *api.Session) error {
			live.LastHeartbeat = f.clock.Now().Add(time.Second)
			return nil
		}); err != nil {
			f.t.Fatal(err)
		}
	}
}

// A hibernated team spawns nothing, whatever the reconcile does; a wake lets the
// supervisor roles start, and the workers once a supervisor has heartbeated.
func TestWakeStartsSupervisorsBeforeWorkers(t *testing.T) {
	f := newWakeFixture(t, "test-wake-order")
	f.ctrl.ReconcileOnce()
	if f.alive("supervisor")+f.alive("worker") != 0 {
		t.Fatal("a hibernated team spawned a seat")
	}

	f.wake()
	f.ctrl.ReconcileOnce()
	if f.alive("supervisor") != 1 || f.alive("worker") != 0 {
		t.Fatalf("after wake: supervisor %d worker %d, want the supervisor only", f.alive("supervisor"), f.alive("worker"))
	}
	f.ctrl.ReconcileOnce()
	if f.alive("worker") != 0 {
		t.Fatal("the worker started before any supervisor heartbeat")
	}

	f.heartbeat("supervisor")
	f.ctrl.ReconcileOnce()
	if f.alive("worker") != 1 {
		t.Fatalf("worker seats = %d after the supervisor heartbeated, want 1", f.alive("worker"))
	}
	f.ctrl.ReconcileOnce()
	tm, _ := f.store.GetTeam(f.ws + "/squad")
	if tm.Hibernate != nil {
		t.Fatalf("the mark = %+v after every role started, want it cleared", tm.Hibernate)
	}
	if got := len(f.ring.Snapshot(events.Filter{Kind: events.KindTeamWoken}, 0)); got != 1 {
		t.Fatalf("team.woken events = %d, want 1", got)
	}
}

// A supervisor that never heartbeats does not hold the workers forever: past
// its health timeout they start, and the wake reports it as not up.
func TestWakeStartsWorkersAfterTheSupervisorHealthTimeout(t *testing.T) {
	f := newWakeFixture(t, "test-wake-timeout")
	f.wake()
	f.ctrl.ReconcileOnce()
	f.clock.Advance(31 * time.Second)
	f.ctrl.ReconcileOnce()
	if f.alive("worker") != 1 {
		t.Fatalf("worker seats = %d past the supervisor's health timeout, want 1", f.alive("worker"))
	}
	f.ctrl.ReconcileOnce()
	woken := f.ring.Snapshot(events.Filter{Kind: events.KindTeamWoken}, 0)
	if len(woken) != 1 || woken[0].Severity != events.SeverityWarning {
		t.Fatalf("team.woken = %+v, want one warning naming the supervisor as not up", woken)
	}
}

// L2: a daemon that restarts before any seat of the waking team is live sets the
// team back to hold while the mark still reads waking. Running wake again sets
// converge again, and every role ends with exactly one live seat.
func TestWakeIsSafeToRunAgainAfterARestartResetsTheHold(t *testing.T) {
	f := newWakeFixture(t, "test-wake-rerun")
	f.wake()
	if err := f.ctrl.InitConvergencePosture(); err != nil { // the restart
		t.Fatal(err)
	}
	tm, _ := f.store.GetTeam(f.ws + "/squad")
	if tm.Posture() != api.PostureHold || tm.Hibernate == nil || tm.Hibernate.State != api.HibernateWaking {
		t.Fatalf("after the restart: posture %s, mark %+v; want hold and waking", tm.Posture(), tm.Hibernate)
	}
	f.ctrl.ReconcileOnce()
	if f.alive("supervisor") != 0 {
		t.Fatal("the held team started a seat before wake ran again")
	}

	f.wake()
	f.ctrl.ReconcileOnce()
	f.heartbeat("supervisor")
	f.ctrl.ReconcileOnce()
	f.wake() // once more, with seats up: it must not start any twice
	f.ctrl.ReconcileOnce()
	f.ctrl.ReconcileOnce()

	if f.alive("supervisor") != 1 || f.alive("worker") != 1 {
		t.Fatalf("supervisor seats %d, worker seats %d; want exactly one each", f.alive("supervisor"), f.alive("worker"))
	}
}

func TestWakeRefusesATeamThatIsNotHibernated(t *testing.T) {
	f := newWakeFixture(t, "test-wake-none")
	if err := f.store.UpdateTeam(f.ws+"/squad", func(live *api.Team) error { live.Hibernate = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.ctrl.Wake(f.ws+"/squad", WakeOptions{}); err == nil {
		t.Fatal("wake accepted a team with no mark")
	}
}

// The hold stops only a cold start; the mark stops every spawn it covers, even
// when the team still has a live seat that would let a top-up through.
func TestTheMarkStopsATopUpThatTheHoldWouldAllow(t *testing.T) {
	f := newWakeFixture(t, "test-mark-topup")
	f.wake()
	f.ctrl.ReconcileOnce() // a supervisor is live now
	if err := f.store.UpdateTeam(f.ws+"/squad", func(live *api.Team) error {
		live.Hibernate.State = api.HibernateHibernated
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tm, _ := f.store.GetTeam(f.ws + "/squad")
	f.ctrl.mu.Lock()
	withheldByHold := f.ctrl.postureWithholds(&tm)
	plan := f.ctrl.planRole(&tm, roleOf(&tm, "worker"), tm.Generation)
	f.ctrl.mu.Unlock()
	if withheldByHold {
		t.Fatal("the hold withheld a spawn on a team with a live seat; the test needs it not to")
	}
	if plan.Action != RoleHold || plan.Hold != HoldHibernated {
		t.Fatalf("plan = %+v, want the mark to hold the worker", plan)
	}
}

// A woken seat is a successor of the seat it replaces: MARVEL_PREDECESSOR names
// the hibernated session the resume journal recorded, so its first prompt points
// at the handoff that seat wrote.
func TestWakeStartsEachSeatAsASuccessorOfItsHibernatedSeat(t *testing.T) {
	store, ctrl, cleanup := setupWithBolt(t, filepath.Join(t.TempDir(), "marvel.db"))
	t.Cleanup(cleanup)
	f := &wakeFixture{t: t, store: store, ctrl: ctrl, clock: newTestClock(listEpoch), ring: events.NewRing(0), ws: "test-wake-pred"}
	ctrl.now = f.clock.Now
	ctrl.Events = f.ring
	createTeamFixture(t, store, f.ws, "squad", wakeRoles())
	if err := store.UpdateTeam(f.ws+"/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureHold
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: f.clock.Now(), Mode: api.HibernateModeHandoff}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	old := "squad-supervisor-before"
	if err := store.RecordResume(api.ResumeEntry{Session: f.ws + "/" + old, Workspace: f.ws, Team: "squad", Role: "supervisor", LaunchedAt: listEpoch.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	f.wake()
	f.ctrl.ReconcileOnce()

	var got []api.Session
	for _, s := range store.ListSessionsByTeamRole(f.ws, "squad", "supervisor") {
		got = append(got, s)
	}
	if len(got) != 1 || got[0].Predecessor != f.ws+"/"+old {
		t.Fatalf("supervisor seats = %+v, want one whose predecessor is %s/%s", got, f.ws, old)
	}
}
