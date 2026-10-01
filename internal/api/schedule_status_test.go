package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var statusEpoch = time.Date(2026, 10, 1, 6, 17, 0, 0, time.UTC)

func run(session string, outcome RunOutcome, endedMin int) RunRecord {
	return RunRecord{
		Session: session, Outcome: outcome,
		StartedAt: statusEpoch.Add(time.Duration(endedMin-1) * time.Minute),
		EndedAt:   statusEpoch.Add(time.Duration(endedMin) * time.Minute),
	}
}

func historySessions(st ScheduleStatus) string {
	names := make([]string, 0, len(st.History))
	for _, r := range st.History {
		names = append(names, r.Session)
	}
	return strings.Join(names, ",")
}

// TestScheduleStatusHistoryIsBounded: history keeps the newest
// history.succeeded successes and the newest history.failed failures,
// each bound applied to its own outcome, in the order the runs ended.
func TestScheduleStatusHistoryIsBounded(t *testing.T) {
	tests := []struct {
		name string
		h    ScheduleHistory
		runs []RunRecord
		want string
	}{
		{
			name: "under both bounds keeps everything",
			h:    ScheduleHistory{Succeeded: 3, Failed: 3},
			runs: []RunRecord{run("s1", RunSucceeded, 1), run("f1", RunFailed, 2)},
			want: "s1,f1",
		},
		{
			name: "successes past their bound drop the oldest success only",
			h:    ScheduleHistory{Succeeded: 2, Failed: 3},
			runs: []RunRecord{
				run("s1", RunSucceeded, 1), run("f1", RunFailed, 2),
				run("s2", RunSucceeded, 3), run("s3", RunSucceeded, 4),
			},
			want: "f1,s2,s3",
		},
		{
			name: "failures past their bound drop the oldest failure only",
			h:    ScheduleHistory{Succeeded: 3, Failed: 1},
			runs: []RunRecord{
				run("f1", RunFailed, 1), run("s1", RunSucceeded, 2), run("f2", RunFailed, 3),
			},
			want: "s1,f2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var st ScheduleStatus
			for _, r := range tc.runs {
				st.AddRun(r, tc.h)
			}
			if got := historySessions(st); got != tc.want {
				t.Fatalf("history = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestScheduleStatusLastSucceeded: a success sets last-succeeded; a later
// failure leaves it where it was.
func TestScheduleStatusLastSucceeded(t *testing.T) {
	var st ScheduleStatus
	h := ScheduleHistory{Succeeded: 3, Failed: 3}
	st.AddRun(run("s1", RunSucceeded, 5), h)
	st.AddRun(run("f1", RunFailed, 9), h)
	if want := statusEpoch.Add(5 * time.Minute); !st.LastSucceededAt.Equal(want) {
		t.Fatalf("last succeeded = %v, want %v", st.LastSucceededAt, want)
	}
	if st.Since.IsZero() {
		t.Fatal("a status written by a run carries no since")
	}
}

// TestScheduleStatusFreshness is design section 7: stale when no
// succeeded run is newer than stale_after, reported once per transition,
// and again on recovery. With no success yet, the bound runs from when
// marvel first recorded the schedule.
func TestScheduleStatusFreshness(t *testing.T) {
	const after = 30 * time.Hour
	var st ScheduleStatus

	if st.EvaluateFreshness(statusEpoch, after) {
		t.Fatal("the first evaluation reported a transition")
	}
	if !st.Since.Equal(statusEpoch) {
		t.Fatalf("since = %v, want the first evaluation %v", st.Since, statusEpoch)
	}
	if st.EvaluateFreshness(statusEpoch.Add(after), after) || st.Stale {
		t.Fatal("stale at exactly stale_after; the bound is exclusive")
	}
	if !st.EvaluateFreshness(statusEpoch.Add(after+time.Minute), after) || !st.Stale {
		t.Fatal("not stale one minute past stale_after with no success")
	}
	if st.EvaluateFreshness(statusEpoch.Add(after+time.Hour), after) {
		t.Fatal("a standing stale state reported a second transition")
	}

	st.AddRun(run("s1", RunSucceeded, int((after + 2*time.Hour).Minutes())), ScheduleHistory{Succeeded: 3, Failed: 3})
	recovered := statusEpoch.Add(after + 2*time.Hour + time.Minute)
	if !st.EvaluateFreshness(recovered, after) || st.Stale {
		t.Fatal("a fresh success did not report recovery")
	}
	if !st.StaleChangedAt.Equal(recovered) {
		t.Fatalf("stale changed at = %v, want %v", st.StaleChangedAt, recovered)
	}
	if !st.EvaluateFreshness(st.LastSucceededAt.Add(after+time.Second), after) {
		t.Fatal("the bound did not run from the last success")
	}
}

// TestRunRecordResultIsCapped: the result keeps at most 4 KiB, cut on a
// rune boundary, and says when it was cut.
func TestRunRecordResultIsCapped(t *testing.T) {
	var r RunRecord
	r.SetResult("short")
	if r.Result != "short" || r.ResultTruncated {
		t.Fatalf("short result = %q truncated %v", r.Result, r.ResultTruncated)
	}
	long := strings.Repeat("é", 3000) // 6000 bytes, two per rune
	r.SetResult(long)
	if len(r.Result) > 4096 || !utf8.ValidString(r.Result) || !r.ResultTruncated {
		t.Fatalf("long result: %d bytes, valid %v, truncated %v", len(r.Result), utf8.ValidString(r.Result), r.ResultTruncated)
	}
	if len(r.Result) < 4095 {
		t.Fatalf("long result cut to %d bytes, want the 4 KiB cap less at most one rune", len(r.Result))
	}
	if d := run("s", RunSucceeded, 3).Duration(); d != time.Minute {
		t.Fatalf("duration = %v, want 1m", d)
	}
}

// TestScheduleStatusStore: the store creates a status on first update,
// hands out copies, and persists only what the update marks as changed.
func TestScheduleStatusStore(t *testing.T) {
	s := NewStore()
	const key = "timers/board/refresh"
	if _, ok := s.GetScheduleStatus(key); ok {
		t.Fatal("a status exists before any update")
	}
	got, err := s.UpdateScheduleStatus(key, func(st *ScheduleStatus) bool {
		st.AddRun(run("s1", RunSucceeded, 1), ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Key != key || len(got.History) != 1 {
		t.Fatalf("update returned %+v", got)
	}
	snap, ok := s.GetScheduleStatus(key)
	if !ok || len(snap.History) != 1 {
		t.Fatalf("get = %+v, %v", snap, ok)
	}
	snap.History[0].Session = "edited"
	if again, _ := s.GetScheduleStatus(key); again.History[0].Session != "s1" {
		t.Fatal("editing a snapshot edited the store")
	}
	if n := len(s.ListScheduleStatus()); n != 1 {
		t.Fatalf("list = %d records, want 1", n)
	}
	if err := s.DeleteScheduleStatus(key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.GetScheduleStatus(key); ok {
		t.Fatal("status survived delete")
	}
}

// TestBoltStore_ScheduleStatusRoundTrips: history, last succeeded and the
// stale state survive a daemon restart (design section 7), so a restart
// neither forgets a stale schedule nor announces it a second time.
func TestBoltStore_ScheduleStatusRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	const key = "timers/board/refresh"
	if _, err := s1.UpdateScheduleStatus(key, func(st *ScheduleStatus) bool {
		st.AddRun(run("s1", RunSucceeded, 1), ScheduleHistory{Succeeded: 3, Failed: 3})
		st.EvaluateFreshness(statusEpoch.Add(40*time.Hour), 30*time.Hour)
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, ok := s2.GetScheduleStatus(key)
	if !ok {
		t.Fatal("schedule status lost across a restart")
	}
	if len(got.History) != 1 || got.History[0].Session != "s1" || !got.Stale || got.LastSucceededAt.IsZero() {
		t.Fatalf("rehydrated status = %+v", got)
	}
	if err := s2.DeleteScheduleStatus(key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s2.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt #2: %v", err)
	}
	s3 := NewStore()
	if err := s3.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #3: %v", err)
	}
	t.Cleanup(func() { _ = s3.CloseBolt() })
	if _, ok := s3.GetScheduleStatus(key); ok {
		t.Fatal("a deleted status came back after a restart")
	}
}
