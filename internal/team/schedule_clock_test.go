package team

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/tmux"
)

// The direct launch path joins Args as shell text, so the script carries
// its own quotes.
func shRuntime(script string) api.Runtime {
	return api.Runtime{Name: "sh", Command: "sh", Args: []string{"-c", "'" + script + "'"}, Mode: api.RuntimeModeHeadless}
}

func clockRole(cron, tz string, rt api.Runtime, mutate func(*api.SchedulePolicy)) api.Role {
	p := &api.SchedulePolicy{
		Cron: cron, Timezone: tz,
		Concurrency: api.ScheduleConcurrencyForbid, OnFailure: api.ScheduleOnFailureWait,
		ActiveDeadline: 45 * time.Minute, StaleAfter: 30 * time.Hour,
		History: &api.ScheduleHistory{Succeeded: 3, Failed: 3},
	}
	if mutate != nil {
		mutate(p)
	}
	return api.Role{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: p}
}

type clockRig struct {
	store *api.Store
	mgr   *session.Manager
	ctrl  *Controller
	clock *testClock
	ring  *events.Ring
	ws    string
}

// newClockRig is a controller on its own store and tmux server, on a fake
// clock shared with the session manager, with no jitter.
func newClockRig(t *testing.T, ws string, start time.Time, role api.Role) *clockRig {
	t.Helper()
	skipIfNoTmux(t)
	store, mgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)
	r := &clockRig{store: store, mgr: mgr, ctrl: ctrl, clock: newTestClock(start), ring: events.NewRing(400), ws: ws}
	ctrl.Events = r.ring
	mgr.Events = r.ring
	ctrl.now = r.clock.Now
	mgr.Now = r.clock.Now
	ctrl.jitter = func(time.Duration) time.Duration { return 0 }
	createTeamFixture(t, store, ws, "timers", []api.Role{role})
	return r
}

func (r *clockRig) sessions() []api.Session {
	return r.store.ListSessionsByTeamRole(r.ws, "timers", "refresh")
}

func (r *clockRig) status() api.ScheduleStatus {
	st, _ := r.store.GetScheduleStatus(r.ws + "/timers/refresh")
	return st
}

func (r *clockRig) kind(k events.Kind) []events.Event {
	return r.ring.Snapshot(events.Filter{Kind: k, Workspace: r.ws}, 0)
}

// settle reconciles on real time, without moving the fake clock, until
// cond holds: panes exit and are reaped in real time.
func (r *clockRig) settle(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r.ctrl.ReconcileOnce()
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: not reached within 15s; sessions %v, status %+v", what, summarize(r.sessions()), r.status())
}

func liveCount(ss []api.Session) int { return api.CountAlive(ss) }

// TestScheduledRoleFiresOncePerFiring is the clock end to end: first
// sight computes the next due time and fires nothing; at the due time the
// firing advances and the reconciler spawns one run stamped with it; a
// succeeded run holds the slot, so nothing respawns; the next firing
// spawns the next run and drops the earlier firing's row.
func TestScheduledRoleFiresOncePerFiring(t *testing.T) {
	r := newClockRig(t, "test-clock-fire", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("17 6 * * *", "Etc/UTC", shRuntime("exit 0"), nil))

	r.ctrl.ReconcileOnce()
	if st := r.status(); !st.NextDueAt.Equal(time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC)) || st.Firing != "" {
		t.Fatalf("after first sight: status %+v, want next due 06:17 and no firing", st)
	}
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("first sight spawned %d sessions", n)
	}

	r.clock.Advance(17*time.Minute + 30*time.Second)
	r.ctrl.ReconcileOnce()
	ss := r.sessions()
	if len(ss) != 1 || ss[0].Firing != "20261001T061700Z" {
		t.Fatalf("at the due time: sessions %+v, want one run of firing 20261001T061700Z", summarize(ss))
	}
	if n := len(r.kind(events.KindScheduleFired)); n != 1 {
		t.Fatalf("schedule.fired events = %d, want 1", n)
	}

	r.settle(t, "the run succeeds", func() bool {
		ss := r.sessions()
		return len(ss) == 1 && ss[0].State == api.SessionSucceeded
	})
	for i := 0; i < 3; i++ {
		r.ctrl.ReconcileOnce()
	}
	if ss := r.sessions(); len(ss) != 1 {
		t.Fatalf("a succeeded run was respawned: %v", summarize(ss))
	}
	if h := r.status().History; len(h) != 1 || h[0].Firing != "20261001T061700Z" || !h[0].DueAt.Equal(time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC)) {
		t.Fatalf("history = %+v, want one run carrying its firing and due time", h)
	}

	r.clock.Advance(24 * time.Hour)
	r.ctrl.ReconcileOnce()
	ss = r.sessions()
	if len(ss) != 1 || ss[0].Firing != "20261002T061700Z" {
		t.Fatalf("next firing: sessions %v, want only the new firing's run", summarize(ss))
	}
}

// TestScheduleOverlap is design section 3's overlap table, with the
// previous firing's run still live at the next due time: forbid skips the
// firing, replace kills the old run (recorded cancelled) and starts the
// new one, allow runs both.
func TestScheduleOverlap(t *testing.T) {
	tests := []struct {
		concurrency string
		wantKind    events.Kind
		wantLive    int
		wantFiring  string
	}{
		{api.ScheduleConcurrencyForbid, events.KindScheduleSkipped, 1, "20261001T061700Z"},
		{api.ScheduleConcurrencyReplace, events.KindScheduleReplaced, 1, "20261002T061700Z"},
		{api.ScheduleConcurrencyAllow, events.KindScheduleFired, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.concurrency, func(t *testing.T) {
			r := newClockRig(t, "test-clock-overlap-"+tc.concurrency, time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
				clockRole("17 6 * * *", "Etc/UTC", shRuntime("sleep 60"), func(p *api.SchedulePolicy) { p.Concurrency = tc.concurrency }))
			r.ctrl.ReconcileOnce()
			r.clock.Advance(17*time.Minute + 30*time.Second)
			r.ctrl.ReconcileOnce()
			if n := liveCount(r.sessions()); n != 1 {
				t.Fatalf("first firing: %d live runs, want 1", n)
			}

			r.clock.Advance(24 * time.Hour)
			r.ctrl.ReconcileOnce()
			ss := r.sessions()
			if n := liveCount(ss); n != tc.wantLive {
				t.Fatalf("live runs after the overlapping firing = %d, want %d: %v", n, tc.wantLive, summarize(ss))
			}
			if tc.wantFiring != "" {
				var live []api.Session
				for _, s := range ss {
					if s.State.CountsAsAlive() {
						live = append(live, s)
					}
				}
				if live[0].Firing != tc.wantFiring {
					t.Fatalf("live run belongs to firing %s, want %s", live[0].Firing, tc.wantFiring)
				}
			}
			evs := r.kind(tc.wantKind)
			if tc.concurrency == api.ScheduleConcurrencyAllow {
				if len(evs) != 2 {
					t.Fatalf("schedule.fired = %d, want 2", len(evs))
				}
			} else if len(evs) != 1 {
				t.Fatalf("%s events = %d, want 1", tc.wantKind, len(evs))
			}
			if tc.concurrency == api.ScheduleConcurrencyForbid && !strings.Contains(evs[0].Message, "overlap") {
				t.Fatalf("skip reason = %q, want overlap", evs[0].Message)
			}
			if tc.concurrency == api.ScheduleConcurrencyReplace {
				h := r.status().History
				if len(h) != 1 || h[0].Outcome != api.RunCancelled || h[0].Firing != "20261001T061700Z" {
					t.Fatalf("history = %+v, want the replaced run recorded cancelled", h)
				}
			}
		})
	}
}

// TestScheduleRecoveryRunsAtMostOnce is design section 3's recovery rows:
// after the daemon was down across several firings, the newest is run
// once, flagged catch-up with the count it coalesced, if it is inside
// starting_deadline; otherwise nothing runs and schedule.missed says how
// many were lost.
func TestScheduleRecoveryRunsAtMostOnce(t *testing.T) {
	start := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	withDeadline := func(p *api.SchedulePolicy) { p.StartingDeadline = 2 * time.Hour }

	t.Run("inside the deadline", func(t *testing.T) {
		r := newClockRig(t, "test-clock-recover", start, clockRole("17 6 * * *", "Etc/UTC", shRuntime("sleep 60"), withDeadline))
		r.ctrl.ReconcileOnce()
		r.clock.Advance(49 * time.Hour) // day 3, 07:00: 43 minutes past the newest due
		r.ctrl.ReconcileOnce()
		r.ctrl.ReconcileOnce()
		ss := r.sessions()
		if len(ss) != 1 || ss[0].Firing != "20261003T061700Z" {
			t.Fatalf("recovery spawned %v, want one run of the newest firing", summarize(ss))
		}
		evs := r.kind(events.KindScheduleFired)
		if len(evs) != 1 || !strings.Contains(evs[0].Message, "catch-up") || !strings.Contains(evs[0].Message, "missed 2") {
			t.Fatalf("schedule.fired = %+v, want one catch-up firing that missed 2", evs)
		}
		if st := r.status(); !st.FiringCatchUp || st.FiringMissed != 2 || !st.NextDueAt.Equal(time.Date(2026, 10, 4, 6, 17, 0, 0, time.UTC)) {
			t.Fatalf("status %+v, want catch-up, missed 2, next due day 4", st)
		}
	})

	t.Run("past the deadline", func(t *testing.T) {
		r := newClockRig(t, "test-clock-missed", start, clockRole("17 6 * * *", "Etc/UTC", shRuntime("sleep 60"), withDeadline))
		r.ctrl.ReconcileOnce()
		r.clock.Advance(51 * time.Hour) // day 3, 09:00: 2h43m past the newest due
		r.ctrl.ReconcileOnce()
		if n := len(r.sessions()); n != 0 {
			t.Fatalf("a firing past starting_deadline spawned %d runs", n)
		}
		evs := r.kind(events.KindScheduleMissed)
		if len(evs) != 1 || !strings.Contains(evs[0].Message, "missed 3") {
			t.Fatalf("schedule.missed = %+v, want one event naming 3 missed firings", evs)
		}
	})
}

// TestFailedRunRetriesThenSettles is the firing record settling failures
// (ADR-010 Amendment 1): a failed run spends an attempt and is retried
// after a backoff while retries remain, then the firing settles and
// nothing more spawns until the next firing. The restart policy is never
// charged: no crash-loop backoff, no restart count.
func TestFailedRunRetriesThenSettles(t *testing.T) {
	r := newClockRig(t, "test-clock-retry", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("17 6 * * *", "Etc/UTC", shRuntime("exit 3"), func(p *api.SchedulePolicy) { p.Retries = 1 }))
	r.ctrl.ReconcileOnce()
	r.clock.Advance(17*time.Minute + 30*time.Second)
	r.ctrl.ReconcileOnce()

	r.settle(t, "the first run fails", func() bool { return len(r.status().History) == 1 })
	st := r.status()
	if st.Attempts != 1 || st.Settled || st.RetryAfter.IsZero() {
		t.Fatalf("after one failure: %+v, want one attempt and a retry pending", st)
	}
	for i := 0; i < 3; i++ {
		r.ctrl.ReconcileOnce()
	}
	if n := liveCount(r.sessions()); n != 0 {
		t.Fatalf("retried before its backoff: %d live", n)
	}

	r.clock.Advance(31 * time.Second)
	r.settle(t, "the retry fails", func() bool { return len(r.status().History) == 2 })
	if st := r.status(); st.Attempts != 2 || !st.Settled {
		t.Fatalf("after the retry: %+v, want two attempts and the firing settled", st)
	}
	r.clock.Advance(10 * time.Minute)
	for i := 0; i < 5; i++ {
		r.ctrl.ReconcileOnce()
	}
	if n := liveCount(r.sessions()); n != 0 {
		t.Fatalf("a settled firing spawned %d runs", n)
	}
	if rh, ok := r.ctrl.RoleHealthSnapshot(r.ws, "timers", "refresh"); ok && (rh.RestartCount != 0 || !rh.BackoffUntil.IsZero()) {
		t.Fatalf("a scheduled role was charged to its restart policy: %+v", rh)
	}
	if n := len(r.kind(events.KindCrashLoopBackoff)); n != 0 {
		t.Fatalf("crash-loop backoff events for a scheduled role: %d", n)
	}
}

// TestOnFailureFreeze: with on_failure = freeze, a failed firing freezes
// the schedule; later firings are skipped until reset-health clears it.
func TestOnFailureFreeze(t *testing.T) {
	r := newClockRig(t, "test-clock-freeze", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("17 6 * * *", "Etc/UTC", shRuntime("exit 3"), func(p *api.SchedulePolicy) { p.OnFailure = api.ScheduleOnFailureFreeze }))
	r.ctrl.ReconcileOnce()
	r.clock.Advance(17*time.Minute + 30*time.Second)
	r.ctrl.ReconcileOnce()
	r.settle(t, "the run fails", func() bool { return r.status().Frozen })
	if n := len(r.kind(events.KindScheduleFrozen)); n != 1 {
		t.Fatalf("schedule.frozen events = %d, want 1", n)
	}

	r.clock.Advance(24 * time.Hour)
	r.ctrl.ReconcileOnce()
	if n := liveCount(r.sessions()); n != 0 {
		t.Fatalf("a frozen schedule spawned %d runs", n)
	}
	skipped := r.kind(events.KindScheduleSkipped)
	if len(skipped) != 1 || !strings.Contains(skipped[0].Message, "frozen") {
		t.Fatalf("schedule.skipped = %+v, want one naming the freeze", skipped)
	}

	r.ctrl.ClearRoleHealthForRole(r.ws, "timers", "refresh")
	if r.status().Frozen {
		t.Fatal("reset-health did not clear the schedule's freeze")
	}
	r.clock.Advance(24 * time.Hour)
	r.ctrl.ReconcileOnce()
	if n := len(r.sessions()); n == 0 {
		t.Fatal("the next firing after reset-health spawned nothing")
	}
}

// TestPostureHoldSkipsAFiring: a team held at the start line with nothing
// live spawns nothing cold (the convergence-posture money guard), so a
// firing that comes due then is recorded skipped, not run.
func TestPostureHoldSkipsAFiring(t *testing.T) {
	r := newClockRig(t, "test-clock-posture", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("17 6 * * *", "Etc/UTC", shRuntime("exit 0"), nil))
	if err := r.store.UpdateTeam(r.ws+"/timers", func(tm *api.Team) error {
		tm.ConvergencePosture = api.PostureHold
		return nil
	}); err != nil {
		t.Fatalf("hold the team: %v", err)
	}
	r.ctrl.ReconcileOnce()
	r.clock.Advance(17*time.Minute + 30*time.Second)
	r.ctrl.ReconcileOnce()
	r.ctrl.ReconcileOnce()
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("a held team spawned %d runs", n)
	}
	skipped := r.kind(events.KindScheduleSkipped)
	if len(skipped) != 1 || !strings.Contains(skipped[0].Message, "posture") {
		t.Fatalf("schedule.skipped = %+v, want one naming the posture", skipped)
	}
}

// TestSuspendedScheduleDoesNotFire: suspend = true fires nothing and says
// so once.
func TestSuspendedScheduleDoesNotFire(t *testing.T) {
	r := newClockRig(t, "test-clock-suspend", time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		clockRole("17 6 * * *", "Etc/UTC", shRuntime("exit 0"), func(p *api.SchedulePolicy) { p.Suspend = true }))
	for i := 0; i < 3; i++ {
		r.ctrl.ReconcileOnce()
		r.clock.Advance(20 * time.Minute)
	}
	if n := len(r.sessions()); n != 0 {
		t.Fatalf("a suspended schedule spawned %d runs", n)
	}
	if n := len(r.kind(events.KindScheduleSuspended)); n != 1 {
		t.Fatalf("schedule.suspended events = %d, want 1", n)
	}
}

// TestScheduleDSTEvents is design 2a's runtime events: computing a next
// due time across an offset change emits schedule.dst-shift, and a zone
// that observes daylight saving without dst_ack (a tzdata change after
// apply) emits schedule.dst-unacknowledged once per computation and keeps
// firing.
func TestScheduleDSTEvents(t *testing.T) {
	eve := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) // CDT; the next 06:17 is CST

	t.Run("shift", func(t *testing.T) {
		r := newClockRig(t, "test-clock-dst-shift", eve,
			clockRole("17 6 * * *", "America/Chicago", shRuntime("exit 0"), func(p *api.SchedulePolicy) { p.DSTAck = true }))
		r.ctrl.ReconcileOnce()
		evs := r.kind(events.KindScheduleDSTShift)
		if len(evs) != 1 || !strings.Contains(evs[0].Message, "-05:00") || !strings.Contains(evs[0].Message, "-06:00") {
			t.Fatalf("schedule.dst-shift = %+v, want one naming -05:00 and -06:00", evs)
		}
		if n := len(r.kind(events.KindScheduleDSTUnacknowledged)); n != 0 {
			t.Fatalf("an acknowledged zone emitted %d dst-unacknowledged", n)
		}
	})

	t.Run("unacknowledged", func(t *testing.T) {
		r := newClockRig(t, "test-clock-dst-unack", eve,
			clockRole("17 6 * * *", "America/Chicago", shRuntime("exit 0"), nil))
		r.ctrl.ReconcileOnce()
		r.ctrl.ReconcileOnce()
		if n := len(r.kind(events.KindScheduleDSTUnacknowledged)); n != 1 {
			t.Fatalf("schedule.dst-unacknowledged = %d, want 1 for one computation", n)
		}
		if r.status().NextDueAt.IsZero() {
			t.Fatal("an unacknowledged zone stopped the schedule")
		}
	})
}

// TestNextDueSurvivesARestart is design 2a test 7: next-due lives in the
// store, so a daemon restart between firings recomputes nothing and fires
// nothing early.
func TestNextDueSurvivesARestart(t *testing.T) {
	skipIfNoTmux(t)
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	start := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	role := clockRole("17 6 * * *", "Etc/UTC", shRuntime("exit 0"), nil)
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	open := func() (*api.Store, *Controller) {
		s := api.NewStore()
		if err := s.OpenBolt(path); err != nil {
			t.Fatalf("OpenBolt: %v", err)
		}
		c := NewController(s, session.NewManager(s, driver))
		c.jitter = func(time.Duration) time.Duration { return 0 }
		return s, c
	}
	s1, c1 := open()
	c1.now = func() time.Time { return start }
	createTeamFixture(t, s1, "test-clock-restart", "timers", []api.Role{role})
	c1.ReconcileOnce()
	before, _ := s1.GetScheduleStatus("test-clock-restart/timers/refresh")
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	s2, c2 := open()
	t.Cleanup(func() { _ = s2.CloseBolt() })
	c2.now = func() time.Time { return start.Add(10 * time.Minute) }
	c2.ReconcileOnce()
	after, _ := s2.GetScheduleStatus("test-clock-restart/timers/refresh")
	if before.NextDueAt.IsZero() || !after.NextDueAt.Equal(before.NextDueAt) || after.Firing != "" {
		t.Fatalf("next due before %v, after restart %v (firing %q): want it kept and nothing fired", before.NextDueAt, after.NextDueAt, after.Firing)
	}
}
