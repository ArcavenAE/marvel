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
// unless the seat has an activity channel that has reported at least once and
// the ring has watched the whole window, so a restart or a seat marvel cannot
// observe never reads 0%. A declared channel that has never reported leaves
// ContextAt zero and every tick quiet, which is no reading either. A seat with
// a statusline feed takes the provisional API-time reading instead.
func activePctAsOf(s api.Session, now time.Time) asof.Cell[float64] {
	if pct, kind := api.APIActivePct(s, now); kind != api.APIActivityNone {
		return apiActivePctAsOf(s, now, pct, kind)
	}
	t := s.ActiveTicks
	if !t.Observable || !t.Full || t.Total <= 0 || t.At.IsZero() || s.ContextAt.IsZero() {
		return asof.Cell[float64]{}
	}
	return asof.Cell[float64]{
		Value:      100 * float64(t.Active) / float64(t.Total),
		ObservedAt: t.At,
		ValidUntil: t.At.Add(activePctValidity),
		Source:     string(s.ContextSource),
	}
}

// apiActivePctAsOf is the provisional reading of a seat with a statusline feed:
// the share of the last 15 minutes spent in API time, not the tick share. The
// operator's provisional choice; the definition is still being developed. A
// pending reading is empty (a dash) and a stale one is already expired (a ?).
func apiActivePctAsOf(s api.Session, now time.Time, pct float64, kind api.APIActivity) asof.Cell[float64] {
	switch kind {
	case api.APIActivityReading:
		seen := s.APISeenAt
		if seen.IsZero() {
			seen = s.LastHeartbeat
		}
		return asof.Cell[float64]{
			Value:      pct,
			ObservedAt: seen,
			ValidUntil: seen.Add(api.APIActiveWindow),
			Source:     string(s.ContextSource),
		}
	case api.APIActivityStale:
		past := now.Add(-time.Nanosecond)
		return asof.Cell[float64]{ObservedAt: past, ValidUntil: past}
	}
	return asof.Cell[float64]{}
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
