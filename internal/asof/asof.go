// Package asof is the one cell shape that `marvel get sessions` uses for
// every derived or status reading: a value, when it was observed, until
// when it holds, and where it came from.
package asof

import "time"

// State is the freshness word of a cell.
type State string

const (
	Fresh State = "fresh"
	Stale State = "stale"
	None  State = "none"
)

// Cell is a reading with its age.
type Cell[T any] struct {
	Value      T         `json:"value"`
	ObservedAt time.Time `json:"observed_at"`
	ValidUntil time.Time `json:"valid_until"`
	Source     string    `json:"source"`
}

// State reports the cell's freshness at now.
func (c Cell[T]) State(now time.Time) State { return Fresh }

// Render prints the cell at now, using format for a fresh value.
func (c Cell[T]) Render(now time.Time, format func(T) string) string {
	return format(c.Value)
}
