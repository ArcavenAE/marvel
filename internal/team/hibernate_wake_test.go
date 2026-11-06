package team

import (
	"path/filepath"
	"strings"
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
// the hibernated session the mark recorded, so its first prompt points at the
// handoff that seat wrote. The role has two journal entries, and the newer one
// belongs to a seat the hibernate never ended; the lookup is by the hibernated
// seat's own key, so the older entry is the one picked.
func TestWakeStartsEachSeatAsASuccessorOfItsHibernatedSeat(t *testing.T) {
	store, ctrl, cleanup := setupWithBolt(t, filepath.Join(t.TempDir(), "marvel.db"))
	t.Cleanup(cleanup)
	f := &wakeFixture{t: t, store: store, ctrl: ctrl, clock: newTestClock(listEpoch), ring: events.NewRing(0), ws: "test-wake-pred"}
	ctrl.now = f.clock.Now
	ctrl.Events = f.ring
	createTeamFixture(t, store, f.ws, "squad", wakeRoles())
	hibernated, newer := f.ws+"/squad-supervisor-before", f.ws+"/squad-supervisor-later"
	if err := store.UpdateTeam(f.ws+"/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureHold
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: f.clock.Now(), Mode: api.HibernateModeHandoff, Seats: []string{hibernated}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []api.ResumeEntry{
		{Session: hibernated, Workspace: f.ws, Team: "squad", Role: "supervisor", LaunchedAt: listEpoch.Add(-2 * time.Hour)},
		{Session: newer, Workspace: f.ws, Team: "squad", Role: "supervisor", LaunchedAt: listEpoch.Add(-time.Hour)},
	} {
		if err := store.RecordResume(e); err != nil {
			t.Fatal(err)
		}
	}

	f.wake()
	f.ctrl.ReconcileOnce()

	got := store.ListSessionsByTeamRole(f.ws, "squad", "supervisor")
	if len(got) != 1 || got[0].Predecessor != hibernated {
		t.Fatalf("supervisor seats = %+v, want one whose predecessor is %s, not %s", got, hibernated, newer)
	}
}

// A seat the hibernate ended is recorded in the mark by its key, and an --end
// records it too, so the wake has the key whichever way the seat ended.
func TestHibernateRecordsEachEndedSeatInTheMark(t *testing.T) {
	f := newWakeFixture(t, "test-wake-seats")
	f.ctrl.mu.Lock()
	tm, _ := f.store.GetTeam(f.ws + "/squad")
	f.ctrl.recordHibernatedSeat(&tm, f.ws+"/squad-worker-a")
	f.ctrl.recordHibernatedSeat(&tm, f.ws+"/squad-worker-a")
	f.ctrl.mu.Unlock()
	live, _ := f.store.GetTeam(f.ws + "/squad")
	if got := live.Hibernate.Seats; len(got) != 1 || got[0] != f.ws+"/squad-worker-a" {
		t.Fatalf("mark seats = %v, want the one key once", got)
	}
}

// The waking message keeps the time the team was hibernated, because the mark's
// Since is reset to the wake time.
func TestWakingMessageRecordsWhenTheTeamWasHibernated(t *testing.T) {
	f := newWakeFixture(t, "test-wake-since")
	hibernatedAt := f.clock.Now()
	f.clock.Advance(3 * time.Hour)
	f.wake()
	var msg string
	for _, ev := range f.ring.Snapshot(events.Filter{}, 0) {
		if ev.Kind == events.KindTeamWaking {
			msg = ev.Message
		}
	}
	want := "hibernated since " + hibernatedAt.Format(time.RFC3339)
	if !strings.Contains(msg, want) {
		t.Fatalf("team.waking message = %q, want it to contain %q", msg, want)
	}
}

// A scheduled role's firing does not run while the team is hibernated: the
// clock stands still for a covered role, so nothing is recorded and nothing
// spawns.
func TestHibernatedMarkHoldsANewFiringOfACoveredRole(t *testing.T) {
	r := newClockRig(t, "test-hib-firing", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("Etc/UTC", shRuntime("exit 0"), nil))
	r.ctrl.ReconcileOnce()
	hibernateRig(t, r)
	r.clock.Advance(20 * time.Minute)
	r.ctrl.ReconcileOnce()
	r.ctrl.ReconcileOnce()
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("a hibernated team started %d runs of a scheduled role, want none", n)
	}
	if st := r.status(); st.Firing != "" {
		t.Fatalf("schedule status records firing %q on a hibernated team, want none", st.Firing)
	}
}

func hibernateRig(t *testing.T, r *clockRig) {
	t.Helper()
	if err := r.store.UpdateTeam(r.ws+"/timers", func(tm *api.Team) error {
		tm.ConvergencePosture = api.PostureHold
		tm.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: r.clock.Now(), Mode: api.HibernateModeHandoff}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A firing due while the team was hibernated gets one catch-up at wake when it
// is still inside starting_deadline.
func TestWakeCatchesUpAFiringInsideTheStartingDeadlineOnce(t *testing.T) {
	r := newClockRig(t, "test-hib-catchup", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("Etc/UTC", shRuntime("exit 0"), func(p *api.SchedulePolicy) { p.StartingDeadline = time.Hour }))
	r.ctrl.ReconcileOnce()
	hibernateRig(t, r)
	r.clock.Advance(40 * time.Minute) // due 06:17, now 06:40
	r.ctrl.ReconcileOnce()
	if err := r.ctrl.Wake(r.ws+"/timers", WakeOptions{Actor: "op", Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	r.settle(t, "one catch-up run", func() bool { return len(r.sessions()) == 1 })
	if m := r.kind(events.KindScheduleMissed); len(m) != 0 {
		t.Fatalf("schedule.missed = %+v, want none inside starting_deadline", m)
	}
}

// A firing later than starting_deadline at wake is reported schedule.missed and
// runs nothing.
func TestWakeReportsAFiringPastTheStartingDeadlineAsMissed(t *testing.T) {
	r := newClockRig(t, "test-hib-missed", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("Etc/UTC", shRuntime("exit 0"), func(p *api.SchedulePolicy) { p.StartingDeadline = time.Hour }))
	r.ctrl.ReconcileOnce()
	hibernateRig(t, r)
	r.clock.Advance(60 * time.Hour)
	r.ctrl.ReconcileOnce()
	if m := r.kind(events.KindScheduleMissed); len(m) != 0 {
		t.Fatalf("schedule.missed %+v while hibernated, want it only at wake", m)
	}
	if err := r.ctrl.Wake(r.ws+"/timers", WakeOptions{Actor: "op", Source: "cli"}); err != nil {
		t.Fatal(err)
	}
	r.ctrl.ReconcileOnce()
	if m := r.kind(events.KindScheduleMissed); len(m) != 1 {
		t.Fatalf("schedule.missed = %+v, want one at wake", m)
	}
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("%d runs started for a firing past starting_deadline, want none", n)
	}
}

// A wake that starts a role's seats over more than one tick names each ended
// seat once: the second tick must not hand out the predecessor a live seat
// already names.
func TestWakeNamesEachPredecessorOnceAcrossTicks(t *testing.T) {
	store, ctrl, cleanup := setupWithBolt(t, filepath.Join(t.TempDir(), "marvel.db"))
	t.Cleanup(cleanup)
	ws := "test-wake-twice"
	ctrl.now = newTestClock(listEpoch).Now
	rt := api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}
	createTeamFixture(t, store, ws, "squad", []api.Role{{Name: "worker", Replicas: 2, Runtime: rt}})
	a, b := ws+"/squad-worker-g1-0", ws+"/squad-worker-g1-1"
	if err := store.UpdateTeam(ws+"/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureConverge
		live.Hibernate = &api.HibernateMark{State: api.HibernateWaking, Since: listEpoch, Mode: api.HibernateModeHandoff, Seats: []string{a, b}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i, k := range []string{a, b} {
		if err := store.RecordResume(api.ResumeEntry{Session: k, Workspace: ws, Team: "squad", Role: "worker", LaunchedAt: listEpoch.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	tm, _ := store.GetTeam(ws + "/squad")
	ctrl.mu.Lock()
	first := ctrl.wakePredecessors(&tm, "worker")
	ctrl.mu.Unlock()
	if len(first) != 2 {
		t.Fatalf("predecessors = %v, want both ended seats", first)
	}
	// One seat starts from the first of them; a later tick must offer the other.
	if err := store.CreateSession(&api.Session{Name: "squad-worker-g1-2", Workspace: ws, Team: "squad", Role: "worker", Generation: 1, State: api.SessionRunning, Predecessor: first[0]}); err != nil {
		t.Fatal(err)
	}
	ctrl.mu.Lock()
	second := ctrl.wakePredecessors(&tm, "worker")
	ctrl.mu.Unlock()
	if len(second) != 1 || second[0] != first[1] {
		t.Fatalf("predecessors on the next tick = %v, want only %s", second, first[1])
	}
}
