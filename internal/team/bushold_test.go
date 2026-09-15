package team

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

func countKind(r *events.Ring, k events.Kind) int {
	n := 0
	for _, e := range r.Snapshot(events.Filter{}, 0) {
		if e.Kind == k {
			n++
		}
	}
	return n
}

// TestBusGateHoldsSpawnOncePerTransition: a bus that is not ready turns a
// would-be spawn into RoleHold/HoldBus, announces it once however many ticks
// it lasts, charges no crash, and releases quietly when the gate admits.
func TestBusGateHoldsSpawnOncePerTransition(t *testing.T) {
	store := api.NewStore()
	ring := events.NewRing(0)
	ctrl := NewController(store, nil)
	ctrl.Events = ring
	ready, reason := false, "bus 127.0.0.1:4222 is not ready"
	ctrl.BusGate = func() (bool, string) { return ready, reason }

	role := api.Role{Name: "worker", Replicas: 2}
	team := &api.Team{Name: "ops", Workspace: planTestWS, Roles: []api.Role{role}}
	if err := store.CreateWorkspace(&api.Workspace{Name: planTestWS}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	for tick := 0; tick < 3; tick++ {
		plan := ctrl.planRole(team, &role, planTestGen)
		if plan.Action != RoleHold || plan.Hold != HoldBus || plan.Spawn != 0 || plan.HoldDetail != reason {
			t.Fatalf("tick %d plan = %+v, want RoleHold/HoldBus with the gate's reason", tick, plan)
		}
		ctrl.applyRolePlan(team, &role, plan)
	}
	if n := countKind(ring, events.KindBusUnavailable); n != 1 {
		t.Errorf("bus.unavailable emitted %d times over 3 held ticks, want 1", n)
	}
	if _, charged := ctrl.roleHealth[planTestWS+"/ops/worker"]; charged {
		t.Error("a bus hold charged the role's crash record")
	}
	if len(store.ListSessionsByTeam(planTestWS, "ops")) != 0 {
		t.Error("a held tick spawned")
	}

	// The gate admits: the plan spawns and the latch clears without an event.
	ready = true
	plan := ctrl.planRole(team, &role, planTestGen)
	if plan.Action != RoleSpawn || plan.Spawn != 2 || plan.Hold != HoldNone {
		t.Fatalf("plan after the bus came back = %+v, want RoleSpawn 2", plan)
	}
	// applyRolePlan would call the nil session manager to spawn; run just the
	// latch half it would run first.
	ctrl.clearBusHold(team, role.Name)
	if ctrl.busHolds[planTestWS+"/ops/worker"] {
		t.Error("latch still set after the gate admitted")
	}
	if n := countKind(ring, events.KindBusUnavailable); n != 1 {
		t.Errorf("release re-announced: bus.unavailable count %d", n)
	}

	// A second outage announces again: one event per transition.
	ready = false
	plan = ctrl.planRole(team, &role, planTestGen)
	ctrl.applyRolePlan(team, &role, plan)
	if n := countKind(ring, events.KindBusUnavailable); n != 2 {
		t.Errorf("second outage: bus.unavailable count %d, want 2", n)
	}
}

// TestNoBusGateMeansNoHold: a cluster without a managed bus has a nil gate and
// the plan is exactly what it was before the gate existed.
func TestNoBusGateMeansNoHold(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)
	role := api.Role{Name: "worker", Replicas: 1}
	team := &api.Team{Name: "ops", Workspace: planTestWS, Roles: []api.Role{role}}
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	plan := ctrl.planRole(team, &role, planTestGen)
	if plan.Action != RoleSpawn || plan.bus != busNoop {
		t.Fatalf("plan with no gate = %+v, want RoleSpawn and no bus intent", plan)
	}
}
