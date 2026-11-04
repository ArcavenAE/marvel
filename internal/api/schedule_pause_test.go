package api

import (
	"testing"
	"time"
)

// A fleet suspend is left out of the time since the freshness anchor, so a
// pause longer than stale_after does not raise the alarm, and a gap that is
// still too long after the pause comes out does.
func TestEvaluateFreshnessLeavesThePauseOut(t *testing.T) {
	anchor := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const staleAfter = 30 * time.Hour
	cases := map[string]struct {
		now    time.Time
		paused time.Duration
		stale  bool
	}{
		"no pause, inside":                 {anchor.Add(29 * time.Hour), 0, false},
		"no pause, past":                   {anchor.Add(31 * time.Hour), 0, true},
		"a pause brings it back inside":    {anchor.Add(41 * time.Hour), 40 * time.Hour, false},
		"still past once the pause is out": {anchor.Add(50 * time.Hour), 10 * time.Hour, true},
		"exactly at the limit is not past": {anchor.Add(40 * time.Hour), 10 * time.Hour, false},
		"a negative count is none":         {anchor.Add(31 * time.Hour), -time.Hour, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			st := ScheduleStatus{Since: anchor, LastSucceededAt: anchor, PausedSinceAnchor: c.paused}
			st.EvaluateFreshness(c.now, staleAfter)
			if st.Stale != c.stale {
				t.Errorf("Stale = %v, want %v", st.Stale, c.stale)
			}
		})
	}
}

// With no success the anchor is Since, and the pause is left out the same way.
func TestEvaluateFreshnessLeavesThePauseOutOfSinceToo(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	st := ScheduleStatus{Since: since, PausedSinceAnchor: 40 * time.Hour}
	st.EvaluateFreshness(since.Add(41*time.Hour), 30*time.Hour)
	if st.Stale {
		t.Error("a schedule with no success read stale after a 40h pause")
	}
}

// A success that advances LastSucceededAt starts the count over; a failure, or
// a success older than the last one, does not.
func TestAddRunResetsThePauseOnlyWhenTheAnchorAdvances(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	h := ScheduleHistory{Succeeded: 3, Failed: 3}
	st := ScheduleStatus{Since: t0, LastSucceededAt: t0, PausedSinceAnchor: 5 * time.Hour}

	st.AddRun(RunRecord{Outcome: RunFailed, EndedAt: t0.Add(time.Hour)}, h)
	if st.PausedSinceAnchor != 5*time.Hour {
		t.Errorf("a failed run reset the count to %v", st.PausedSinceAnchor)
	}
	st.AddRun(RunRecord{Outcome: RunSucceeded, EndedAt: t0.Add(-time.Hour)}, h)
	if st.PausedSinceAnchor != 5*time.Hour {
		t.Errorf("an older success reset the count to %v", st.PausedSinceAnchor)
	}
	st.AddRun(RunRecord{Outcome: RunSucceeded, EndedAt: t0.Add(2 * time.Hour)}, h)
	if st.PausedSinceAnchor != 0 {
		t.Errorf("a newer success left the count at %v, want 0", st.PausedSinceAnchor)
	}
}

// Both fields survive the store and a bolt reload.
func TestSchedulePauseFieldsSurviveBolt(t *testing.T) {
	path := t.TempDir() + "/s.bolt"
	st := NewStore()
	if err := st.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	if _, err := st.UpdateScheduleStatus("ws/t/r", func(s *ScheduleStatus) bool {
		s.PausedSinceAnchor = 90 * time.Minute
		s.PauseAppliedSince = since
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	st2 := NewStore()
	if err := st2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.CloseBolt() })
	got, ok := st2.GetScheduleStatus("ws/t/r")
	if !ok || got.PausedSinceAnchor != 90*time.Minute || !got.PauseAppliedSince.Equal(since) {
		t.Errorf("reload = %+v (found %v), want the two pause fields kept", got, ok)
	}
}
