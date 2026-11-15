// Package timing records how long the daemon's measured sites take: a small
// fixed-bucket histogram per site, and a per-site throttle for the overrun
// events a site raises when a call runs past its budget. Nothing here starts a
// goroutine or reads the clock on its own; callers pass durations in and the
// throttle takes its clock as an argument.
package timing

import (
	"sync"
	"time"
)

// OverrunEventInterval is how often one site may emit an overrun event.
const OverrunEventInterval = 10 * time.Minute

// bounds is read only. The last entry, 30s, is the design cap: tmux calls are
// cut off at 10s, and nothing the daemon times is expected to take longer.
var bounds = [...]time.Duration{
	1 * time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond,
	10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	1 * time.Second, 2 * time.Second, 5 * time.Second,
	10 * time.Second, 30 * time.Second,
}

// Bounds returns a copy of the upper edge of each finite bucket, smallest
// first, from 1ms to 30s. A duration lands in the first bucket whose bound it
// does not exceed; a longer one lands in the overflow count.
func Bounds() []time.Duration { return append([]time.Duration(nil), bounds[:]...) }

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
type Histogram struct {
	budget time.Duration

	mu       sync.Mutex
	count    uint64
	sum      time.Duration
	max      time.Duration
	overruns uint64
	buckets  [len(bounds)]uint64
	overflow uint64
}

// NewHistogram returns a histogram whose budget is the longest duration that
// does not count as an overrun. A zero budget means the site has none.
func NewHistogram(budget time.Duration) *Histogram { return &Histogram{budget: budget} }

// Observe records one duration and reports whether it ran past the budget. A
// negative duration, which a clock that stepped back can produce, counts as
// zero.
func (h *Histogram) Observe(d time.Duration) (overran bool) {
	if d < 0 {
		d = 0
	}
	overran = h.budget > 0 && d > h.budget
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += d
	if d > h.max {
		h.max = d
	}
	if overran {
		h.overruns++
	}
	for i, b := range bounds {
		if d <= b {
			h.buckets[i]++
			return overran
		}
	}
	h.overflow++
	return overran
}

// Snapshot returns a copy of the histogram's current state.
func (h *Histogram) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Snapshot{
		Count:    h.count,
		Sum:      h.sum,
		Max:      h.max,
		Overruns: h.overruns,
		Buckets:  make([]Bucket, len(bounds)),
		Overflow: h.overflow,
	}
	for i, b := range bounds {
		s.Buckets[i] = Bucket{UpperBound: b, Count: h.buckets[i]}
	}
	return s
}

// Throttle lets one event per site through each interval and counts the ones
// it held back. It is safe for concurrent use.
type Throttle struct {
	interval time.Duration
	now      func() time.Time

	mu    sync.Mutex
	sites map[string]*throttleSite
}

type throttleSite struct {
	last       time.Time
	suppressed uint64
}

// NewThrottle returns a throttle that reads time from now.
func NewThrottle(interval time.Duration, now func() time.Time) *Throttle {
	return &Throttle{interval: interval, now: now, sites: make(map[string]*throttleSite)}
}

// Allow reports whether the caller may emit for site. When it may, suppressed
// is how many calls for the site were held back since the last emit; when it
// may not, suppressed is zero. A clock that stepped back since the last emit
// counts as inside the interval.
func (t *Throttle) Allow(site string) (ok bool, suppressed uint64) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sites[site]
	if st == nil {
		t.sites[site] = &throttleSite{last: now}
		return true, 0
	}
	if d := now.Sub(st.last); d < 0 || d < t.interval {
		st.suppressed++
		return false, 0
	}
	suppressed = st.suppressed
	st.last, st.suppressed = now, 0
	return true, suppressed
}
