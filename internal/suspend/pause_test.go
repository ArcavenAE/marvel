package suspend

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// A deadline is a future stamp, so the pause moves it later with no cap at now:
// a backoff that had 10 minutes left when the cluster stopped has 10 minutes
// left when it resumes.
func TestShiftedDeadline(t *testing.T) {
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		d     time.Time
		pause time.Duration
		want  time.Time
	}{
		"moved by the pause, past now": {at.Add(10 * time.Minute), time.Hour, at.Add(70 * time.Minute)},
		"expired stays behind now":     {at.Add(-5 * time.Minute), time.Hour, at.Add(55 * time.Minute)},
		"zero stays zero":              {time.Time{}, time.Hour, time.Time{}},
		"negative shifts none":         {at.Add(time.Minute), -time.Hour, at.Add(time.Minute)},
		"zero pause shifts none":       {at.Add(time.Minute), 0, at.Add(time.Minute)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ShiftedDeadline(c.d, c.pause); !got.Equal(c.want) {
				t.Errorf("ShiftedDeadline = %v, want %v", got, c.want)
			}
		})
	}
}

// The hook runs after every SIGCONT and before the record is cleared, so a
// crash between the two leaves a record that a repeat resume still holds.
func TestThawWithRunsTheHookAfterTheSignalsAndBeforeClear(t *testing.T) {
	o := newFakeOS(100, 200)
	for _, p := range o.procs {
		p.stopped = true
	}
	rec := record(100, 200)
	rec.State = StateSuspended
	st := &memStore{os: o}
	var logAtHook []string
	var clearedAtHook bool
	var gotRec Record
	_, err := o.freezer(t).ThawWith(rec, st, func(r Record) error {
		logAtHook = slices.Clone(o.log)
		clearedAtHook = st.cleared
		gotRec = r
		return nil
	})
	if err != nil {
		t.Fatalf("ThawWith: %v", err)
	}
	if want := []string{"CONT 200", "CONT 100"}; !slices.Equal(logAtHook, want) {
		t.Errorf("signals at the hook = %v, want %v", logAtHook, want)
	}
	if clearedAtHook {
		t.Error("the record was cleared before the hook ran")
	}
	if !st.cleared {
		t.Error("the record was not cleared after the hook succeeded")
	}
	if !gotRec.Since.Equal(rec.Since) {
		t.Errorf("hook saw Since %v, want %v", gotRec.Since, rec.Since)
	}
}

// A hook that fails leaves the record as resuming and returns the error, so
// resume can be run again.
func TestThawWithKeepsTheRecordWhenTheHookFails(t *testing.T) {
	o := newFakeOS(100, 200)
	for _, p := range o.procs {
		p.stopped = true
	}
	rec := record(100, 200)
	rec.State = StateSuspended
	st := &memStore{os: o}
	boom := errors.New("shift failed")
	_, err := o.freezer(t).ThawWith(rec, st, func(Record) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("ThawWith = %v, want the hook error", err)
	}
	if st.cleared {
		t.Error("the record was cleared although the hook failed")
	}
	if n := len(st.saves); n == 0 || st.saves[n-1] != StateResuming {
		t.Errorf("saves = %v, want the record left resuming", st.saves)
	}
}

// Thaw is ThawWith with no hook.
func TestThawIsThawWithNoHook(t *testing.T) {
	o := newFakeOS(100)
	for _, p := range o.procs {
		p.stopped = true
	}
	rec := record(100)
	rec.State = StateSuspended
	st := &memStore{os: o}
	if _, err := o.freezer(t).ThawWith(rec, st, nil); err != nil {
		t.Fatalf("ThawWith nil: %v", err)
	}
	if !st.cleared {
		t.Error("record not cleared")
	}
}
