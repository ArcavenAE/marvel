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
)

// recordCostSample adds the total seen at at. A total below the last one is a
// reset (a restart, /clear, a new harness session under the same seat): the
// ring starts again from this value, so no change is ever read across the
// drop. An unchanged total stores nothing.
func recordCostSample(sess *Session, usd float64, at time.Time) {
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
