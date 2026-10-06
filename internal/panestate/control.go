package panestate

import "errors"

// ControlResult is the outcome of running one pattern against its own sample.
type ControlResult struct {
	PatternID      string
	PatternVersion int
	Harness        string
	HarnessVersion string
	Pass           bool
	// State and Confidence are what the classification returned, set on a
	// failure that was a classification.
	State      State
	Confidence Confidence
	// Err is the read error, set when the sample could not be read or
	// normalized.
	Err error
}

// Control runs each pattern against its own stored sample through Classify
// and reports one result per pattern (docs/design/watchdog-control-and-uncovered.md
// section 2).
func Control(sets []Pattern, sample func(Pattern) (string, error)) []ControlResult {
	out := make([]ControlResult, 0, len(sets))
	for _, p := range sets {
		_ = sample
		out = append(out, ControlResult{
			PatternID: p.ID, PatternVersion: p.Version, Harness: p.Harness,
			HarnessVersion: p.HarnessVersion, Err: errors.New("control not built"),
		})
	}
	return out
}
