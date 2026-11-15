// Package timing records how long the daemon's measured sites take: a small
// fixed-bucket histogram per site, and a per-site throttle for the overrun
// events a site raises when a call runs past its budget. Nothing here starts a
// goroutine or reads the clock on its own; callers pass durations in and the
// throttle takes its clock as an argument.
package timing

import "time"

// OverrunEventInterval is how often one site may emit an overrun event.
const OverrunEventInterval = 10 * time.Minute

// Bounds returns the upper edge of each finite bucket, smallest first. A
// duration lands in the first bucket whose bound it does not exceed; a longer
// one lands in the overflow count.
func Bounds() []time.Duration { return nil }

// Bucket is one finite bucket of a Snapshot.
type Bucket struct {
	UpperBound time.Duration
	Count      uint64
}

// Snapshot is a copy of a Histogram at one moment.
type Snapshot struct {
	Count    uint64
	Sum      time.Duration
	Max      time.Duration
	Overruns uint64
	Buckets  []Bucket
	Overflow uint64
}

// Histogram counts durations into fixed buckets for one site. It is safe for
// concurrent use.
type Histogram struct{}

// NewHistogram returns a histogram whose budget is the longest duration that
// does not count as an overrun. A zero budget means the site has none.
func NewHistogram(budget time.Duration) *Histogram { return &Histogram{} }

// Observe records one duration and reports whether it ran past the budget.
func (h *Histogram) Observe(d time.Duration) (overran bool) { return false }

// Snapshot returns a copy of the histogram's current state.
func (h *Histogram) Snapshot() Snapshot { return Snapshot{} }

// Throttle lets one event per site through each interval and counts the ones
// it held back.
type Throttle struct{}

// NewThrottle returns a throttle that reads time from now.
func NewThrottle(interval time.Duration, now func() time.Time) *Throttle { return &Throttle{} }

// Allow reports whether the caller may emit for site. When it may, suppressed
// is how many calls for the site were held back since the last emit.
func (t *Throttle) Allow(site string) (ok bool, suppressed uint64) { return false, 0 }
