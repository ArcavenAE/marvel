package usage

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

func quietFixture(contextAge time.Duration) (asof.Cell[float64], *api.Session, time.Time) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cell := asof.Cell[float64]{
		Value:      40,
		ObservedAt: now.Add(-contextAge),
		ValidUntil: now.Add(-contextAge).Add(OutRateValidFor),
	}
	s := &api.Session{}
	s.ContextAt = now.Add(-contextAge)
	return cell, s, now
}

// The rate and the validity of its reading are the same ten minutes as the
// default quiet window: one constant, so they cannot drift apart.
func TestOutRateValidForIsTheDefaultQuietWindow(t *testing.T) {
	if OutRateValidFor != api.DefaultQuietWindow {
		t.Errorf("OutRateValidFor = %v, want api.DefaultQuietWindow %v", OutRateValidFor, api.DefaultQuietWindow)
	}
}

// A seat that went quiet reads exactly 0 the moment the shared predicate says
// quiet, not a number still decaying toward it.
func TestRateSnapsToZeroExactlyWhenQuiet(t *testing.T) {
	const w = api.DefaultQuietWindow
	cell, s, now := quietFixture(w + time.Nanosecond)
	got, state := QuietRate(cell, s, w, true, now)
	if got != 0 || state != asof.Fresh {
		t.Errorf("past the window: rate %v state %v, want exactly 0 and fresh", got, state)
	}

	cell, s, now = quietFixture(w)
	got, state = QuietRate(cell, s, w, true, now)
	if got <= 0 || state != asof.Fresh {
		t.Errorf("at exactly the window the seat is not quiet yet: rate %v state %v, want a decaying positive value", got, state)
	}

	cell, s, now = quietFixture(30 * time.Second)
	if got, _ := QuietRate(cell, s, w, true, now); got <= 0 {
		t.Errorf("a seat that was active 30s ago reads %v, want a positive decaying rate", got)
	}
}

// Past the window both rules apply: the reading has expired and the seat is
// quiet. With an activity channel attached the snapped 0 wins, because the
// channel is why 0 is a measurement; the expired ? is for a reading whose
// channel is gone.
func TestSnappedZeroBeatsStaleWhenAChannelIsAttached(t *testing.T) {
	const w = api.DefaultQuietWindow
	cell, s, now := quietFixture(w + time.Minute)
	if cell.State(now) != asof.Stale {
		t.Fatal("setup: the reading should have expired")
	}
	if got, state := QuietRate(cell, s, w, true, now); got != 0 || state != asof.Fresh {
		t.Errorf("with a channel: rate %v state %v, want snapped 0 and fresh", got, state)
	}
	if _, state := QuietRate(cell, s, w, false, now); state != asof.Stale {
		t.Errorf("without a channel: state %v, want stale", state)
	}
}

// A rate never sampled is absent, not zero, whatever the predicate says.
func TestQuietRateNeverSampledIsAbsent(t *testing.T) {
	_, s, now := quietFixture(time.Hour)
	if got, state := QuietRate(asof.Cell[float64]{}, s, api.DefaultQuietWindow, true, now); got != 0 || state != asof.None {
		t.Errorf("never sampled: rate %v state %v, want 0 and none", got, state)
	}
}
