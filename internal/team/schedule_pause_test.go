package team

import (
	"reflect"
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
	if err := c.ShiftForPause(now.Sub(since), since, now); err != nil {
		t.Fatal(err)
	}

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
			if err := ctrl.ShiftForPause(c.end.Sub(c.since), c.since, c.end); err != nil {
				t.Fatal(err)
			}
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
	if err := c.ShiftForPause(now.Sub(since), since, now); err != nil {
		t.Fatal(err)
	}

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

// staleRig is a controller on a fixed clock with one scheduled role (stale
// after 30h) whose last success was at t0, and a suspend that began an hour
// later and ended at now.
type staleRig struct {
	ctrl  *Controller
	store *api.Store
	ring  *events.Ring
	key   string
	t0    time.Time
	since time.Time
	now   time.Time
}

func newStaleRig(t *testing.T, ws string, gap, pause time.Duration) *staleRig {
	t.Helper()
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	r := &staleRig{ctrl: ctrl, store: store, ring: events.NewRing(64), key: ws + "/timers/refresh"}
	r.t0 = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	r.now = r.t0.Add(gap)
	r.since = r.now.Add(-pause)
	ctrl.Events = r.ring
	ctrl.now = func() time.Time { return r.now }
	createTeamFixture(t, store, ws, "timers", []api.Role{scheduledRole(1)})
	if _, err := store.UpdateScheduleStatus(r.key, func(st *api.ScheduleStatus) bool {
		st.Since = r.t0.Add(-time.Hour)
		st.AddRun(api.RunRecord{
			Session: ws + "/timers-refresh-g1-0", Outcome: api.RunSucceeded,
			StartedAt: r.t0.Add(-time.Minute), EndedAt: r.t0,
		}, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *staleRig) shift(t *testing.T) {
	t.Helper()
	if err := r.ctrl.ShiftForPause(r.now.Sub(r.since), r.since, r.now); err != nil {
		t.Fatal(err)
	}
}

func (r *staleRig) freshness() {
	r.ctrl.reconcileScheduleFreshness(r.store.ListTeams())
}

func (r *staleRig) status() api.ScheduleStatus {
	st, _ := r.store.GetScheduleStatus(r.key)
	return st
}

// A pause longer than stale_after must not raise the stale alarm on the first
// tick after resume; the cluster did no work, so nothing was late.
func TestAPauseLongerThanStaleAfterDoesNotRaiseTheAlarm(t *testing.T) {
	r := newStaleRig(t, "test-pause-stale", 41*time.Hour, 40*time.Hour)
	before := r.status()
	r.shift(t)
	r.freshness()

	st := r.status()
	if st.Stale {
		t.Error("the schedule read stale on the first tick after a 40h pause")
	}
	if n := len(r.ring.Snapshot(events.Filter{Kind: events.KindScheduleStale}, 0)); n != 0 {
		t.Errorf("%d schedule.stale events, want 0", n)
	}
	if st.PausedSinceAnchor != 40*time.Hour {
		t.Errorf("PausedSinceAnchor = %v, want 40h", st.PausedSinceAnchor)
	}
	// No recorded stamp moves: these are facts the status and the history report.
	if !st.LastSucceededAt.Equal(before.LastSucceededAt) || !st.Since.Equal(before.Since) || !st.FiringDueAt.Equal(before.FiringDueAt) {
		t.Errorf("a recorded stamp moved: before %+v after %+v", before, st)
	}
}

// A schedule that is stale even after the pause is taken out still alarms, and
// the message says how much was left out.
func TestStaleAfterAPauseNamesWhatWasExcluded(t *testing.T) {
	r := newStaleRig(t, "test-pause-stale-msg", 50*time.Hour, 10*time.Hour)
	r.shift(t)
	r.freshness()
	if !r.status().Stale {
		t.Fatal("40h without a success against a 30h limit read fresh")
	}
	evs := r.ring.Snapshot(events.Filter{Kind: events.KindScheduleStale}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "(paused 10h0m0s excluded)") {
		t.Errorf("stale events %+v, want one naming the 10h pause", evs)
	}
}

// With no pause the stale alarm and its message are as they were.
func TestStaleWithoutAPauseIsUnchanged(t *testing.T) {
	r := newStaleRig(t, "test-nopause-stale", 41*time.Hour, 0)
	r.freshness()
	if !r.status().Stale {
		t.Fatal("41h without a success against a 30h limit read fresh")
	}
	evs := r.ring.Snapshot(events.Filter{Kind: events.KindScheduleStale}, 0)
	if len(evs) != 1 || strings.Contains(evs[0].Message, "paused") {
		t.Errorf("stale events %+v, want one with no mention of a pause", evs)
	}
}

// The same suspend applied twice counts once; a later suspend adds its own.
func TestPauseOnAScheduleIsCountedOncePerSuspend(t *testing.T) {
	r := newStaleRig(t, "test-pause-once", 41*time.Hour, 10*time.Hour)
	r.shift(t)
	r.shift(t)
	if got := r.status().PausedSinceAnchor; got != 10*time.Hour {
		t.Fatalf("PausedSinceAnchor after a repeated hook = %v, want 10h", got)
	}
	if err := r.ctrl.ShiftForPause(2*time.Hour, r.now.Add(-2*time.Hour), r.now); err != nil {
		t.Fatal(err)
	}
	if got := r.status().PausedSinceAnchor; got != 12*time.Hour {
		t.Errorf("PausedSinceAnchor after a second suspend = %v, want 12h", got)
	}
}

// A success after the pause starts the count over, so a pause before the last
// success is never counted against a later gap.
func TestASuccessAfterThePauseResetsTheCount(t *testing.T) {
	r := newStaleRig(t, "test-pause-reset", 41*time.Hour, 10*time.Hour)
	r.shift(t)
	if _, err := r.store.UpdateScheduleStatus(r.key, func(st *api.ScheduleStatus) bool {
		st.AddRun(api.RunRecord{Outcome: api.RunSucceeded, StartedAt: r.now, EndedAt: r.now.Add(time.Minute)}, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if got := r.status().PausedSinceAnchor; got != 0 {
		t.Errorf("PausedSinceAnchor after a success = %v, want 0", got)
	}
	// A failure does not move the anchor, so it does not reset the count.
	r.shift(t)
	if _, err := r.store.UpdateScheduleStatus(r.key, func(st *api.ScheduleStatus) bool {
		st.PausedSinceAnchor = time.Hour
		st.AddRun(api.RunRecord{Outcome: api.RunFailed, StartedAt: r.now, EndedAt: r.now.Add(time.Minute)}, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if got := r.status().PausedSinceAnchor; got != time.Hour {
		t.Errorf("PausedSinceAnchor after a failed run = %v, want it kept at 1h", got)
	}
}

// A retry wait that was still ahead when the suspend began moves with the
// pause; one that had already passed stays.
func TestRetryAfterMovesOnlyWhenItWasAheadOfTheSuspend(t *testing.T) {
	r := newStaleRig(t, "test-pause-retry", 41*time.Hour, 10*time.Hour)
	ahead := r.since.Add(5 * time.Minute)
	passed := r.since.Add(-5 * time.Minute)
	for _, c := range []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"test-pause-retry", ahead, ahead.Add(10 * time.Hour)},
	} {
		if _, err := r.store.UpdateScheduleStatus(r.key, func(st *api.ScheduleStatus) bool {
			st.RetryAfter = c.at
			return true
		}); err != nil {
			t.Fatal(err)
		}
		r.shift(t)
		if got := r.status().RetryAfter; !got.Equal(c.want) {
			t.Errorf("%s: RetryAfter = %v, want %v", c.name, got, c.want)
		}
	}

	r2 := newStaleRig(t, "test-pause-retry-passed", 41*time.Hour, 10*time.Hour)
	if _, err := r2.store.UpdateScheduleStatus(r2.key, func(st *api.ScheduleStatus) bool {
		st.RetryAfter = passed
		return true
	}); err != nil {
		t.Fatal(err)
	}
	r2.shift(t)
	if got := r2.status().RetryAfter; !got.Equal(passed) {
		t.Errorf("a RetryAfter before the suspend moved to %v, want it at %v", got, passed)
	}
}

// A cadence status holds no last-seen stamp or alarm, and its clock is not
// changed here, so a resume leaves it exactly as it was.
func TestCadenceStatusIsUnchangedAcrossAResume(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	const key = "ws/timers/refresh/keep"
	if _, err := store.UpdateCadenceStatus(key, func(st *api.CadenceStatus) bool {
		st.SpecKey = "0 * * * *|UTC"
		st.NextDueAt = now.Add(-time.Hour)
		st.AddSlot(api.CadenceSlot{ID: "s1", DueAt: now.Add(-2 * time.Hour), State: api.CadenceSlotOpen, RecordedAt: now.Add(-2 * time.Hour)}, 10)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetCadenceStatus(key)
	if err := ctrl.ShiftForPause(90*time.Minute, now.Add(-90*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetCadenceStatus(key)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a cadence status changed across a resume:\nbefore %+v\nafter  %+v", before, after)
	}
}
