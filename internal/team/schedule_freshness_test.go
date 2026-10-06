package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// TestScheduleStaleOncePerTransition is design section 7 on the
// reconciler: a scheduled role with no succeeded run newer than
// stale_after raises schedule.stale once, not once per tick, and
// schedule.fresh once when a succeeded run brings it back (the operator's
// ruling on #431 default 2). With no run at all, the bound runs
// from when the reconciler first saw the schedule. The role stays held
// throughout: the alarm fires nothing.
func TestScheduleStaleOncePerTransition(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(64)
	ctrl.Events = ring
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	clock := newTestClock(start)
	ctrl.now = clock.Now

	const ws = "test-sched-stale"
	createTeamFixture(t, store, ws, "timers", []api.Role{scheduledRole(1)})
	key := ws + "/timers/refresh"
	stale := func() []events.Event {
		return ring.Snapshot(events.Filter{Kind: events.KindScheduleStale, Workspace: ws}, 0)
	}

	ctrl.ReconcileOnce()
	st, ok := store.GetScheduleStatus(key)
	if !ok || !st.Since.Equal(start) {
		t.Fatalf("status after the first tick = %+v (found %v), want since %v", st, ok, start)
	}
	if n := len(stale()); n != 0 {
		t.Fatalf("schedule.stale events on the first tick = %d, want 0", n)
	}

	clock.Advance(30*time.Hour + time.Minute)
	for i := 0; i < 3; i++ {
		ctrl.ReconcileOnce()
		clock.Advance(time.Minute)
	}
	got := stale()
	if len(got) != 1 || got[0].Severity != events.SeverityWarning || got[0].Role != "refresh" {
		t.Fatalf("schedule.stale after stale_after = %+v, want one warning for refresh", got)
	}
	if st, _ := store.GetScheduleStatus(key); !st.Stale {
		t.Fatal("the stored status does not record the stale state")
	}

	if _, err := store.UpdateScheduleStatus(key, func(st *api.ScheduleStatus) bool {
		st.AddRun(api.RunRecord{
			Session: ws + "/timers-refresh-g1-0", Outcome: api.RunSucceeded,
			StartedAt: clock.Now().Add(-time.Minute), EndedAt: clock.Now(),
		}, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	}); err != nil {
		t.Fatalf("record a run: %v", err)
	}
	ctrl.ReconcileOnce()
	ctrl.ReconcileOnce()
	if got = stale(); len(got) != 1 {
		t.Fatalf("schedule.stale after a success = %+v, want still only the one stale event", got)
	}
	fresh := ring.Snapshot(events.Filter{Kind: events.KindScheduleFresh, Workspace: ws}, 0)
	if len(fresh) != 1 || fresh[0].Severity != events.SeverityInfo || fresh[0].Role != "refresh" {
		t.Fatalf("schedule.fresh after a success = %+v, want one info event for refresh", fresh)
	}
	if n := len(store.ListSessionsByTeamRole(ws, "timers", "refresh")); n != 0 {
		t.Fatalf("the alarm path spawned %d sessions for a held role", n)
	}
}

// TestScheduleStatusFollowsTheRole: when the role loses its schedule (or
// the role or team goes), the reconciler drops the role's schedule
// status, so a later schedule under the same name starts fresh.
func TestScheduleStatusFollowsTheRole(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ctrl.now = newTestClock(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)).Now

	const ws = "test-sched-follow"
	createTeamFixture(t, store, ws, "timers", []api.Role{scheduledRole(0)})
	key := ws + "/timers/refresh"
	ctrl.ReconcileOnce()
	if _, ok := store.GetScheduleStatus(key); !ok {
		t.Fatal("no status for a scheduled role after a tick")
	}

	if err := store.UpdateTeam(ws+"/timers", func(tm *api.Team) error {
		tm.Roles[0].Schedule = nil
		tm.Roles[0].Replicas = 0
		return nil
	}); err != nil {
		t.Fatalf("drop the schedule: %v", err)
	}
	ctrl.ReconcileOnce()
	if _, ok := store.GetScheduleStatus(key); ok {
		t.Fatal("status outlived the role's schedule")
	}
}

// TestScheduleRecoversWhenStaleAfterIsRaised: a stale schedule that never
// succeeded recovers when the operator raises stale_after, and the event
// says so rather than naming a success that never happened.
func TestScheduleRecoversWhenStaleAfterIsRaised(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	ring := events.NewRing(64)
	ctrl.Events = ring
	clock := newTestClock(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	ctrl.now = clock.Now

	const ws = "test-sched-raised"
	createTeamFixture(t, store, ws, "timers", []api.Role{scheduledRole(0)})
	ctrl.ReconcileOnce()
	clock.Advance(31 * time.Hour)
	ctrl.ReconcileOnce()

	if err := store.UpdateTeam(ws+"/timers", func(tm *api.Team) error {
		tm.Roles[0].Schedule.StaleAfter = 60 * time.Hour
		return nil
	}); err != nil {
		t.Fatalf("raise stale_after: %v", err)
	}
	ctrl.ReconcileOnce()

	if got := ring.Snapshot(events.Filter{Kind: events.KindScheduleStale, Workspace: ws}, 0); len(got) != 1 {
		t.Fatalf("schedule.stale = %+v, want the one stale event", got)
	}
	got := ring.Snapshot(events.Filter{Kind: events.KindScheduleFresh, Workspace: ws}, 0)
	if len(got) != 1 || got[0].Severity != events.SeverityInfo {
		t.Fatalf("schedule.fresh = %+v, want one recovery event at info", got)
	}
	if msg := got[0].Message; !strings.Contains(msg, "stale_after raised to 60h0m0s") || strings.Contains(msg, "0001-01-01") {
		t.Fatalf("recovery message = %q, want it to name the raised stale_after and no zero time", msg)
	}
}
