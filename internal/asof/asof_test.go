package asof

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func itoa(n int) string { return strconv.Itoa(n) }

// A cell that was never observed prints a dash, including when its value
// field holds the zero value: a missing sample must never read as 0.
func TestAsOfDashWhenNeverObserved(t *testing.T) {
	var c Cell[int]
	if got := c.State(t0); got != None {
		t.Fatalf("State = %q, want %q", got, None)
	}
	if got := c.Render(t0, itoa); got != "-" {
		t.Errorf("Render = %q, want %q", got, "-")
	}
	// A zero value with no observation is still none, never "0".
	c = Cell[int]{Value: 0, Source: "statusline"}
	if got := c.Render(t0, itoa); got != "-" {
		t.Errorf("Render with source only = %q, want %q", got, "-")
	}
}

// An expired reading prints a question mark. The number it once held is
// never shown, because the number is no longer true.
func TestAsOfStaleNeverPrintsNumber(t *testing.T) {
	c := Cell[int]{Value: 42, ObservedAt: t0, ValidUntil: t0.Add(time.Minute)}

	if got := c.Render(t0.Add(time.Minute), itoa); got != "42" {
		t.Errorf("at valid_until Render = %q, want %q (still fresh)", got, "42")
	}
	now := t0.Add(time.Minute + time.Nanosecond)
	if got := c.State(now); got != Stale {
		t.Fatalf("State = %q, want %q", got, Stale)
	}
	if got := c.Render(now, itoa); got != "?" {
		t.Errorf("Render = %q, want %q", got, "?")
	}

	// No valid_until means the reading does not expire.
	forever := Cell[int]{Value: 7, ObservedAt: t0}
	if got := forever.Render(t0.Add(24*time.Hour), itoa); got != "7" {
		t.Errorf("no valid_until Render = %q, want %q", got, "7")
	}
}

// The cell survives the wire: fresh, stale and none all decode to the
// value that was encoded, and the state words are derived from the
// clock, not stored.
func TestAsOfJSONRoundTrip(t *testing.T) {
	cases := map[string]Cell[float64]{
		"fresh": {Value: 1.5, ObservedAt: t0, ValidUntil: t0.Add(time.Minute), Source: "statusline"},
		"none":  {},
		"open":  {Value: 3, ObservedAt: t0, Source: "stream"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(in)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var out Cell[float64]
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatalf("Unmarshal %s: %v", b, err)
			}
			if out.Value != in.Value || out.Source != in.Source ||
				!out.ObservedAt.Equal(in.ObservedAt) || !out.ValidUntil.Equal(in.ValidUntil) {
				t.Errorf("round trip %s = %+v, want %+v", b, out, in)
			}
		})
	}

	b, _ := json.Marshal(Cell[int]{})
	if string(b) != `{}` {
		t.Errorf("none cell encodes as %s, want {} (no zero value on the wire)", b)
	}
}
