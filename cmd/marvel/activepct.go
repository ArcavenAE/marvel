package main

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// activePctValidity is how long a reading stays fresh after the newest tick it
// counted. The reading moves every reconcile tick, so a reading much older than
// a few ticks means the daemon stopped counting.
const activePctValidity = 30 * time.Second

// activePctAsOf is the ACTIVE% reading as an as-of cell: the share of the last
// fifteen minutes of ticks that found the seat not quiet. It is empty (a dash)
// unless the seat has an activity channel and the ring has watched the whole
// window, so a restart or a seat marvel cannot observe never reads 0%.
func activePctAsOf(s api.Session, _ time.Time) asof.Cell[float64] {
	t := s.ActiveTicks
	if !t.Observable || !t.Full || t.Total <= 0 || t.At.IsZero() {
		return asof.Cell[float64]{}
	}
	return asof.Cell[float64]{
		Value:      100 * float64(t.Active) / float64(t.Total),
		ObservedAt: t.At,
		ValidUntil: t.At.Add(activePctValidity),
		Source:     string(s.ContextSource),
	}
}

// activePctCell renders the ACTIVE% cell. It claims "not quiet", not "busy",
// and carries the source mark where a statusline or heartbeat reported it.
func activePctCell(s api.Session, now time.Time) string {
	c := activePctAsOf(s, now)
	out := c.Render(now, func(v float64) string { return fmt.Sprintf("%.0f%%", v) })
	if c.State(now) == asof.Fresh && c.Source == string(api.ContextSourceHeartbeat) {
		out += sourceMark
	}
	return out
}
