package main

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/usage"
)

// rateClock is the clock the RATE cell and its sort read, a seam for tests.
var rateClock = time.Now

// sessionRate is the session's output-token rate as the cell shows it at
// now, with the state that says how to print it. It goes through
// usage.QuietRate: a decaying value while fresh, exactly 0 for a seat that
// went quiet, expired once the reading is past its validity while the seat
// is still reporting, and absent for a seat never sampled.
//
// The window is the default quiet window, because the client cannot see a
// role's own activity_timeout or the cluster's watchdog.window. The channel
// is always present: a rate cell exists only for a seat whose token stream
// the accountant reads.
func sessionRate(s api.Session, now time.Time) (float64, asof.State) {
	return usage.QuietRate(s.OutRate, &s, api.DefaultQuietWindow, true, now)
}

// rateCell renders the RATE cell: tokens a second, "?" once the reading
// expired, and "-" for a seat never sampled, never a zero it did not measure.
func rateCell(s api.Session, now time.Time) string {
	v, state := sessionRate(s, now)
	switch state {
	case asof.None:
		return asof.DashNone
	case asof.Stale:
		return asof.MarkStale
	}
	switch {
	case v == 0:
		return "0/s"
	case v < 100:
		return fmt.Sprintf("%.1f/s", v)
	default:
		return fmt.Sprintf("%.0f/s", v)
	}
}

// rateSortKey orders sessions by the number the RATE cell shows. A seat with
// no number (never sampled, or expired) sorts below every number, 0 included.
func rateSortKey(s api.Session, now time.Time) float64 {
	v, state := sessionRate(s, now)
	if state != asof.Fresh {
		return -1
	}
	return v
}
