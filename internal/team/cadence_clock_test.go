package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Tests for the slot clock as the controller runs it (docs/design/declared-cadence.md
// V2, ticket T3). The clock opens slot records and nothing else.

var cadenceEpoch = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func cadenceDue(day int) time.Time { return time.Date(2026, 10, day, 6, 17, 0, 0, time.UTC) }

// cadenceRole is an interactive role with one daily 06:17 UTC cadence.
func cadenceRole() api.Role {
	return api.Role{
		Name: "watch", Replicas: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude", Mode: api.RuntimeModeInteractive},
		Cadence: []api.Cadence{{
			Name: "sweep", Schedule: "17 6 * * *", Timezone: "UTC", Prompt: "sweep the queue",
			StartingDeadline: 10 * time.Minute, Grace: 6 * time.Hour,
		}},
	}
}

type cadenceRig struct {
	t     *testing.T
	store *api.Store
	ctrl  *Controller
	clock *testClock
	ring  *events.Ring
	ws    string
}

func newCadenceRig(t *testing.T, ws string, role api.Role) *cadenceRig {
	t.Helper()
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	r := &cadenceRig{t: t, store: store, ctrl: ctrl, clock: newTestClock(cadenceEpoch), ring: events.NewRing(200), ws: ws}
	ctrl.Events = r.ring
	ctrl.now = r.clock.Now
	createTeamFixture(t, store, ws, "squad", []api.Role{role})
	return r
}

func (r *cadenceRig) key() string { return r.ws + "/squad/watch/sweep" }

func (r *cadenceRig) tick() {
	r.t.Helper()
	r.ctrl.reconcileCadenceClock(r.store.ListTeams())
}

func (r *cadenceRig) status() api.CadenceStatus {
	st, _ := r.store.GetCadenceStatus(r.key())
	return st
}

func (r *cadenceRig) count(k events.Kind) int {
	return len(r.ring.Snapshot(events.Filter{Kind: k, Workspace: r.ws}, 0))
}

// The controller arms a cadence on first sight, opens one slot at the due
// time, and says so once.
func TestCadenceClockOpensOneSlotAtDue(t *testing.T) {
	r := newCadenceRig(t, "test-cad-open", cadenceRole())
	r.tick()
	if st := r.status(); !st.NextDueAt.Equal(cadenceDue(21)) || len(st.Slots) != 0 {
		t.Fatalf("first sight: %+v, want armed for %v with no slot", st, cadenceDue(21))
	}
	r.clock.Advance(cadenceDue(21).Sub(cadenceEpoch) + 2*time.Second)
	r.tick()
	r.tick()
	st := r.status()
	if len(st.Slots) != 1 || st.Slots[0].State != api.CadenceSlotOpen || st.Slots[0].ID != api.FiringID(cadenceDue(21)) {
		t.Fatalf("Slots = %+v, want one open slot %s", st.Slots, api.FiringID(cadenceDue(21)))
	}
	evs := r.ring.Snapshot(events.Filter{Kind: events.KindCadenceSlotOpened, Workspace: r.ws}, 0)
	if len(evs) != 1 || evs[0].Role != "watch" || !strings.Contains(evs[0].Message, api.FiringID(cadenceDue(21))) || !strings.Contains(evs[0].Message, "sweep") {
		t.Fatalf("slot-opened events = %+v, want one naming the cadence and the slot", evs)
	}
}

// A daemon restart in the outage: a new controller on the same store opens one
// catch-up slot within starting_deadline, and a later one past it records one
// skip.
func TestCadenceClockRecoversAfterARestart(t *testing.T) {
	r := newCadenceRig(t, "test-cad-restart", cadenceRole())
	r.tick()
	fresh := NewController(r.store, r.ctrl.sessMgr)
	fresh.now = r.clock.Now
	fresh.Events = r.ring
	r.ctrl = fresh

	r.clock.Advance(cadenceDue(23).Add(5 * time.Minute).Sub(cadenceEpoch))
	r.tick()
	st := r.status()
	if len(st.Slots) != 1 || !st.Slots[0].CatchUp || st.Slots[0].Missed != 2 || st.Slots[0].State != api.CadenceSlotOpen {
		t.Fatalf("Slots = %+v, want one open catch-up slot counting 2", st.Slots)
	}
	if r.count(events.KindCadenceSlotOpened) != 1 || r.count(events.KindCadenceSlotSkipped) != 0 {
		t.Fatalf("opened=%d skipped=%d, want 1 and 0", r.count(events.KindCadenceSlotOpened), r.count(events.KindCadenceSlotSkipped))
	}

	r.clock.Advance(cadenceDue(26).Add(time.Hour).Sub(r.clock.Now()))
	r.tick()
	st = r.status()
	last := st.Slots[len(st.Slots)-1]
	if last.State != api.CadenceSlotSkipped || last.Reason != api.CadenceSkipPastDeadline || last.Missed != 3 {
		t.Fatalf("last slot = %+v, want one skipped record counting 3", last)
	}
	if r.count(events.KindCadenceSlotSkipped) != 1 {
		t.Fatalf("skipped events = %d, want 1", r.count(events.KindCadenceSlotSkipped))
	}
}

// A team held at the start line with nothing live does not wait: the due time
// is recorded skipped, and nothing opens.
func TestCadenceClockSkipsAPostureWithheldTeam(t *testing.T) {
	r := newCadenceRig(t, "test-cad-posture", cadenceRole())
	if err := r.store.UpdateTeam("test-cad-posture/squad", func(live *api.Team) error {
		live.ConvergencePosture = api.PostureHold
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()
	r.clock.Advance(cadenceDue(21).Sub(cadenceEpoch) + 2*time.Second)
	r.tick()
	st := r.status()
	if len(st.Slots) != 1 || st.Slots[0].State != api.CadenceSlotSkipped || st.Slots[0].Reason != api.CadenceSkipPosture {
		t.Fatalf("Slots = %+v, want one skipped (posture)", st.Slots)
	}
	if r.count(events.KindCadenceSlotOpened) != 0 || r.count(events.KindCadenceSlotSkipped) != 1 {
		t.Fatalf("opened=%d skipped=%d, want 0 and 1", r.count(events.KindCadenceSlotOpened), r.count(events.KindCadenceSlotSkipped))
	}
}

// A role with no cadence leaves no status.
func TestCadenceClockIgnoresARoleWithoutACadence(t *testing.T) {
	role := cadenceRole()
	role.Cadence = nil
	r := newCadenceRig(t, "test-cad-none", role)
	r.tick()
	if n := len(r.store.ListCadenceStatus()); n != 0 {
		t.Fatalf("ListCadenceStatus = %d, want 0 for a role with no cadence", n)
	}
}

// The status of a cadence that is no longer declared, of a removed role, and
// of a deleted team is dropped, so no stale open slot is left for a claim.
func TestCadenceClockDropsStatusOfWhatIsGone(t *testing.T) {
	r := newCadenceRig(t, "test-cad-gone", cadenceRole())
	other := api.Role{Name: "other", Replicas: 1, Runtime: api.Runtime{Name: "claude", Command: "claude", Mode: api.RuntimeModeInteractive},
		Cadence: []api.Cadence{{Name: "keep", Schedule: "0 * * * *", Timezone: "UTC", Prompt: "p", StartingDeadline: time.Minute, Grace: 30 * time.Minute}}}
	if err := r.store.UpdateTeam("test-cad-gone/squad", func(live *api.Team) error {
		live.Roles = append(live.Roles, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if n := len(r.store.ListCadenceStatus()); n != 2 {
		t.Fatalf("setup: ListCadenceStatus = %d, want 2", n)
	}

	// The cadence is removed from its role.
	if err := r.store.UpdateTeam("test-cad-gone/squad", func(live *api.Team) error {
		live.Roles[0].Cadence = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if _, ok := r.store.GetCadenceStatus(r.key()); ok {
		t.Fatal("the status of a cadence no longer declared is still stored")
	}
	if _, ok := r.store.GetCadenceStatus("test-cad-gone/squad/other/keep"); !ok {
		t.Fatal("the status of a cadence still declared was dropped")
	}

	// The role is removed.
	if err := r.store.UpdateTeam("test-cad-gone/squad", func(live *api.Team) error {
		live.Roles = live.Roles[:1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if n := len(r.store.ListCadenceStatus()); n != 0 {
		t.Fatalf("ListCadenceStatus = %d after the role was removed, want 0", n)
	}

	// The team is deleted.
	if err := r.store.UpdateTeam("test-cad-gone/squad", func(live *api.Team) error {
		live.Roles = append(live.Roles, cadenceRole())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if _, ok := r.store.GetCadenceStatus(r.key()); !ok {
		t.Fatal("setup: the status was not recreated")
	}
	if err := r.store.DeleteTeam("test-cad-gone/squad"); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if n := len(r.store.ListCadenceStatus()); n != 0 {
		t.Fatalf("ListCadenceStatus = %d after the team was deleted, want 0", n)
	}
}

// The clock never spawns, types or claims: a due slot on a role with no seat
// opens, and no session is created.
func TestCadenceClockOpensASlotWhenNoSeatIsRunning(t *testing.T) {
	r := newCadenceRig(t, "test-cad-noseat", cadenceRole())
	r.tick()
	r.clock.Advance(cadenceDue(21).Sub(cadenceEpoch) + 2*time.Second)
	r.tick()
	if st := r.status(); len(st.Slots) != 1 || st.Slots[0].State != api.CadenceSlotOpen {
		t.Fatalf("Slots = %+v, want one open slot with no seat running", st.Slots)
	}
	if n := len(r.store.ListSessionsByTeamRole(r.ws, "squad", "watch")); n != 0 {
		t.Fatalf("the clock created %d sessions, want 0", n)
	}
}
