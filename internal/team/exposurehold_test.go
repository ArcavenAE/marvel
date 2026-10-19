package team

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/session"
)

// The exposure gate holds a role that writes a function its slot has read
// (marvel#945). The three lifecycle tests are the ones a record held on a
// session row fails: each drives the real step that deletes the row, then asks
// the planner for a new spawn.

const exposureWS = "xws"

func exposureWriter() api.Role {
	return api.Role{
		Name: "writer", Replicas: 1, Writes: []string{"parse"},
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
}

func exposureEnv(t *testing.T, role api.Role) (*api.Store, *Controller, *api.Team) {
	t.Helper()
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ctrl.Events = events.NewRing(0)
	createTeamFixture(t, store, exposureWS, "ops", []api.Role{role})
	got, err := store.GetTeam(exposureWS + "/ops")
	if err != nil {
		t.Fatal(err)
	}
	team := &got
	if _, err := store.RecordExposure(api.ExposureRecord{
		Workspace: exposureWS, Team: "ops", Role: "writer", Function: "parse",
		Caster: "supervisor", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return store, ctrl, team
}

func seedWriterRow(t *testing.T, store *api.Store, name string, gen int64, state api.SessionState, predecessor string) api.Session {
	t.Helper()
	sess := api.Session{
		Name: name, Workspace: exposureWS, Team: "ops", Role: "writer",
		Generation: gen, State: state, Predecessor: predecessor,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSession(&sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func wantExposureHold(t *testing.T, ctrl *Controller, team *api.Team, role api.Role, when string) {
	t.Helper()
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	plan := ctrl.planRole(team, &role, team.Generation)
	if plan.Action != RoleHold || plan.Hold != HoldExposure || plan.Spawn != 0 {
		t.Errorf("%s: plan = %+v, want RoleHold/HoldExposure with no spawn", when, plan)
	}
}

// TestExposureHoldsAfterAHealthRestart: restartSession deletes the failed row.
func TestExposureHoldsAfterAHealthRestart(t *testing.T) {
	role := exposureWriter()
	store, ctrl, team := exposureEnv(t, role)
	sess := seedWriterRow(t, store, "writer-0", team.Generation, api.SessionRunning, "")

	ctrl.mu.Lock()
	ctrl.restartSession(&sess, team, &role)
	ctrl.mu.Unlock()
	if rows := store.ListSessionsByTeamRole(exposureWS, "ops", "writer"); len(rows) != 0 {
		t.Fatalf("setup: the restart left %d rows, want the row deleted", len(rows))
	}
	wantExposureHold(t, ctrl, team, role, "after a health restart deleted the row")
}

// TestExposureHoldsAfterAShiftDrain: the successor names its predecessor, then
// shiftDrain deletes the predecessor, leaving a chain that points at nothing. A
// replica added after the drain is still refused.
func TestExposureHoldsAfterAShiftDrain(t *testing.T) {
	role := exposureWriter()
	store, ctrl, team := exposureEnv(t, role)
	seedWriterRow(t, store, "writer-0", 1, api.SessionRunning, "")
	if err := store.UpdateTeam(team.Key(), func(live *api.Team) error {
		live.Generation = 2
		live.Shift = api.ShiftState{Phase: api.ShiftDraining, Roles: []string{"writer"}, OldGeneration: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seedWriterRow(t, store, "writer-1", 2, api.SessionRunning, exposureWS+"/writer-0")
	got, _ := store.GetTeam(team.Key())
	team = &got

	ctrl.mu.Lock()
	ctrl.shiftDrain(team, &role)
	ctrl.mu.Unlock()
	if _, err := store.GetSession(exposureWS + "/writer-0"); err == nil {
		t.Fatal("setup: the drain left the predecessor row in place")
	}
	role.Replicas = 2 // one more replica than the successor alone
	wantExposureHold(t, ctrl, team, role, "after a drain deleted the predecessor")
}

// TestExposureHoldsAfterACrashRespawn: ClearCrashedForRole runs just before the
// replacement is created and deletes the crashed row.
func TestExposureHoldsAfterACrashRespawn(t *testing.T) {
	role := exposureWriter()
	store, ctrl, team := exposureEnv(t, role)
	seedWriterRow(t, store, "writer-0", team.Generation, api.SessionCrashed, "")

	ctrl.sessMgr.ClearCrashedForRole(exposureWS, "ops", "writer")
	if rows := store.ListSessionsByTeamRole(exposureWS, "ops", "writer"); len(rows) != 0 {
		t.Fatalf("setup: %d rows left after the crashed row was cleared", len(rows))
	}
	wantExposureHold(t, ctrl, team, role, "after a crash respawn cleared the row")
}

// TestExposureGateLeavesOtherRolesAlone: the controls. A function the slot has
// not read, and a role that declares no Writes, both spawn.
func TestExposureGateLeavesOtherRolesAlone(t *testing.T) {
	role := exposureWriter()
	_, ctrl, team := exposureEnv(t, role)

	other := role
	other.Writes = []string{"emit"}
	none := role
	none.Writes = nil
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	for name, r := range map[string]api.Role{"a function the slot has not read": other, "no Writes": none} {
		plan := ctrl.planRole(team, &r, team.Generation)
		if plan.Action != RoleSpawn || plan.Spawn != 1 || plan.Hold != HoldNone {
			t.Errorf("%s: plan = %+v, want RoleSpawn 1", name, plan)
		}
	}
}

type failingExposures struct{ calls int }

func (f *failingExposures) ExposuresForSlot(_, _, _ string) ([]api.ExposureRecord, error) {
	f.calls++
	return nil, errors.New("exposure bucket unreadable")
}

// TestExposureGateFailsClosed: a record that cannot be read holds a role that
// declares Writes, and the reason says so. A role with none never reads it.
func TestExposureGateFailsClosed(t *testing.T) {
	role := exposureWriter()
	_, ctrl, team := exposureEnv(t, role)
	src := &failingExposures{}
	ctrl.Exposures = src

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	plan := ctrl.planRole(team, &role, team.Generation)
	if plan.Action != RoleHold || plan.Hold != HoldExposure || !strings.Contains(plan.HoldDetail, "unreadable") {
		t.Errorf("unreadable record: plan = %+v, want a hold whose reason carries the error", plan)
	}

	none := role
	none.Writes = nil
	before := src.calls
	plan = ctrl.planRole(team, &none, team.Generation)
	if plan.Action != RoleSpawn || src.calls != before {
		t.Errorf("a role with no Writes: plan = %+v, reads %d, want a spawn and no read", plan, src.calls-before)
	}
}

// TestExposureHoldAnnouncesOncePerTransition: a standing hold is one event, not
// one per tick, and it charges no crash.
func TestExposureHoldAnnouncesOncePerTransition(t *testing.T) {
	role := exposureWriter()
	_, ctrl, team := exposureEnv(t, role)
	ring := ctrl.Events.(*events.Ring)

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	for tick := 0; tick < 3; tick++ {
		plan := ctrl.planRole(team, &role, team.Generation)
		ctrl.applyRolePlan(team, &role, plan)
	}
	if n := countKind(ring, events.KindExposureRefused); n != 1 {
		t.Errorf("exposure.refused emitted %d times over 3 held ticks, want 1", n)
	}
	if _, charged := ctrl.roleHealth[exposureWS+"/ops/writer"]; charged {
		t.Error("an exposure hold charged the role's crash record")
	}
}

// TestShiftLaunchRefusesAWriterWhoseSlotHasRead: shiftLaunch creates the
// successors itself, and reconcile never plans the shifting role, so the gate in
// planRole does not cover it. The real launch must create no successor, hold the
// shift in launching, and say so once.
func TestShiftLaunchRefusesAWriterWhoseSlotHasRead(t *testing.T) {
	role := exposureWriter()
	store, ctrl, team := exposureEnv(t, role)
	seedWriterRow(t, store, "writer-0", 1, api.SessionRunning, "")
	if err := store.UpdateTeam(team.Key(), func(live *api.Team) error {
		live.Generation = 2
		live.Shift = api.ShiftState{Phase: api.ShiftLaunching, Roles: []string{"writer"}, OldGeneration: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetTeam(team.Key())
	team = &got

	ctrl.mu.Lock()
	for tick := 0; tick < 2; tick++ {
		ctrl.shiftLaunch(team, &role)
	}
	ctrl.mu.Unlock()
	if rows := store.ListSessionsByTeamRoleGeneration(exposureWS, "ops", "writer", 2); len(rows) != 0 {
		t.Errorf("shiftLaunch created %d successor rows for a writer whose slot has read, want none", len(rows))
	}
	if team.Shift.Phase != api.ShiftLaunching {
		t.Errorf("shift phase = %s, want launching: nothing was launched", team.Shift.Phase)
	}
	if n := countKind(ctrl.Events.(*events.Ring), events.KindExposureRefused); n != 1 {
		t.Errorf("exposure.refused emitted %d times over 2 ticks, want 1", n)
	}
}

// TestSessionCreateRefusesAWriterWhoseSlotHasRead: every spawn ends in
// Manager.Create, so the refusal sits there too, for the paths that do not go
// through a plan. No row is left behind.
func TestSessionCreateRefusesAWriterWhoseSlotHasRead(t *testing.T) {
	role := exposureWriter()
	store, ctrl, _ := exposureEnv(t, role)

	sess := &api.Session{Name: "writer-9", Workspace: exposureWS, Team: "ops", Role: "writer", Runtime: role.Runtime}
	err := ctrl.sessMgr.Create(sess)
	if !errors.Is(err, session.ErrExposureRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrExposureRefused", err)
	}
	if _, gerr := store.GetSession(sess.Key()); gerr == nil {
		t.Error("a refused Create left a session row")
	}
}
