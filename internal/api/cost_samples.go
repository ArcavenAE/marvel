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

func recordCostSample(sess *Session, usd float64, at time.Time) {}
