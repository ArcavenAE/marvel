package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// advanceBy moves the clock by d and runs the two ticks a repeat needs, as
// nextWindow does for one window.
func (f *deliverFixture) advanceBy(d time.Duration) {
	f.t.Helper()
	f.clock.Advance(d)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
	f.clock.Advance(time.Second)
	f.evaluate()
	f.ctrl.handoffProbes.wait()
}

// An escalated request that nobody decides keeps the supervisor told, but the
// sends back off: the first few repeats come one window apart, then the gap
// doubles up to a ceiling. Before this a request nobody decided sent once per
// window for as long as it stood.
func TestEscalatedRequestRepeatBacksOff(t *testing.T) {
	f := newDeliverFixture(t, "test-dlv-backoff", true, true)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 1_000_000)
	f.escalate()

	const windows = 100
	for i := 0; i < windows; i++ {
		f.nextWindow()
	}
	sent := len(f.pub.delivered())
	t.Logf("sent %d envelopes over %d windows", sent, windows)
	if sent > 12 {
		t.Fatalf("sent %d envelopes over %d windows (%s); want the repeats to back off, at most 12",
			sent, windows, time.Duration(windows)*5*time.Minute)
	}
	// The supervisor is still told: more than the escalation alone.
	if sent < 4 {
		t.Fatalf("sent %d envelopes, want the escalation and the flat repeats (at least 4)", sent)
	}
}

// The repeats never stop: at the ceiling the supervisor still gets one a day.
func TestEscalatedRequestRepeatContinuesAtTheCeiling(t *testing.T) {
	f := newDeliverFixture(t, "test-dlv-ceiling", true, true)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 1_000_000)
	f.escalate()
	for i := 0; i < 100; i++ {
		f.nextWindow()
	}
	// Walk well past the doubling so the gap is at the ceiling.
	for _, d := range []time.Duration{2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		f.advanceBy(d)
	}
	before := len(f.pub.delivered())
	f.advanceBy(api.DefaultShiftRepeatCeiling + time.Second)
	if got := len(f.pub.delivered()); got != before+1 {
		t.Fatalf("a day at the ceiling sent %d more envelopes, want exactly 1", got-before)
	}
}

// The role's own values change the schedule: with one flat repeat the third
// send waits two windows, where the default would still be at one.
func TestEscalatedRequestRepeatFollowsTheRoleConfiguration(t *testing.T) {
	f := newDeliverFixture(t, "test-dlv-config", true, true)
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 1_000_000)
	if err := f.store.UpdateTeam(f.teamKey, func(live *api.Team) error {
		for i := range live.Roles {
			if live.Roles[i].Shift != nil {
				live.Roles[i].Shift.HandoffRepeatFlat = 1
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.escalate()
	for i := 0; i < 6; i++ {
		f.nextWindow()
	}
	if got := len(f.pub.delivered()); got != 3 {
		t.Fatalf("sent %d envelopes over six windows with one flat repeat, want 3 (the default schedule sends 5)", got)
	}
}
