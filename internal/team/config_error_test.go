package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
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

// On a scheduled role with on_failure = freeze, an exit-78 run freezes the
// schedule, and deleting the held session releases the slot but does not clear
// the freeze: later firings are skipped until reset-health. The reason text
// has to say so.
func TestConfigErrorOnAFrozenScheduleNeedsResetHealthToo(t *testing.T) {
	store := api.NewStore()
	ctrl := NewController(store, nil)
	ring := events.NewRing(32)
	ctrl.Events = ring
	start := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	clock := newTestClock(start)
	ctrl.now = clock.Now

	job := api.Runtime{Name: "sh", Command: "sh", Args: []string{"-c", "exit 78"}, Mode: api.RuntimeModeHeadless}
	pol := &api.SchedulePolicy{
		Cron: "17 6 * * *", Timezone: "Etc/UTC",
		Concurrency: api.ScheduleConcurrencyForbid, OnFailure: api.ScheduleOnFailureFreeze,
		ActiveDeadline: 45 * time.Minute, StaleAfter: 30 * time.Hour,
		History: &api.ScheduleHistory{Succeeded: 3, Failed: 3},
	}
	createTeamFixture(t, store, "test-e78-freeze", "timers", []api.Role{{Name: "refresh", Replicas: 1, Runtime: job, Schedule: pol}})
	const key = "test-e78-freeze/timers/refresh"
	// First sight of the schedule fixes its next due time.
	first := mustTeam(t, store, "test-e78-freeze/timers")
	ctrl.stepScheduleClock(first, &first.Roles[0], clock.Now())
	firingDue := start.Add(17 * time.Minute)
	if _, err := store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
		st.Firing, st.FiringDueAt = api.FiringID(firingDue), firingDue
		return true
	}); err != nil {
		t.Fatal(err)
	}
	seedSession(t, store, api.Session{
		Name: "timers-refresh-g1-0", Workspace: "test-e78-freeze", Team: "timers", Role: "refresh",
		Generation: 1, State: api.SessionFailed, ExitStatus: "78", Runtime: job, Firing: api.FiringID(firingDue),
	})
	if _, err := store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
		return st.SettleRun(api.RunRecord{Outcome: api.RunFailed, Firing: st.Firing, ExitStatus: "78"}, *pol, firingDue.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	if st, _ := store.GetScheduleStatus(key); !st.Frozen {
		t.Fatal("an exit-78 run on on_failure = freeze did not freeze the schedule")
	}

	if err := store.DeleteSession("test-e78-freeze/timers-refresh-g1-0"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(17*time.Minute + 30*time.Second)
	team := mustTeam(t, store, "test-e78-freeze/timers")
	ctrl.stepScheduleClock(team, &team.Roles[0], clock.Now())

	if st, _ := store.GetScheduleStatus(key); !st.Frozen {
		t.Fatal("deleting the held session cleared the freeze; only reset-health should")
	}
	skipped := ring.Snapshot(events.Filter{Kind: events.KindScheduleSkipped}, 0)
	if len(skipped) != 1 || !strings.Contains(skipped[0].Message, "reset-health") {
		t.Fatalf("schedule.skipped = %+v, want one naming the freeze and reset-health", skipped)
	}
}
