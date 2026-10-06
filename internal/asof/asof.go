// Package asof is the one cell shape that `marvel get sessions` uses for
// every derived or status reading: a value, when it was observed, until
// when it holds, and where it came from.
//
// A cell renders in three states. A value means the reading is fresh. A
// question mark means it expired, and the expired number is never shown. A
// dash means it was never observed, and a cell with no sample never prints
// zero. The state is computed from the clock at render time and is not
// stored, so a cell read off the wire ages correctly.
//
// The cell is diagnostic only. No value here gates anything (aae-orc
// SOUL section 8). Design: docs/design/get-sessions-output.md, section 3.
package asof

import "time"

// State is the freshness word of a cell.
type State string

const (
	// Fresh means the reading is observed and has not expired.
	Fresh State = "fresh"
	// Stale means the reading was observed and has expired.
	Stale State = "stale"
	// None means the reading was never observed.
	None State = "none"
)

// Rendered marks for the two cells that carry no value.
const (
	// DashNone is printed for a cell never observed.
	DashNone = "-"
	// MarkStale is printed for a cell whose reading expired.
	MarkStale = "?"
)

// Cell is a reading with its age. A zero ObservedAt means never observed,
// whatever Value holds. A zero ValidUntil means the reading does not
// expire. Source names where the reading came from, for a cell whose
// source can be weaker than its name claims.
type Cell[T any] struct {
	Value      T         `json:"value,omitzero"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	ValidUntil time.Time `json:"valid_until,omitzero"`
	Source     string    `json:"source,omitempty"`
}

// State reports the cell's freshness at now. A reading is still fresh at
// exactly ValidUntil.
func (c Cell[T]) State(now time.Time) State {
	switch {
	case c.ObservedAt.IsZero():
		return None
	case !c.ValidUntil.IsZero() && now.After(c.ValidUntil):
		return Stale
	default:
		return Fresh
	}
}

// Render prints the cell at now. format renders a fresh value and is never
// called for a stale or absent one.
func (c Cell[T]) Render(now time.Time, format func(T) string) string {
	switch c.State(now) {
	case None:
		return DashNone
	case Stale:
		return MarkStale
	default:
		return format(c.Value)
	}
}
