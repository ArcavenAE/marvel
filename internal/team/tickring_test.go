package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

var ringEpoch = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

// feed records a tick every step from ringEpoch for span, the session always
// active (ContextAt at the tick), and returns the time of the last tick.
func feed(r *TickRing, step, span time.Duration) time.Time {
	var at time.Time
	for off := time.Duration(0); off <= span; off += step {
		at = ringEpoch.Add(off)
		r.Record(at, at)
	}
	return at
}

// The ring holds the last fifteen minutes of two-second ticks and nothing
// older: 450 of them, counted when read.
func TestTickRingHoldsFifteenMinutes(t *testing.T) {
	r := &TickRing{}
	last := feed(r, 2*time.Second, 20*time.Minute)

	active, total, full := r.Counts(10*time.Minute, last)
	if total != 450 || active != 450 || !full {
		t.Fatalf("counts = %d active of %d, full %v; want 450 of 450, full", active, total, full)
	}
	if len(r.samples) != 450 {
		t.Errorf("the ring holds %d samples, want it bounded at 450", len(r.samples))
	}
}

// Before the ring has watched for the whole window it reports what it has and
// says it is not full, so the reader can show a dash.
func TestTickRingIsNotFullBeforeTheWindowFills(t *testing.T) {
	r := &TickRing{}
	last := feed(r, 2*time.Second, 10*time.Minute)
	active, total, full := r.Counts(10*time.Minute, last)
	if full || total != 301 || active != 301 {
		t.Fatalf("counts = %d of %d, full %v; want 301 of 301, not full", active, total, full)
	}
}

// An empty ring is not full and counts nothing.
func TestTickRingEmptyIsNotFull(t *testing.T) {
	r := &TickRing{}
	if a, n, full := r.Counts(time.Minute, ringEpoch); a != 0 || n != 0 || full {
		t.Fatalf("empty ring = %d of %d, full %v", a, n, full)
	}
}

// What counts as quiet is the one predicate, applied to the ContextAt each tick
// saw and the window the reader passes: never observed is quiet, exactly the
// window old is not, older is. The ring stores no percentage, so the same ticks
// read differently under another window.
func TestTickRingCountsQuietByTheOneQuietPredicate(t *testing.T) {
	r := &TickRing{}
	w := 10 * time.Minute
	at := ringEpoch.Add(20 * time.Minute)
	r.Record(at.Add(-4*time.Second), time.Time{})                    // never observed: quiet
	r.Record(at.Add(-3*time.Second), at.Add(-3*time.Second).Add(-w)) // exactly the window: active
	r.Record(at.Add(-2*time.Second), at.Add(-2*time.Second).Add(-w-time.Second))
	r.Record(at.Add(-1*time.Second), at.Add(-time.Second)) // just seen: active

	active, total, _ := r.Counts(w, at)
	if active != 2 || total != 4 {
		t.Fatalf("under %v: %d of %d, want 2 of 4", w, active, total)
	}
	active, total, _ = r.Counts(w+2*time.Second, at)
	if active != 3 || total != 4 {
		t.Fatalf("under %v: %d of %d, want 3 of 4 from the same ticks", w+2*time.Second, active, total)
	}
}

// Ticks older than the window are not counted even before a Record trims them.
func TestTickRingCountsOnlyTheLastWindowWhenRead(t *testing.T) {
	r := &TickRing{}
	last := feed(r, 2*time.Second, 20*time.Minute)
	_, total, _ := r.Counts(10*time.Minute, last.Add(5*time.Minute))
	if total != 300 {
		t.Fatalf("total five minutes later = %d, want the 300 ticks still inside the window", total)
	}
}

// Every evaluation tick of a running session lands in its ring, and the ring is
// dropped when the session is no longer running.
func TestEvaluateHealthFeedsTheTickRing(t *testing.T) {
	f := newListFixture(t, "test-ring-feed", viewRole())
	sess := f.seed(time.Hour, time.Minute, 0, 0)

	for i := 0; i < 3; i++ {
		f.ctrl.evaluateHealth()
	}
	if _, total, _ := f.ctrl.ActiveTicks(sess.Key(), 10*time.Minute, time.Now().UTC()); total != 3 {
		t.Fatalf("ticks after three evaluations = %d, want 3", total)
	}
	if _, total, _ := f.ctrl.ActiveTicks("ws/nobody", 10*time.Minute, time.Now().UTC()); total != 0 {
		t.Errorf("a session never evaluated has %d ticks", total)
	}

	if err := f.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.State = api.SessionCrashed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.ctrl.evaluateHealth()
	if _, total, _ := f.ctrl.ActiveTicks(sess.Key(), 10*time.Minute, time.Now().UTC()); total != 0 {
		t.Errorf("a session that is no longer running kept %d ticks", total)
	}
}
