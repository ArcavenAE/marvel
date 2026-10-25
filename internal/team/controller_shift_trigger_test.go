package team

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The remainder trigger is scoped to the homogeneous 1M-Claude case: a
// single-replica claude role on a resolved window. These helpers fix the parts
// that never vary across the cases (the role name and the headroom) so each
// test varies only the occupancy it is probing.
const (
	testShiftRole     = "worker"
	testShiftHeadroom = 120_000
)

// shiftTriggerRole is a single-replica claude role that auto-shifts when a live
// session comes within testShiftHeadroom of its window.
func shiftTriggerRole() api.Role {
	return api.Role{
		Name:     testShiftRole,
		Replicas: 1,
		Runtime:  api.Runtime{Name: "claude", Command: "claude"},
		Shift: &api.ShiftPolicy{
			On: api.ShiftTriggerContextPressure, HeadroomTokens: testShiftHeadroom,
			// Context pressure is handoff-first: a pressure condition needs the
			// pair (marvel#442, the operator's ruling of 2026-10-04).
			Handoff:       filepath.Join(os.TempDir(), "marvel-pressure-test", "{session}.md"),
			HandoffMarker: "END HANDOFF",
		},
	}
}

// pressureAsked reports whether the team holds a pending context-pressure
// handoff request for the test role.
func pressureAsked(tm api.Team) bool {
	r, ok := tm.ShiftRequests[testShiftRole]
	return ok && r.Cause == api.ShiftTriggerContextPressure
}

// seedContext seeds one Running current-generation session and gives it a
// context reading, the same path the accountant writes through.
func seedContext(t *testing.T, store *api.Store, ws, team string, tokens, limit int) {
	t.Helper()
	s := api.Session{
		Name:       team + "-" + testShiftRole + "-g1-0",
		Workspace:  ws,
		Team:       team,
		Role:       testShiftRole,
		Generation: 1,
		State:      api.SessionRunning,
	}
	seedSession(t, store, s)
	store.UpdateSessionContext(s.Key(), api.SessionContext{
		ContextTokens: tokens,
		ContextLimit:  limit,
	})
}

func TestAutoShiftFiresOnContextRemainder(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-autoshift", "squad", []api.Role{shiftTriggerRole()})
	// remainder 100k <= headroom 120k -> fire
	seedContext(t, store, "test-autoshift", "squad", 900_000, 1_000_000)

	team, _ := store.GetTeam("test-autoshift/squad")
	ctrl.evaluateShiftTriggers(&team)

	got, _ := store.GetTeam("test-autoshift/squad")
	if !pressureAsked(got) || got.Shift.Phase != api.ShiftNone {
		t.Fatalf("requests = %+v, phase = %q, want a pressure handoff request and no shift (the remainder crossed headroom)", got.ShiftRequests, got.Shift.Phase)
	}
}

// A role's seats carry the generation of the last shift that covered the role,
// not the team's counter, so the pressure trigger must look at the role's live
// seats at any generation (marvel#924, the class of #451 on the other trigger).
func TestAutoShiftFiresOnSeatAtAnOlderGeneration(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-autoshift-oldgen", "squad", []api.Role{shiftTriggerRole()})
	// The seat is at generation 1, over the remainder, on a team at 4.
	seedContext(t, store, "test-autoshift-oldgen", "squad", 900_000, 1_000_000)
	if err := store.UpdateTeam("test-autoshift-oldgen/squad", func(live *api.Team) error {
		live.Generation = 4
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	team, _ := store.GetTeam("test-autoshift-oldgen/squad")
	ctrl.evaluateShiftTriggers(&team)

	got, _ := store.GetTeam("test-autoshift-oldgen/squad")
	if !pressureAsked(got) {
		t.Fatalf("requests = %+v, want a pressure handoff request: the seat is at generation 1 on a team at 4", got.ShiftRequests)
	}
}

func TestAutoShiftHoldsBelowRemainder(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-autoshift-hold", "squad", []api.Role{shiftTriggerRole()})
	// remainder 200k > headroom 120k -> no fire
	seedContext(t, store, "test-autoshift-hold", "squad", 800_000, 1_000_000)

	team, _ := store.GetTeam("test-autoshift-hold/squad")
	ctrl.evaluateShiftTriggers(&team)

	got, _ := store.GetTeam("test-autoshift-hold/squad")
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift phase = %q, want none (still 200k from the window)", got.Shift.Phase)
	}
}

func TestAutoShiftSkipsUnresolvedWindow(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-autoshift-nowin", "squad", []api.Role{shiftTriggerRole()})
	// high tokens but no denominator (codex, or opencode with no override)
	seedContext(t, store, "test-autoshift-nowin", "squad", 900_000, 0)

	team, _ := store.GetTeam("test-autoshift-nowin/squad")
	ctrl.evaluateShiftTriggers(&team)

	got, _ := store.GetTeam("test-autoshift-nowin/squad")
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift phase = %q, want none (an unresolved window cannot be metered)", got.Shift.Phase)
	}
}

func TestAutoShiftIgnoresRoleWithoutPolicy(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	role := api.Role{Name: testShiftRole, Replicas: 1, Runtime: api.Runtime{Name: "claude", Command: "claude"}}
	createTeamFixture(t, store, "test-autoshift-nopol", "squad", []api.Role{role})
	seedContext(t, store, "test-autoshift-nopol", "squad", 999_000, 1_000_000)

	team, _ := store.GetTeam("test-autoshift-nopol/squad")
	ctrl.evaluateShiftTriggers(&team)

	got, _ := store.GetTeam("test-autoshift-nopol/squad")
	if got.Shift.Phase != api.ShiftNone {
		t.Fatalf("shift phase = %q, want none (role declared no shift policy)", got.Shift.Phase)
	}
}

func TestAutoShiftDoesNotInterruptRunningShift(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-autoshift-inflight", "squad", []api.Role{shiftTriggerRole()})
	seedContext(t, store, "test-autoshift-inflight", "squad", 900_000, 1_000_000)
	// A shift is already draining; the trigger must leave it alone.
	if err := store.UpdateTeam("test-autoshift-inflight/squad", func(live *api.Team) error {
		live.Shift = api.ShiftState{Phase: api.ShiftDraining}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	team, _ := store.GetTeam("test-autoshift-inflight/squad")
	ctrl.evaluateShiftTriggers(&team)

	got, _ := store.GetTeam("test-autoshift-inflight/squad")
	if got.Shift.Phase != api.ShiftDraining {
		t.Fatalf("shift phase = %q, want draining unchanged (trigger must not touch an in-flight shift)", got.Shift.Phase)
	}
}

// TestAutoShiftPerTickBudgetCaps: two hot teams, one reconcile pass. Pressure
// asks for a handoff rather than shifting, so both ask and the shift budget (which
// caps the shifts that follow a marker, so an automatic actuator cannot burst
// the shared per-account rate limit) stays unspent.
func TestAutoShiftPerTickBudgetCaps(t *testing.T) {
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	// Two teams in two workspaces: the cap is fleet-wide, the shape a
	// multi-workspace fleet under one account presents.
	createTeamFixture(t, store, "test-budget-a", "alpha", []api.Role{shiftTriggerRole()})
	createTeamFixture(t, store, "test-budget-b", "beta", []api.Role{shiftTriggerRole()})
	seedContext(t, store, "test-budget-a", "alpha", 900_000, 1_000_000)
	seedContext(t, store, "test-budget-b", "beta", 900_000, 1_000_000)

	// One pass: the budget resets in ReconcileOnce, so simulate a single pass
	// by leaving autoShiftsThisTick at zero and evaluating both teams in order.
	ctrl.autoShiftsThisTick = 0
	alpha, _ := store.GetTeam("test-budget-a/alpha")
	ctrl.evaluateShiftTriggers(&alpha)
	beta, _ := store.GetTeam("test-budget-b/beta")
	ctrl.evaluateShiftTriggers(&beta)

	gotAlpha, _ := store.GetTeam("test-budget-a/alpha")
	gotBeta, _ := store.GetTeam("test-budget-b/beta")
	// A request is not a shift, so it does not spend the shift budget: both
	// teams ask in the same pass and neither shifts.
	for name, tm := range map[string]api.Team{"alpha": gotAlpha, "beta": gotBeta} {
		if !pressureAsked(tm) || tm.Shift.Phase != api.ShiftNone {
			t.Errorf("%s: requests = %+v, phase = %q, want a pressure request and no shift", name, tm.ShiftRequests, tm.Shift.Phase)
		}
	}
	if ctrl.autoShiftsThisTick != 0 {
		t.Errorf("autoShiftsThisTick = %d, want 0: asking is not a shift", ctrl.autoShiftsThisTick)
	}
}
