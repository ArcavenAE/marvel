package asof

import (
	"testing"
	"time"
)

// A stamp later than the reader's now plus the tolerance is neither fresh nor
// stale: its age cannot be told, so it prints the question mark and never the
// value. A never-observed cell stays a dash.
func TestAsOfFutureStampReadsQuestion(t *testing.T) {
	ahead := Cell[int]{Value: 42, ObservedAt: t0.Add(10 * time.Minute)}

	if got := ahead.StateWithin(t0, 0); got != Future {
		t.Fatalf("StateWithin = %q, want %q", got, Future)
	}
	if got := ahead.RenderWithin(t0, 0, itoa); got != "?" {
		t.Errorf("RenderWithin = %q, want ? and never the value", got)
	}
	if got := ahead.State(t0); got != Future {
		t.Errorf("State with no tolerance = %q, want %q", got, Future)
	}

	// The tolerance is inclusive: a stamp exactly now+tol is still fresh.
	tol := 10 * time.Minute
	if got := ahead.StateWithin(t0, tol); got != Fresh {
		t.Errorf("stamp at now+tol: State = %q, want %q", got, Fresh)
	}
	if got := ahead.RenderWithin(t0, tol, itoa); got != "42" {
		t.Errorf("stamp at now+tol: Render = %q, want 42", got)
	}
	if got := ahead.StateWithin(t0, tol-time.Nanosecond); got != Future {
		t.Errorf("stamp 1ns past now+tol: State = %q, want %q", got, Future)
	}

	// Future is decided before expiry: a stamp in the future with a valid_until
	// that has not arrived is Future, not Fresh.
	both := Cell[int]{Value: 7, ObservedAt: t0.Add(time.Hour), ValidUntil: t0.Add(2 * time.Hour)}
	if got := both.StateWithin(t0, 0); got != Future {
		t.Errorf("future stamp with open valid_until: State = %q, want %q", got, Future)
	}

	var none Cell[int]
	if got := none.StateWithin(t0, 0); got != None {
		t.Errorf("never observed: State = %q, want %q", got, None)
	}
	if got := none.RenderWithin(t0, 0, itoa); got != "-" {
		t.Errorf("never observed: Render = %q, want -", got)
	}
}
