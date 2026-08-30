package team

import (
	"fmt"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// planTestWS and planTestGen are the workspace and generation every
// side-effect-free plan test seeds into. These tests never spawn, so both are
// fixed rather than parameters (their only value would be these constants).
const (
	planTestWS  = "ws"
	planTestGen = int64(1)
)

// addAliveSessions inserts n running sessions for a role straight into the
// store, bypassing the session manager (and tmux) entirely. The reconcile
// decision is store-based, so the plan tests need real rows but not real panes.
func addAliveSessions(t *testing.T, store *api.Store, team, role string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := store.CreateSession(&api.Session{
			Name:       fmt.Sprintf("%s-%s-g%d-%d", team, role, planTestGen, i),
			Workspace:  planTestWS,
			Team:       team,
			Role:       role,
			Generation: planTestGen,
			State:      api.SessionRunning,
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
}

// sessionKeySet is a helper for order-independent membership assertions.
func sessionKeySet(sessions []api.Session) map[string]struct{} {
	set := make(map[string]struct{}, len(sessions))
	for i := range sessions {
		set[sessions[i].Key()] = struct{}{}
	}
	return set
}

// --- Property 1: planRole/PlanConvergence are side-effect-free ---------------

// TestPlanRoleDecision is the pure decision table: every convergence shape,
// asserted on the RolePlan value. The session manager is nil, so a stray spawn
// or delete inside planRole would panic rather than pass silently — that is the
// guard this test adds. Store-write / event / admission-latch purity is proven
// separately by TestPlanConvergenceIsSideEffectFree; together they cover
// planRole's side-effect-freedom.
func TestPlanRoleDecision(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		budget     api.Budget
		replicas   int
		alive      int
		backoff    bool // seed a crash-loop backoff window in the future
		wantAction RoleAction
		wantSpawn  int
		wantDelete int
		wantHold   HoldReason
		wantAdmOp  admissionOp
		admKeySet  bool // admission latch must carry a non-empty key + reason
	}{
		{
			name:       "scale up, no budget",
			replicas:   3,
			alive:      0,
			wantAction: RoleSpawn,
			wantSpawn:  3,
			wantHold:   HoldNone,
			wantAdmOp:  admissionClear, // top clear fires on !Budget.Declared()
		},
		{
			name:       "scale down",
			replicas:   1,
			alive:      3,
			wantAction: RoleScaleDown,
			wantDelete: 2,
			wantHold:   HoldNone,
			wantAdmOp:  admissionClear, // top clear fires on actual>=desired
		},
		{
			name:       "steady",
			replicas:   2,
			alive:      2,
			wantAction: RoleSteady,
			wantHold:   HoldNone,
			wantAdmOp:  admissionClear,
		},
		{
			name:       "backoff hold keeps the admission latch untouched",
			budget:     api.Budget{MaxSessions: 5},
			replicas:   2,
			alive:      1,
			backoff:    true,
			wantAction: RoleHold,
			wantHold:   HoldBackoff,
			wantAdmOp:  admissionNoop, // budget declared + gate not reached => untouched
		},
		{
			name:       "admission full grant",
			budget:     api.Budget{MaxSessions: 5},
			replicas:   3,
			alive:      2,
			wantAction: RoleSpawn,
			wantSpawn:  1,
			wantHold:   HoldNone,
			wantAdmOp:  admissionClear,
		},
		{
			name:       "admission full refusal",
			budget:     api.Budget{MaxSessions: 2},
			replicas:   3,
			alive:      2,
			wantAction: RoleHold,
			wantHold:   HoldAdmission,
			wantAdmOp:  admissionLatch,
			admKeySet:  true,
		},
		{
			name:       "admission partial grant spawns and holds",
			budget:     api.Budget{MaxSessions: 3},
			replicas:   5,
			alive:      2,
			wantAction: RoleSpawn,
			wantSpawn:  1, // headroom(3,2)=1 of a 3-session deficit
			wantHold:   HoldAdmission,
			wantAdmOp:  admissionLatch,
			admKeySet:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := api.NewStore()
			ctrl := NewController(store, nil)
			ctrl.now = func() time.Time { return fixed }

			const ws, team, role = "ws", "team", "worker"
			team1 := api.Team{
				Name:       team,
				Workspace:  ws,
				Budget:     tc.budget,
				Roles:      []api.Role{{Name: role, Replicas: tc.replicas, Runtime: api.Runtime{Name: "sleep", Command: "sleep"}}},
				Generation: 1,
			}
			addAliveSessions(t, store, team, role, tc.alive)
			if tc.backoff {
				ctrl.roleHealth[ws+"/"+team+"/"+role] = &RoleHealth{
					RestartCount: 2,
					BackoffUntil: fixed.Add(time.Minute),
				}
			}

			plan := ctrl.planRole(&team1, &team1.Roles[0], team1.Generation)

			if plan.Action != tc.wantAction {
				t.Errorf("Action = %q, want %q", plan.Action, tc.wantAction)
			}
			if plan.Spawn != tc.wantSpawn {
				t.Errorf("Spawn = %d, want %d", plan.Spawn, tc.wantSpawn)
			}
			if len(plan.Delete) != tc.wantDelete {
				t.Errorf("len(Delete) = %d, want %d", len(plan.Delete), tc.wantDelete)
			}
			if plan.Hold != tc.wantHold {
				t.Errorf("Hold = %q, want %q", plan.Hold, tc.wantHold)
			}
			if plan.admission.op != tc.wantAdmOp {
				t.Errorf("admission.op = %d, want %d", plan.admission.op, tc.wantAdmOp)
			}
			if tc.admKeySet && (plan.admission.key == "" || plan.admission.reason == "") {
				t.Errorf("admission latch missing key/reason: %+v", plan.admission)
			}
			if plan.Desired != tc.replicas || plan.Actual != tc.alive {
				t.Errorf("Desired/Actual = %d/%d, want %d/%d", plan.Desired, plan.Actual, tc.replicas, tc.alive)
			}
		})
	}
}

// TestPlanScaleDownNamesExactExcess asserts a scale-down plan names exactly the
// excess (Actual-Desired) sessions to remove, each a distinct, existing session
// of the role. The old reconciler and planRole both index one store snapshot
// identically (current[len-1-i]); since ListSessionsByTeamRole returns map
// order, the SET is the deterministic invariant to pin here, and
// TestPlanThenApplyScaleDownDeletesExactly proves apply removes exactly that set.
func TestPlanScaleDownNamesExactExcess(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)

	const ws, team, role = "ws", "team", "worker"
	addAliveSessions(t, store, team, role, 3) // indices 0,1,2
	team1 := api.Team{
		Name: team, Workspace: ws, Generation: 1,
		Roles: []api.Role{{Name: role, Replicas: 1}},
	}

	plan := ctrl.planRole(&team1, &team1.Roles[0], 1)
	if plan.Action != RoleScaleDown || len(plan.Delete) != 2 {
		t.Fatalf("plan = %+v, want scale_down deleting 2", plan)
	}

	existing := sessionKeySet(store.ListSessionsByTeamRole(ws, team, role))
	seen := make(map[string]struct{})
	for _, sess := range plan.Delete {
		if _, ok := existing[sess.Key()]; !ok {
			t.Errorf("Delete names %s, which is not a current session of the role", sess.Key())
		}
		if _, dup := seen[sess.Key()]; dup {
			t.Errorf("Delete names %s twice", sess.Key())
		}
		seen[sess.Key()] = struct{}{}
	}
}

// TestPlanConvergenceIsSideEffectFree is the purity guard. It exercises the
// path most likely to leak — an over-budget team whose apply would latch a
// hold, emit admission.refused, and write Team.Admission — and asserts that
// computing the plan does none of it: no sessions created or deleted, no
// events, no admission latch, no store record touched.
func TestPlanConvergenceIsSideEffectFree(t *testing.T) {
	store := api.NewStore()
	ring := events.NewRing(0)
	ctrl := NewController(store, nil)
	ctrl.Events = ring

	const ws, team, role = "ws", "crew", "crew"
	createBudgetTeamFixture(t, store, ws, team, api.Budget{MaxSessions: 2}, []api.Role{sleepRole(role, 3)})
	addAliveSessions(t, store, team, role, 2) // over budget: declared 3 > ceiling 2

	before := sessionKeySet(store.ListSessions())

	var plans []RolePlan
	for i := 0; i < 3; i++ {
		plans = ctrl.PlanConvergence()
	}

	// The decision is real: the role wants a third replica but admission holds it.
	if len(plans) != 1 {
		t.Fatalf("expected 1 role plan, got %d", len(plans))
	}
	if plans[0].Action != RoleHold || plans[0].Hold != HoldAdmission {
		t.Fatalf("plan = %+v, want hold on admission", plans[0])
	}

	// ... and it changed nothing.
	after := sessionKeySet(store.ListSessions())
	if len(after) != len(before) {
		t.Errorf("session count changed: before %d, after %d", len(before), len(after))
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("session %s disappeared during planning", k)
		}
	}
	if got := len(ctrl.admissionHolds); got != 0 {
		t.Errorf("planning latched %d admission hold(s), want 0", got)
	}
	if evs := ring.Snapshot(events.Filter{}, 0); len(evs) != 0 {
		t.Errorf("planning emitted %d event(s), want 0: %+v", len(evs), evs)
	}
	if tm, _ := store.GetTeam(ws + "/" + team); tm.Admission.Held {
		t.Errorf("planning wrote Team.Admission = %+v, want zero", tm.Admission)
	}
	if got := len(ctrl.SnapshotRoleHealth()); got != 0 {
		t.Errorf("planning wrote %d role-health record(s), want 0", got)
	}
}

// --- Property 2: applyRolePlan enacts exactly the plan -----------------------

// TestApplyRolePlanLatchesAndClearsAdmission proves apply performs precisely the
// admission bookkeeping the plan carries — latch-and-emit on a hold intent,
// once per transition — and clears it on a clear intent. No tmux: the hold path
// touches only the store and the event ring.
func TestApplyRolePlanLatchesAndClearsAdmission(t *testing.T) {
	store := api.NewStore()
	ring := events.NewRing(0)
	ctrl := NewController(store, nil)
	ctrl.Events = ring

	const ws, team, role = "ws", "crew", "crew"
	createBudgetTeamFixture(t, store, ws, team, api.Budget{MaxSessions: 2}, []api.Role{sleepRole(role, 3)})
	addAliveSessions(t, store, team, role, 2)
	teamKey := ws + "/" + team

	// Latch.
	ctrl.mu.Lock()
	tm, _ := store.GetTeam(teamKey)
	plan := ctrl.planRole(&tm, &tm.Roles[0], tm.Generation)
	if plan.admission.op != admissionLatch {
		ctrl.mu.Unlock()
		t.Fatalf("plan.admission.op = %d, want latch", plan.admission.op)
	}
	ctrl.applyRolePlan(&tm, &tm.Roles[0], plan)
	// Idempotent within the same standing condition: a second apply of the same
	// verdict key must not emit a second event.
	ctrl.applyRolePlan(&tm, &tm.Roles[0], plan)
	ctrl.mu.Unlock()

	if _, held := ctrl.AdmissionHold(ws, team, role); !held {
		t.Errorf("apply did not latch the admission hold")
	}
	if got := len(admissionEvents(t, ring, events.KindAdmissionRefused)); got != 1 {
		t.Errorf("admission.refused events = %d, want exactly 1 (once per transition)", got)
	}
	if got, _ := store.GetTeam(teamKey); !got.Admission.Held || got.Admission.Role != role {
		t.Errorf("Team.Admission = %+v, want held on role %s", got.Admission, role)
	}

	// Clear: raise the ceiling so the gate is satisfied, then apply the fresh plan.
	setMaxSessions(t, store, teamKey, 5)
	ctrl.mu.Lock()
	tm, _ = store.GetTeam(teamKey)
	clearPlan := ctrl.planRole(&tm, &tm.Roles[0], tm.Generation)
	if clearPlan.admission.op != admissionClear {
		ctrl.mu.Unlock()
		t.Fatalf("clearPlan.admission.op = %d, want clear", clearPlan.admission.op)
	}
	// Enact ONLY the admission bookkeeping — drop the spawn so this stays
	// tmux-free and isolates the clear.
	clearPlan.Spawn = 0
	clearPlan.Action = RoleSteady
	ctrl.applyRolePlan(&tm, &tm.Roles[0], clearPlan)
	ctrl.mu.Unlock()

	if _, held := ctrl.AdmissionHold(ws, team, role); held {
		t.Errorf("apply did not clear the admission hold")
	}
	if got := len(admissionEvents(t, ring, events.KindAdmissionCleared)); got != 1 {
		t.Errorf("admission.cleared events = %d, want exactly 1", got)
	}
}

// TestApplyRolePlanObeysPlanNotState is the sharpest proof of the seam: apply
// enacts the plan it is handed, not a fresh reading of the world. A team already
// at its desired count is handed a plan that spawns one anyway, and apply
// spawns it — so apply is enacting the decision, never re-deciding.
func TestApplyRolePlanObeysPlanNotState(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	const ws, team, role = "obey", "agents", "worker"
	createTeamFixture(t, store, ws, team, []api.Role{sleepRole(role, 1)})
	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole(ws, team, role)); got != 1 {
		t.Fatalf("expected 1 session after reconcile, got %d", got)
	}

	tm, _ := store.GetTeam(ws + "/" + team)
	plan := RolePlan{
		Workspace: ws, Team: team, Role: role, Generation: 1,
		Desired: 1, Actual: 1, Action: RoleSpawn, Spawn: 1,
	}
	ctrl.mu.Lock()
	ctrl.applyRolePlan(&tm, &tm.Roles[0], plan)
	ctrl.mu.Unlock()

	if got := len(store.ListSessionsByTeamRole(ws, team, role)); got != 2 {
		t.Errorf("apply of Spawn=1 against a satisfied role produced %d sessions, want 2 (apply obeys the plan)", got)
	}
}

// TestPlanThenApplyScaleDownDeletesExactly runs the two halves back to back and
// asserts apply removes exactly the sessions the plan named — no more, no fewer,
// and the survivor is the one the plan did not name.
func TestPlanThenApplyScaleDownDeletesExactly(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	const ws, team, role = "exact", "agents", "worker"
	createTeamFixture(t, store, ws, team, []api.Role{sleepRole(role, 3)})
	ctrl.ReconcileOnce()
	if got := len(store.ListSessionsByTeamRole(ws, team, role)); got != 3 {
		t.Fatalf("expected 3 sessions, got %d", got)
	}

	setReplicas(t, store, ws+"/"+team, role, 1)

	ctrl.mu.Lock()
	tm, _ := store.GetTeam(ws + "/" + team)
	plan := ctrl.planRole(&tm, &tm.Roles[0], tm.Generation)
	ctrl.mu.Unlock()
	if plan.Action != RoleScaleDown || len(plan.Delete) != 2 {
		t.Fatalf("plan = %+v, want scale_down deleting 2", plan)
	}
	doomed := sessionKeySet(plan.Delete)

	ctrl.mu.Lock()
	ctrl.applyRolePlan(&tm, &tm.Roles[0], plan)
	ctrl.mu.Unlock()

	survivors := store.ListSessionsByTeamRole(ws, team, role)
	if len(survivors) != 1 {
		t.Fatalf("expected 1 survivor, got %d", len(survivors))
	}
	if _, doomedSurvived := doomed[survivors[0].Key()]; doomedSurvived {
		t.Errorf("survivor %s was named in the plan's Delete set", survivors[0].Key())
	}
}
