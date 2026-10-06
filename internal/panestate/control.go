package panestate

// ControlResult is the outcome of running one pattern against its own sample.
type ControlResult struct {
	PatternID      string
	PatternVersion int
	Harness        string
	HarnessVersion string
	Pass           bool
	// State and Confidence are what Classify returned. They are set on every
	// result that got as far as a classification, and read as a failure
	// reason when Pass is false.
	State      State
	Confidence Confidence
	// Err is the read or normalize error, set when the sample could not be
	// turned into rows.
	Err error
}

// Control runs each pattern against its own stored sample, one result per
// pattern, in order (docs/design/watchdog-control-and-uncovered.md section 2).
// The sample is read, normalized and classified through the same Classify a
// pane goes through, with only that pattern in the set and its own harness
// version as the session version. A pattern passes on logged-out at high
// confidence naming that pattern. Anything else fails it, an unreadable
// sample included.
//
// The control proves a pattern loads and matches its own sample. It cannot
// detect a harness upgrade, because the sample never changes.
func Control(sets []Pattern, sample func(Pattern) (string, error)) []ControlResult {
	out := make([]ControlResult, 0, len(sets))
	for _, p := range sets {
		res := ControlResult{
			PatternID: p.ID, PatternVersion: p.Version,
			Harness: p.Harness, HarnessVersion: p.HarnessVersion,
		}
		raw, err := sample(p)
		if err != nil {
			res.Err = err
			out = append(out, res)
			continue
		}
		rows, err := Normalize(p.ID, raw)
		if err != nil {
			res.Err = err
			out = append(out, res)
			continue
		}
		got := Classify([]Pattern{p}, p.Harness, p.HarnessVersion, rows)
		res.State, res.Confidence = got.State, got.Confidence
		res.Pass = got.State == StateLoggedOut && got.Confidence == ConfHigh && got.PatternID == p.ID
		out = append(out, res)
	}
	return out
}
