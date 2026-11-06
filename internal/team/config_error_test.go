package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// ADR-010 Amendment 2: a headless run that exited 78 holds its replica slot
// like a completed run, so the reconciler plans nothing for the role, charges
// no RoleHealth and creates no freeze. A person deleting the held session
// releases the slot, and the next plan spawns a replacement. An exit-1 row
// frees its slot as before.
func TestConfigErrorRowHoldsItsSlotWithoutHealthOrFreeze(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)
	ctrl.now = newTestClock(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)).Now

	job := api.Runtime{Name: "sh", Command: "sh", Args: []string{"-c", "exit 78"}, Mode: api.RuntimeModeHeadless}
	createTeamFixture(t, store, "test-e78", "jobs", []api.Role{
		{Name: "cfg", Replicas: 1, Runtime: job},
		{Name: "crash", Replicas: 1, Runtime: job},
	})
	seed := func(role, status string, st api.SessionState) {
		seedSession(t, store, api.Session{
			Name: "jobs-" + role + "-g1-0", Workspace: "test-e78", Team: "jobs", Role: role,
			Generation: 1, State: st, ExitStatus: status, Runtime: job,
		})
	}
	seed("cfg", "78", api.SessionFailed)
	seed("crash", "1", api.SessionCrashed)

	team := mustTeam(t, store, "test-e78/jobs")
	plan := ctrl.planRole(team, &team.Roles[0], 1)
	if plan.Action != RoleSteady || plan.Actual != 1 {
		t.Fatalf("cfg plan = %+v, want steady with actual=1 (the exit-78 run holds its slot)", plan)
	}
	if plan := ctrl.planRole(team, &team.Roles[1], 1); plan.Action != RoleSpawn {
		t.Fatalf("crash plan = %+v, want a spawn (an exit-1 row frees its slot)", plan)
	}
	if _, ok := ctrl.RoleHealthSnapshot("test-e78", "jobs", "cfg"); ok {
		t.Fatal("an exit-78 row has RoleHealth; it must hold its slot with no backoff entry and no freeze")
	}

	if err := store.DeleteSession("test-e78/jobs-cfg-g1-0"); err != nil {
		t.Fatalf("delete held session: %v", err)
	}
	if plan := ctrl.planRole(team, &team.Roles[0], 1); plan.Action != RoleSpawn {
		t.Fatalf("cfg plan after the held session was deleted = %+v, want a spawn", plan)
	}
}
