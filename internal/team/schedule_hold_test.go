package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// scheduledRole is a headless role on a schedule that would fail if it ever
// ran: under the ordinary restart policy a failure is refilled, which
// ADR-010 Amendment 1 says does not apply to a scheduled role.
func scheduledRole(replicas int) api.Role {
	return api.Role{
		Name: "refresh", Replicas: replicas,
		Runtime: api.Runtime{Name: "sh", Command: "sh", Args: []string{"-c", "exit 3"}, Mode: api.RuntimeModeHeadless},
		Schedule: &api.SchedulePolicy{
			Cron: "17 6 * * *", Timezone: "Etc/UTC",
			Concurrency: api.ScheduleConcurrencyForbid, OnFailure: api.ScheduleOnFailureWait,
			ActiveDeadline: 45 * time.Minute, StaleAfter: 30 * time.Hour,
		},
	}
}

// TestScheduledRoleIsHeldUntilTheClock: until S-3 builds the clock, a
// scheduled role spawns nothing on any tick, whatever its replica count,
// and the plan names the hold. Without it the role runs at apply as an
// ordinary Job and a failed run is refilled with no cap (marvel#429 review).
func TestScheduledRoleIsHeldUntilTheClock(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	clock := newTestClock(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	createTeamFixture(t, store, "test-sched-hold", "timers", []api.Role{scheduledRole(1)})

	for i := 0; i < 4; i++ {
		ctrl.ReconcileOnce()
		clock.Advance(10 * time.Minute)
	}
	if got := store.ListSessionsByTeamRole("test-sched-hold", "timers", "refresh"); len(got) != 0 {
		t.Fatalf("scheduled role spawned %d sessions over 4 ticks, want 0: %+v", len(got), summarize(got))
	}
	role := scheduledRole(1)
	plan := ctrl.planRole(mustTeam(t, store, "test-sched-hold/timers"), &role, 1)
	if plan.Action != RoleHold || plan.Spawn != 0 || plan.Hold != HoldReason("schedule") {
		t.Fatalf("plan = %+v, want a hold named schedule with nothing spawned", plan)
	}
}

// TestScheduledRoleIsNotLaunchedByAShift: a shift's launch path spawns on
// its own, outside planRole, so it must hold a scheduled role as well.
func TestScheduledRoleIsNotLaunchedByAShift(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	createTeamFixture(t, store, "test-sched-shift", "timers", []api.Role{scheduledRole(1)})
	if err := ctrl.InitiateShift("test-sched-shift/timers", ""); err != nil {
		t.Fatalf("initiate shift: %v", err)
	}
	for i := 0; i < 3; i++ {
		ctrl.ReconcileOnce()
	}
	if got := store.ListSessionsByTeamRole("test-sched-shift", "timers", "refresh"); len(got) != 0 {
		t.Fatalf("a shift launched %d sessions for a scheduled role, want 0: %+v", len(got), summarize(got))
	}
}
