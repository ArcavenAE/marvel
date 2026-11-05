package team

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// deadlineRig is a scheduled role whose run never ends by itself, with the
// firing started and one live run. clockRole's active_deadline is 45m.
func deadlineRig(t *testing.T, ws string, mutate func(*api.SchedulePolicy)) *clockRig {
	t.Helper()
	r := newClockRig(t, ws, time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("Etc/UTC", shRuntime("sleep 600"), mutate))
	r.ctrl.ReconcileOnce()
	r.clock.Advance(17*time.Minute + 30*time.Second)
	r.ctrl.ReconcileOnce()
	if n := liveCount(r.sessions()); n != 1 {
		t.Fatalf("the firing started %d runs, want 1; sessions %v", n, summarize(r.sessions()))
	}
	return r
}

// A run still live when active_deadline passes is killed and recorded as a
// failed run, so it spends an attempt and follows retries and on_failure
// (scheduled-runs section 2, the failed-run rule). The kill must not take
// the operator-kill path, which records cancelled and settles the firing
// with no attempt.
//
// The policy here is the default one: retries 0, on_failure wait, concurrency forbid.
func TestRunPastActiveDeadlineIsKilledAndFails(t *testing.T) {
	r := deadlineRig(t, "test-deadline-zero", nil)

	r.clock.Advance(44 * time.Minute)
	r.ctrl.ReconcileOnce()
	if n := liveCount(r.sessions()); n != 1 {
		t.Fatalf("a run inside active_deadline was killed: %d live", n)
	}
	if n := len(r.kind(events.KindRunDeadline)); n != 0 {
		t.Fatalf("run.deadline before the deadline: %d", n)
	}

	r.clock.Advance(2 * time.Minute)
	r.ctrl.ReconcileOnce()
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("the run past its deadline is still here: %v", summarize(r.sessions()))
	}
	st := r.status()
	if len(st.History) != 1 || st.History[0].Outcome != api.RunFailed {
		t.Fatalf("history %+v, want one failed run", st.History)
	}
	if st.Attempts != 1 || !st.Settled {
		t.Fatalf("firing %+v, want one attempt and the firing settled", st)
	}
	if n := len(r.kind(events.KindRunDeadline)); n != 1 {
		t.Fatalf("run.deadline events = %d, want 1", n)
	}
	if n := len(r.kind(events.KindRunCancelled)); n != 0 {
		t.Fatalf("run.cancelled events = %d, want 0", n)
	}
	for i := 0; i < 3; i++ {
		r.ctrl.ReconcileOnce()
	}
	if n := len(r.kind(events.KindRunDeadline)); n != 1 {
		t.Fatalf("run.deadline events after more ticks = %d, want 1", n)
	}

	// The next firing spawns; the slot is free and nothing is skipped as
	// overlap.
	r.clock.Advance(23*time.Hour + 14*time.Minute) // 06:17:30 the next day
	r.ctrl.ReconcileOnce()
	if n := liveCount(r.sessions()); n != 1 {
		t.Fatalf("the next firing started %d runs, want 1", n)
	}
	for _, e := range r.kind(events.KindScheduleSkipped) {
		t.Fatalf("a firing was skipped: %s", e.Message)
	}
}

// With a retry left and the firing inside starting_deadline, a deadline
// kill schedules a retry after backoff, and the second kill settles.
func TestDeadlineKillSpendsARetry(t *testing.T) {
	r := deadlineRig(t, "test-deadline-retry", func(p *api.SchedulePolicy) {
		p.Retries = 1
		p.StartingDeadline = 3 * time.Hour
	})

	r.clock.Advance(46 * time.Minute)
	r.ctrl.ReconcileOnce()
	st := r.status()
	if st.Attempts != 1 || st.Settled || st.RetryAfter.IsZero() {
		t.Fatalf("after the first kill: %+v, want one attempt and a retry pending", st)
	}
	if n := liveCount(r.sessions()); n != 0 {
		t.Fatalf("retried before its backoff: %d live", n)
	}

	r.clock.Advance(31 * time.Second)
	r.ctrl.ReconcileOnce()
	if n := liveCount(r.sessions()); n != 1 {
		t.Fatalf("the retry started %d runs, want 1", n)
	}
	r.clock.Advance(46 * time.Minute)
	r.ctrl.ReconcileOnce()
	st = r.status()
	if st.Attempts != 2 || !st.Settled {
		t.Fatalf("after the second kill: %+v, want two attempts and the firing settled", st)
	}
	if n := len(r.kind(events.KindRunDeadline)); n != 2 {
		t.Fatalf("run.deadline events = %d, want 2", n)
	}
	if n := len(r.kind(events.KindRunCancelled)); n != 0 {
		t.Fatalf("run.cancelled events = %d, want 0", n)
	}
}

// Under on_failure = freeze, a deadline kill with no retries left freezes
// the schedule, which a cancelled record never does.
func TestDeadlineKillFreezesUnderOnFailureFreeze(t *testing.T) {
	r := deadlineRig(t, "test-deadline-freeze", func(p *api.SchedulePolicy) {
		p.OnFailure = api.ScheduleOnFailureFreeze
	})

	r.clock.Advance(46 * time.Minute)
	r.ctrl.ReconcileOnce()
	if !r.status().Frozen {
		t.Fatalf("the deadline kill did not freeze the schedule: %+v", r.status())
	}
	if n := len(r.kind(events.KindScheduleFrozen)); n != 1 {
		t.Fatalf("schedule.frozen events = %d, want 1", n)
	}
}

// Control: an operator's own delete of a live scheduled run stays a
// cancelled run, with no attempt, however old the run is.
func TestOperatorDeleteOfALiveRunStaysCancelled(t *testing.T) {
	r := deadlineRig(t, "test-deadline-control", nil)

	live := r.sessions()
	if err := r.mgr.Delete(live[0].Key()); err != nil {
		t.Fatal(err)
	}
	st := r.status()
	if len(st.History) != 1 || st.History[0].Outcome != api.RunCancelled {
		t.Fatalf("history %+v, want one cancelled run", st.History)
	}
	if st.Attempts != 0 || !st.Settled {
		t.Fatalf("firing %+v, want no attempt and the firing settled", st)
	}
	if n := len(r.kind(events.KindRunCancelled)); n != 1 {
		t.Fatalf("run.cancelled events = %d, want 1", n)
	}
	if n := len(r.kind(events.KindRunDeadline)); n != 0 {
		t.Fatalf("run.deadline events = %d, want 0", n)
	}
	for _, e := range r.kind(events.KindRunDeadline) {
		if strings.Contains(e.Message, "cancel") {
			t.Fatalf("unexpected message %q", e.Message)
		}
	}
}
