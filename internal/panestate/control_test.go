package panestate

import (
	"errors"
	"os"
	"testing"
)

// Every pattern the binary ships matches its own sample at high confidence.
// This is the release guard: a pattern that stopped matching its sample fails
// here, before it ships to a fleet that would read it as "nothing matched".
func TestEveryShippedPatternPassesItsControl(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	results := Control(sets, EmbeddedSample)
	if len(results) == 0 {
		t.Fatal("Control returned no results over the shipped patterns, so the guard guards nothing")
	}
	if len(results) != len(sets) {
		t.Fatalf("results = %d, want one per pattern (%d)", len(results), len(sets))
	}
	for _, r := range results {
		if !r.Pass {
			t.Errorf("shipped pattern %s v%d (%s %s) failed its control: state=%q confidence=%q err=%v",
				r.PatternID, r.PatternVersion, r.Harness, r.HarnessVersion, r.State, r.Confidence, r.Err)
		}
	}
}

// The guard is not green at red: a pattern whose fixed row was edited fails,
// and an intact pattern beside it passes, each result naming its pattern.
func TestControlFailsAnEditedRowAndPassesAnIntactOne(t *testing.T) {
	intact := fixtureSet(t)[0]
	edited := intact
	edited.ID = "edited"
	edited.Rows = append([]Row(nil), intact.Rows...)
	edited.Rows[0].Prefix += " (edited)"
	sample := func(p Pattern) (string, error) {
		// The edited pattern keeps the intact pattern's sample.
		return Sample(os.DirFS("testdata"), ".", intact)
	}

	results := Control([]Pattern{intact, edited}, sample)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	byID := map[string]ControlResult{}
	for _, r := range results {
		byID[r.PatternID] = r
	}
	if r := byID[intact.ID]; !r.Pass || r.Err != nil {
		t.Errorf("intact pattern = %+v, want a pass", r)
	}
	r := byID["edited"]
	if r.Pass {
		t.Fatalf("edited pattern passed its control: %+v", r)
	}
	if r.State == StateLoggedOut && r.Confidence == ConfHigh {
		t.Errorf("a failed result carries a passing classification: %+v", r)
	}
	if r.Harness != "claude" || r.HarnessVersion != "2.1.283" || r.PatternVersion != intact.Version {
		t.Errorf("result does not name its pattern: %+v", r)
	}
}

// A sample that cannot be read fails the pattern and carries the error.
func TestControlFailsAnUnreadableSampleWithTheError(t *testing.T) {
	p := fixtureSet(t)[0]
	boom := errors.New("sample unreadable")
	results := Control([]Pattern{p}, func(Pattern) (string, error) { return "", boom })
	if len(results) != 1 || results[0].Pass {
		t.Fatalf("results = %+v, want one failed result", results)
	}
	if !errors.Is(results[0].Err, boom) {
		t.Errorf("Err = %v, want the read error", results[0].Err)
	}
}

// A sample that holds an escape byte cannot be normalized and fails the pattern.
func TestControlFailsASampleThatCannotBeNormalized(t *testing.T) {
	p := fixtureSet(t)[0]
	results := Control([]Pattern{p}, func(Pattern) (string, error) { return "a\x1b[0m\n", nil })
	if len(results) != 1 || results[0].Pass || results[0].Err == nil {
		t.Fatalf("results = %+v, want one failed result with an error", results)
	}
}
