package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A daily 06:17 schedule with no starting_deadline, so a firing more than a
// minute late is missed.
func pauseClockRole() api.Role {
	return api.Role{Name: "refresh", Replicas: 1, Schedule: &api.SchedulePolicy{
		Cron: "17 6 * * *", Timezone: "UTC",
		Concurrency: api.ScheduleConcurrencyForbid, OnFailure: api.ScheduleOnFailureWait,
	}}
}

// stepAfterPause steps the clock once at now against a status that was waiting
// for the 06:17 firing of 2026-11-04, and returns what it decided.
func stepAfterPause(t *testing.T, c *Controller, now time.Time) (clockStep, api.ScheduleStatus) {
	t.Helper()
	role := pauseClockRole()
	team := &api.Team{Workspace: "ws", Name: "timers"}
	due := time.Date(2026, 11, 4, 6, 17, 0, 0, time.UTC)
	st := api.ScheduleStatus{Key: "ws/timers/refresh", SpecKey: "17 6 * * *|UTC", NextDueAt: due, NextFireAt: due}
	var step clockStep
	if !c.advanceFiring(team, &role, &st, now, nil, false, &step) {
		t.Fatal("the clock changed nothing at a due time")
	}
	return step, st
}

func onlyEvent(t *testing.T, step clockStep, kind events.Kind) events.Event {
	t.Helper()
	var got []events.Event
	for _, ev := range step.events {
		if ev.Kind == kind {
			got = append(got, ev)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d %s events in %+v, want 1", len(got), kind, step.events)
	}
	return got[0]
}

// A firing missed because the cluster was suspended says so: the operator who
// reads schedule.missed after a resume would otherwise take it for a daemon
// that was down.
func TestMissedFiringInsideAFleetSuspendNamesTheSuspend(t *testing.T) {
	_, _, c, cleanup := setup(t)
	t.Cleanup(cleanup)
	since := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 4, 7, 30, 0, 0, time.UTC)
	c.ShiftForPause(now.Sub(since), since, now)

	step, st := stepAfterPause(t, c, now)
	ev := onlyEvent(t, step, events.KindScheduleMissed)
	want := "during a fleet suspend from " + since.Format(time.RFC3339)
	if !strings.Contains(ev.Message, want) {
		t.Errorf("message %q does not contain %q", ev.Message, want)
	}
	if !strings.Contains(ev.Message, "past starting_deadline 1m0s") {
		t.Errorf("message %q lost its starting_deadline clause", ev.Message)
	}
	if st.Firing != "" {
		t.Errorf("a missed firing set Firing %q", st.Firing)
	}
}

// With no suspend on record the message is the one it always was.
func TestMissedFiringWithoutASuspendKeepsItsText(t *testing.T) {
	_, _, c, cleanup := setup(t)
	t.Cleanup(cleanup)
	now := time.Date(2026, 11, 4, 7, 30, 0, 0, time.UTC)

	step, _ := stepAfterPause(t, c, now)
	ev := onlyEvent(t, step, events.KindScheduleMissed)
	if strings.Contains(ev.Message, "suspend") {
		t.Errorf("message %q names a suspend that did not happen", ev.Message)
	}
}

// A due time before the suspend began, or after it ended, is not blamed on it.
func TestMissedFiringOutsideTheSuspendIsNotNamedForIt(t *testing.T) {
	cases := map[string]struct{ since, end time.Time }{
		"suspend began after the due time": {
			time.Date(2026, 11, 4, 6, 30, 0, 0, time.UTC), time.Date(2026, 11, 4, 7, 30, 0, 0, time.UTC),
		},
		"suspend ended before the due time": {
			time.Date(2026, 11, 4, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, ctrl, cleanup := setup(t)
			t.Cleanup(cleanup)
			ctrl.ShiftForPause(c.end.Sub(c.since), c.since, c.end)
			now := time.Date(2026, 11, 4, 7, 30, 0, 0, time.UTC)
			step, _ := stepAfterPause(t, ctrl, now)
			ev := onlyEvent(t, step, events.KindScheduleMissed)
			if strings.Contains(ev.Message, "suspend") {
				t.Errorf("message %q names a suspend the due time was not inside", ev.Message)
			}
		})
	}
}

// Within starting_deadline of the newest due time the firing runs, as a catch-up,
// after a suspend as after any pause; nothing is skipped.
func TestFiringWithinStartingDeadlineStillRunsAfterASuspend(t *testing.T) {
	_, _, c, cleanup := setup(t)
	t.Cleanup(cleanup)
	since := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 4, 6, 17, 30, 0, time.UTC)
	c.ShiftForPause(now.Sub(since), since, now)

	step, st := stepAfterPause(t, c, now)
	if st.Firing == "" {
		t.Errorf("a firing 30s late did not run: %+v", st)
	}
	onlyEvent(t, step, events.KindScheduleFired)
	for _, ev := range step.events {
		if ev.Kind == events.KindScheduleMissed {
			t.Errorf("unexpected schedule.missed: %q", ev.Message)
		}
	}
}
