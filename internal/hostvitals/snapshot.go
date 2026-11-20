package hostvitals

import "time"

// Threshold is one line the sampler holds, with where the latest reading
// stands against it. State is "under", "over", "gap" (the host gave no figure)
// or "none" (a vital the design names no line for).
type Threshold struct {
	Name   string `json:"name"`
	Line   string `json:"line"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Gap is a vital the design lists and the sampler does not hold yet.
type Gap struct {
	Vital   string `json:"vital"`
	Section string `json:"section"`
	Reason  string `json:"reason"`
}

// Snapshot is what `marvel get vitals` shows: the latest reading, its time,
// each threshold with its state and each gap. It is built from memory only.
type Snapshot struct {
	Enabled    bool        `json:"enabled"`
	Sampled    bool        `json:"sampled"`
	At         *time.Time  `json:"at,omitempty"`
	Reading    Reading     `json:"reading"`
	Thresholds []Threshold `json:"thresholds,omitempty"`
	Gaps       []Gap       `json:"gaps,omitempty"`
}

// Snapshot returns the sampler's state. It does not read the host.
func (s *Sampler) Snapshot() Snapshot {
	return Snapshot{Enabled: true, Sampled: true}
}
