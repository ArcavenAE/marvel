package api

import (
	"testing"
	"time"
)

// chicago is America/Chicago, the zone the daylight-saving tests use.
func chicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("zone America/Chicago not available: %v", err)
	}
	return loc
}

// firings returns the first n firings strictly after from.
func firings(t *testing.T, p SchedulePolicy, from time.Time, n int) []time.Time {
	t.Helper()
	out := make([]time.Time, 0, n)
	at := from
	for i := 0; i < n; i++ {
		next, err := p.NextFiring(at)
		if err != nil {
			t.Fatalf("NextFiring(%v): %v", at, err)
		}
		if !next.After(at) {
			t.Fatalf("NextFiring(%v) = %v, not after", at, next)
		}
		out = append(out, next)
		at = next
	}
	return out
}

func wantTimes(t *testing.T, got []time.Time, loc *time.Location, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d firings, want %d", len(got), len(want))
	}
	for i, w := range want {
		if s := got[i].In(loc).Format("2006-01-02 15:04 MST"); s != w {
			t.Errorf("firing %d = %s, want %s", i, s, w)
		}
	}
}

// TestNextFiringMatchesTheCron: field matching, steps and lists, the
// day-of-week 7 alias, Vixie's either-day rule when both day fields are
// restricted, and a fixed offset.
func TestNextFiringMatchesTheCron(t *testing.T) {
	t.Parallel()
	utc := time.UTC
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, utc) // a Thursday
	tests := []struct {
		name string
		cron string
		tz   string
		n    int
		want []string
	}{
		{"daily", "17 6 * * *", "Etc/UTC", 2, []string{"2026-10-01 06:17 UTC", "2026-10-02 06:17 UTC"}},
		{"weekday interval", "*/20 17 * * 5", "Etc/UTC", 3, []string{"2026-10-02 17:00 UTC", "2026-10-02 17:20 UTC", "2026-10-02 17:40 UTC"}},
		{"list and range step", "0 9-13/2,20 1 * *", "Etc/UTC", 5, []string{"2026-10-01 09:00 UTC", "2026-10-01 11:00 UTC", "2026-10-01 13:00 UTC", "2026-10-01 20:00 UTC", "2026-11-01 09:00 UTC"}},
		{"either day when both are restricted", "0 0 3 * 1", "Etc/UTC", 3, []string{"2026-10-03 00:00 UTC", "2026-10-05 00:00 UTC", "2026-10-12 00:00 UTC"}},
		{"day of week 7 is sunday", "0 12 * * 7", "Etc/UTC", 1, []string{"2026-10-04 12:00 UTC"}},
		{"february 29 skips to a leap year", "0 0 29 2 *", "Etc/UTC", 1, []string{"2028-02-29 00:00 UTC"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := SchedulePolicy{Cron: tc.cron, Timezone: tc.tz}
			wantTimes(t, firings(t, p, from, tc.n), utc, tc.want...)
		})
	}

	p := SchedulePolicy{Cron: "17 6 * * *", Timezone: "-06:00"}
	got := firings(t, p, from, 1)
	if want := time.Date(2026, 10, 1, 12, 17, 0, 0, utc); !got[0].Equal(want) {
		t.Fatalf("fixed -06:00 firing = %v, want %v", got[0], want)
	}
}

// TestNextFiringSpringGap is design 2a test 5 and the carried review note
// 2: a firing time inside the spring-forward gap fires once, at the first
// minute after the gap, and several gap firings coalesce with a genuine
// firing at that minute into one.
func TestNextFiringSpringGap(t *testing.T) {
	t.Parallel()
	chi := chicago(t)
	eve := time.Date(2027, 3, 13, 12, 0, 0, 0, chi)

	daily := SchedulePolicy{Cron: "30 2 * * *", Timezone: "America/Chicago", DSTAck: true}
	wantTimes(t, firings(t, daily, eve, 2), chi, "2027-03-14 03:00 CDT", "2027-03-15 02:30 CDT")

	twice := SchedulePolicy{Cron: "0,30 2,3 * * *", Timezone: "America/Chicago", DSTAck: true}
	wantTimes(t, firings(t, twice, time.Date(2027, 3, 14, 1, 0, 0, 0, chi), 2), chi,
		"2027-03-14 03:00 CDT", "2027-03-14 03:30 CDT")

	interval := SchedulePolicy{Cron: "*/15 * * * *", Timezone: "America/Chicago", DSTAck: true}
	wantTimes(t, firings(t, interval, time.Date(2027, 3, 14, 1, 40, 0, 0, chi), 3), chi,
		"2027-03-14 01:45 CST", "2027-03-14 03:00 CDT", "2027-03-14 03:15 CDT")
}

// TestNextFiringFallOverlap is design 2a test 6 and the carried review
// note 1 (Vixie's rule): a fixed-time job fires once, at the first
// occurrence of the repeated hour; a wildcard or interval job runs on real
// elapsed time, so it fires in both occurrences.
func TestNextFiringFallOverlap(t *testing.T) {
	t.Parallel()
	chi := chicago(t)
	eve := time.Date(2026, 10, 31, 12, 0, 0, 0, chi)

	fixed := SchedulePolicy{Cron: "30 1 * * *", Timezone: "America/Chicago", DSTAck: true}
	wantTimes(t, firings(t, fixed, eve, 2), chi, "2026-11-01 01:30 CDT", "2026-11-02 01:30 CST")

	hourly := SchedulePolicy{Cron: "0 * * * *", Timezone: "America/Chicago", DSTAck: true}
	wantTimes(t, firings(t, hourly, time.Date(2026, 10, 31, 23, 30, 0, 0, chi), 4), chi,
		"2026-11-01 00:00 CDT", "2026-11-01 01:00 CDT", "2026-11-01 01:00 CST", "2026-11-01 02:00 CST")

	half := SchedulePolicy{Cron: "*/30 * * * *", Timezone: "America/Chicago", DSTAck: true}
	wantTimes(t, firings(t, half, time.Date(2026, 11, 1, 0, 45, 0, 0, chi), 5), chi,
		"2026-11-01 01:00 CDT", "2026-11-01 01:30 CDT", "2026-11-01 01:00 CST", "2026-11-01 01:30 CST", "2026-11-01 02:00 CST")
}

// forwardWalk is the reference recovery walk: one NextFiring per due time.
// It is the oracle CatchUp is checked against, and too slow for the clock
// itself (scheduled-runs review on #650, finding 1).
func forwardWalk(t *testing.T, p SchedulePolicy, after, now time.Time) (time.Time, int) {
	t.Helper()
	newest, n := after, 0
	for {
		next, err := p.NextFiring(newest)
		if err != nil {
			t.Fatalf("NextFiring(%v): %v", newest, err)
		}
		if next.After(now) {
			return newest, n
		}
		newest, n = next, n+1
	}
}

// TestCatchUpMatchesTheForwardWalk: CatchUp finds the same newest due time
// and the same count as stepping NextFiring forward, across both
// daylight-saving edges, either-day matching, a day boundary on each side
// and an empty span.
func TestCatchUpMatchesTheForwardWalk(t *testing.T) {
	t.Parallel()
	chi := chicago(t)
	tests := []struct {
		name       string
		cron       string
		tz         string
		after, now time.Time
	}{
		{"minutely across the spring gap", "* * * * *", "America/Chicago", time.Date(2027, 3, 13, 22, 0, 0, 0, chi), time.Date(2027, 3, 14, 5, 0, 0, 0, chi)},
		{"minutely across the fall overlap", "* * * * *", "America/Chicago", time.Date(2026, 10, 31, 22, 0, 0, 0, chi), time.Date(2026, 11, 1, 4, 30, 0, 0, chi)},
		{"interval in the spring gap", "*/15 * * * *", "America/Chicago", time.Date(2027, 3, 14, 1, 40, 0, 0, chi), time.Date(2027, 3, 14, 3, 20, 0, 0, chi)},
		{"fixed times in the spring gap", "0,30 2,3 * * *", "America/Chicago", time.Date(2027, 3, 13, 1, 0, 0, 0, chi), time.Date(2027, 3, 16, 1, 0, 0, 0, chi)},
		{"fixed time in the fall overlap", "30 1 * * *", "America/Chicago", time.Date(2026, 10, 30, 12, 0, 0, 0, chi), time.Date(2026, 11, 3, 12, 0, 0, 0, chi)},
		{"daily over three weeks", "17 6 * * *", "Etc/UTC", time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC), time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)},
		{"either day", "0 0 3 * 1", "Etc/UTC", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC)},
		{"now is exactly a due time", "17 6 * * *", "Etc/UTC", time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC), time.Date(2026, 10, 3, 6, 17, 0, 0, time.UTC)},
		{"nothing missed", "17 6 * * *", "Etc/UTC", time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC), time.Date(2026, 10, 1, 6, 20, 0, 0, time.UTC)},
		{"fixed offset", "*/20 17 * * 5", "-06:00", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := SchedulePolicy{Cron: tc.cron, Timezone: tc.tz, DSTAck: true}
			wantNewest, wantMissed := forwardWalk(t, p, tc.after, tc.now)
			newest, missed, err := p.CatchUp(tc.after, tc.now)
			if err != nil {
				t.Fatalf("CatchUp: %v", err)
			}
			if !newest.Equal(wantNewest) || missed != wantMissed {
				t.Fatalf("CatchUp = %v, %d; the forward walk gives %v, %d", newest, missed, wantNewest, wantMissed)
			}
		})
	}
}

// TestCatchUpOverALongOutage: a minutely job runs on elapsed time through
// both offset changes, so six months down misses exactly the elapsed
// minutes, and the newest due time is now. The forward walk would take
// minutes here; CatchUp answers from the span, not from each due time.
func TestCatchUpOverALongOutage(t *testing.T) {
	t.Parallel()
	chi := chicago(t)
	after := time.Date(2026, 10, 1, 0, 0, 0, 0, chi)
	now := time.Date(2027, 4, 1, 0, 0, 0, 0, chi)
	p := SchedulePolicy{Cron: "* * * * *", Timezone: "America/Chicago", DSTAck: true}
	newest, missed, err := p.CatchUp(after, now)
	if err != nil {
		t.Fatalf("CatchUp: %v", err)
	}
	if want := int(now.Sub(after) / time.Minute); missed != want || !newest.Equal(now) {
		t.Fatalf("CatchUp = %v, %d; want %v, %d", newest, missed, now, want)
	}
}

// BenchmarkCatchUp is the cost the clock pays, under the store lock, to
// recover a minutely schedule after two months down.
func BenchmarkCatchUp(b *testing.B) {
	p := SchedulePolicy{Cron: "* * * * *", Timezone: "America/Chicago", DSTAck: true}
	after := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := after.AddDate(0, 2, 0)
	for b.Loop() {
		if _, _, err := p.CatchUp(after, now); err != nil {
			b.Fatal(err)
		}
	}
}

// TestOccupiesFiringSlot is ADR-010 Amendment 1's slot rule: only a
// session of the current firing occupies, live or succeeded; a failed,
// crashed or earlier-firing session occupies nothing.
func TestOccupiesFiringSlot(t *testing.T) {
	t.Parallel()
	headless := Runtime{Mode: RuntimeModeHeadless}
	tests := []struct {
		name  string
		state SessionState
		fire  string
		want  bool
	}{
		{"live, current firing", SessionRunning, "f2", true},
		{"succeeded, current firing", SessionSucceeded, "f2", true},
		{"crashed, current firing", SessionCrashed, "f2", false},
		{"failed, current firing", SessionFailed, "f2", false},
		{"live, earlier firing", SessionRunning, "f1", false},
		{"succeeded, earlier firing", SessionSucceeded, "f1", false},
	}
	var all []Session
	for _, tc := range tests {
		s := Session{State: tc.state, Firing: tc.fire, Runtime: headless}
		all = append(all, s)
		if got := OccupiesFiringSlot(s, "f2"); got != tc.want {
			t.Errorf("%s: occupies = %v, want %v", tc.name, got, tc.want)
		}
	}
	if n := CountFiringSlots(all, "f2"); n != 2 {
		t.Errorf("CountFiringSlots = %d, want 2", n)
	}
	if n := CountFiringSlots(all, ""); n != 0 {
		t.Errorf("no current firing: CountFiringSlots = %d, want 0", n)
	}
}

// TestSettleRun is the firing record settling failures (design section 2,
// section 11's ruling): a failed run of the current firing spends an
// attempt and retries after a backoff while attempts <= retries and the
// firing is inside starting_deadline; then the firing settles, and
// on_failure = freeze freezes the schedule. A cancelled run settles its
// firing. A run of an earlier firing changes nothing.
func TestSettleRun(t *testing.T) {
	t.Parallel()
	due := time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC)
	now := due.Add(5 * time.Minute)
	base := SchedulePolicy{OnFailure: ScheduleOnFailureWait}
	tests := []struct {
		name        string
		p           func(SchedulePolicy) SchedulePolicy
		attempts    int
		outcome     RunOutcome
		firing      string
		wantSettled bool
		wantFrozen  bool
		wantRetry   bool
		// wantAttempts is the attempt count after the run is settled.
		wantAttempts int
	}{
		{"no retries settles", func(p SchedulePolicy) SchedulePolicy { return p }, 0, RunFailed, "f1", true, false, false, 1},
		{"one retry waits a backoff", func(p SchedulePolicy) SchedulePolicy { p.Retries = 1; return p }, 0, RunFailed, "f1", false, false, true, 1},
		{"retries used up settles", func(p SchedulePolicy) SchedulePolicy { p.Retries = 1; return p }, 1, RunFailed, "f1", true, false, false, 2},
		{"past starting_deadline settles", func(p SchedulePolicy) SchedulePolicy { p.Retries = 3; p.StartingDeadline = time.Minute; return p }, 0, RunFailed, "f1", true, false, false, 1},
		{"freeze freezes", func(p SchedulePolicy) SchedulePolicy { p.OnFailure = ScheduleOnFailureFreeze; return p }, 0, RunFailed, "f1", true, true, false, 1},
		{"no starting_deadline: retries are not bounded in time", func(p SchedulePolicy) SchedulePolicy { p.Retries = 1; return p }, 0, RunFailed, "f1", false, false, true, 1},
		{"cancelled settles without an attempt", func(p SchedulePolicy) SchedulePolicy { p.Retries = 2; return p }, 0, RunCancelled, "f1", true, false, false, 0},
		{"success changes nothing", func(p SchedulePolicy) SchedulePolicy { return p }, 0, RunSucceeded, "f1", false, false, false, 0},
		{"an earlier firing changes nothing", func(p SchedulePolicy) SchedulePolicy { return p }, 0, RunFailed, "f0", false, false, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := ScheduleStatus{Firing: "f1", FiringDueAt: due, Attempts: tc.attempts}
			froze := st.SettleRun(RunRecord{Outcome: tc.outcome, Firing: tc.firing}, tc.p(base), now)
			if st.Settled != tc.wantSettled || st.Frozen != tc.wantFrozen || froze != tc.wantFrozen {
				t.Fatalf("settled %v frozen %v (reported %v), want %v %v", st.Settled, st.Frozen, froze, tc.wantSettled, tc.wantFrozen)
			}
			if got := !st.RetryAfter.IsZero(); got != tc.wantRetry {
				t.Fatalf("retry after = %v, want a retry: %v", st.RetryAfter, tc.wantRetry)
			}
			if tc.wantRetry && !st.RetryAfter.After(now) {
				t.Fatalf("retry after %v is not after now %v", st.RetryAfter, now)
			}
			if st.Attempts != tc.wantAttempts {
				t.Fatalf("attempts = %d, want %d", st.Attempts, tc.wantAttempts)
			}
		})
	}
}

// TestFiringIDIsTheDueTime: a firing id names its nominal due time in
// UTC, so two firings of one role never share an id and a reader can tell
// which firing a run belonged to.
func TestFiringIDIsTheDueTime(t *testing.T) {
	t.Parallel()
	chi := chicago(t)
	due := time.Date(2026, 10, 1, 1, 17, 0, 0, chi)
	if got := FiringID(due); got != "20261001T061700Z" {
		t.Fatalf("FiringID = %q, want 20261001T061700Z", got)
	}
}
