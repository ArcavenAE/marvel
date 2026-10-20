package api

import "time"

// CostSample is one stored value of the statusline's running cost total and
// when it was first seen. The samples let a reader compute the change in the
// total over a window without a transcript (marvel#801).
type CostSample struct {
	At  time.Time `json:"at"`
	USD float64   `json:"usd"`
}

const (
	// CostSampleKeep is how far back samples are kept, plus one older anchor.
	// A RATE window longer than this reads as not yet measured.
	CostSampleKeep = time.Hour
	// costSampleSpacing is the least gap between two stored samples. A beat
	// inside it replaces the last sample's value, so a busy seat's ring stays
	// a few dozen entries and the window start is known to within this gap.
	costSampleSpacing = 30 * time.Second
	costSampleCap     = 128
	// CostGapLimit is the longest quiet between two beats that carry the total
	// that still counts as one run. The feed ticks every 15 seconds, so more
	// than this is a silence (a stopped seat, a daemon that was down).
	CostGapLimit = 3 * time.Minute
)

// recordCostSample adds the total seen at at. A total below the last one is a
// reset (a restart, /clear, a new harness session under the same seat): the
// ring starts again from this value, so no change is ever read across the
// drop. An unchanged total stores no sample, but every beat stamps CostSeenAt,
// so a silence longer than CostGapLimit tells itself apart from a flat run: it
// starts the ring over and stamps CostGapAt, and RATE refuses a window with the
// gap in it instead of putting the spend of the whole silence into one window.
func recordCostSample(sess *Session, usd float64, at time.Time) {
	gap := !sess.CostSeenAt.IsZero() && at.Sub(sess.CostSeenAt) > CostGapLimit
	sess.CostSeenAt = at
	if gap {
		sess.CostSamples = []CostSample{{At: at, USD: usd}}
		sess.CostGapAt = at
		return
	}
	n := len(sess.CostSamples)
	switch {
	case n == 0 || usd < sess.CostSamples[n-1].USD:
		sess.CostSamples = []CostSample{{At: at, USD: usd}}
		return
	case usd == sess.CostSamples[n-1].USD:
		return
	case at.Sub(sess.CostSamples[n-1].At) < costSampleSpacing:
		sess.CostSamples[n-1].USD = usd
	default:
		sess.CostSamples = append(sess.CostSamples, CostSample{At: at, USD: usd})
	}
	cutoff := at.Add(-CostSampleKeep)
	drop := 0
	for drop+1 < len(sess.CostSamples) && !sess.CostSamples[drop+1].At.After(cutoff) {
		drop++
	}
	if over := len(sess.CostSamples) - drop - costSampleCap; over > 0 {
		drop += over
	}
	sess.CostSamples = append([]CostSample(nil), sess.CostSamples[drop:]...)
}

// APISample is one stored value of the statusline's running API-time total
// (milliseconds spent waiting on model responses) and when it was first seen.
type APISample struct {
	At time.Time `json:"at"`
	MS int64     `json:"ms"`
}

// APIActiveWindow is the span ACTIVE% reads for a seat with a statusline feed.
const APIActiveWindow = 15 * time.Minute

// APIActivity says what the API-time reading of a seat amounts to.
type APIActivity int

const (
	// APIActivityNone: the seat has no statusline feed, so ACTIVE% is the tick
	// reading.
	APIActivityNone APIActivity = iota
	// APIActivityPending: a feed seat with no window of samples yet.
	APIActivityPending
	// APIActivityStale: the feed is silent past the window, or a gap sits in it.
	APIActivityStale
	// APIActivityReading: the percentage is valid.
	APIActivityReading
)

func recordAPISample(sess *Session, ms int64, at time.Time) {}

// APIActivePct is the provisional ACTIVE% of a seat with a statusline feed.
func APIActivePct(s Session, now time.Time) (float64, APIActivity) {
	return 0, APIActivityNone
}
