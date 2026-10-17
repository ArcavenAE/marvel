package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// stampTotals sets the seat's cost and API-time change stamp, the way a
// statusline beat whose totals moved would (marvel#801).
func (f *listFixture) stampTotals(s api.Session, ago time.Duration) {
	f.t.Helper()
	at := f.clock.Now().Add(-ago)
	if err := f.store.UpdateSession(s.Key(), func(live *api.Session) error {
		live.TotalsChangedAt = at
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

// setContextAt backdates the seat's feed timestamp on the fixture's clock.
// seed cannot: UpdateSessionContext stamps wall time over whatever it is given.
func (f *listFixture) setContextAt(s api.Session, ago time.Duration) {
	f.t.Helper()
	at := f.clock.Now().Add(-ago)
	if err := f.store.UpdateSession(s.Key(), func(live *api.Session) error {
		live.ContextAt = at
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

// The handoff ask lands at the first pause of five minutes or more, and a pause
// is read from the change stamp where the seat has one: a statusline that beats
// on an idle seat moves ContextAt but not the totals (marvel#936).
func TestMaxAgeAskWaitsForAFiveMinutePause(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		contextAt time.Duration // ago
		stamp     time.Duration // ago; 0 means no stamp
		conds     []api.ShiftCondition
		wantAsk   bool
	}{
		{"stamped seat idle six minutes, feed still beating", 10 * time.Second, 6 * time.Minute, nil, true},
		{"stamped seat idle three minutes, feed still beating", 10 * time.Second, 3 * time.Minute, nil, false},
		{"stamped seat idle exactly five minutes", 10 * time.Second, 5 * time.Minute, nil, true},
		{"stamped seat worked a minute ago, quiet feed", 10 * time.Minute, time.Minute, nil, false},
		{"no stamp, feed quiet three minutes", 3 * time.Minute, 0, nil, false},
		{"no stamp, feed quiet six minutes", 6 * time.Minute, 0, nil, true},
		{
			"explicit quiet_for wins over the default",
			3 * time.Minute, 0,
			[]api.ShiftCondition{{On: api.ShiftTriggerMaxAge, MaxAge: testMaxAge, QuietFor: 2 * time.Minute}},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newListFixture(t, "test-pause-"+time.Now().Format("150405.000000000"), maxAgeRole("", tc.conds...))
			s := f.seed(testMaxAge+10*time.Minute, tc.contextAt, 0, 0)
			f.setContextAt(s, tc.contextAt)
			if tc.stamp > 0 {
				f.stampTotals(s, tc.stamp)
			}
			f.evaluate()
			if got := len(f.notices) == 1; got != tc.wantAsk {
				t.Fatalf("asked = %v (notices %q), want %v", got, f.notices, tc.wantAsk)
			}
		})
	}
}

// With no pause the ask still goes out at the end of the existing window, and
// a stamp that keeps moving does not push it past that.
func TestMaxAgeAskStillGoesOutAtTheEndOfTheWindow(t *testing.T) {
	t.Parallel()
	f := newListFixture(t, "test-pause-hard", maxAgeRole(""))
	s := f.seed(testMaxAge+api.DefaultShiftMaxDefer+time.Minute, 10*time.Second, 0, 0)
	f.setContextAt(s, 10*time.Second)
	f.stampTotals(s, 10*time.Second)
	f.evaluate()
	if len(f.notices) != 1 {
		t.Fatalf("notices = %d, want 1 at max_age + max_defer however busy", len(f.notices))
	}
}
