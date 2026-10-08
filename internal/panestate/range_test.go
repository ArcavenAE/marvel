package panestate

import (
	"strings"
	"testing"
	"testing/fstest"
)

// rangePattern is a pattern captured at 2.1.290 that covers 2.1.285 to 2.1.293.
func rangePattern() Pattern {
	return Pattern{ID: "logged-out", Harness: "claude", HarnessVersion: "2.1.290", MinVersion: "2.1.285", MaxVersion: "2.1.293"}
}

// A range is inclusive at both ends and compared by dotted numeric parts, so
// 2.1.10 sorts above 2.1.9 where a string compare would put it below.
func TestCoversIsInclusiveAndNumeric(t *testing.T) {
	p := rangePattern()
	for v, want := range map[string]bool{
		"2.1.284": false, "2.1.285": true, "2.1.286": true, "2.1.290": true, "2.1.293": true, "2.1.294": false,
		"2.1.1000": false, "2.2.0": false, "2.0.290": false, "claude": false, "": false, "2.1": false, "2.1.290.1": false,
	} {
		if got := p.Covers(v); got != want {
			t.Errorf("Covers(%q) = %v, want %v", v, got, want)
		}
	}
	wide := Pattern{HarnessVersion: "2.1.9", MinVersion: "2.1.9", MaxVersion: "2.1.10"}
	if !wide.Covers("2.1.10") || !wide.Covers("2.1.9") {
		t.Error("2.1.10 must sort above 2.1.9: a range 2.1.9 to 2.1.10 covers both")
	}
	if narrow := (Pattern{HarnessVersion: "2.1.2", MinVersion: "2.1.2", MaxVersion: "2.1.9"}); narrow.Covers("2.1.10") {
		t.Error("2.1.10 is above 2.1.9 and outside a range ending there")
	}
}

// With no range the pattern is exact, as before.
func TestCoversWithoutARangeIsExact(t *testing.T) {
	p := Pattern{HarnessVersion: "2.1.290"}
	if !p.Covers("2.1.290") || p.Covers("2.1.291") || p.Covers("2.1.289") || p.Covers("") {
		t.Error("a pattern with no range covers exactly its own version")
	}
	if p.VersionLabel() != "2.1.290" {
		t.Errorf("label = %q, want the version", p.VersionLabel())
	}
	if got := rangePattern().VersionLabel(); got != "2.1.285-2.1.293" {
		t.Errorf("range label = %q, want 2.1.285-2.1.293", got)
	}
}

// Inside the range a full match is high and sets logged-out; outside it is low
// and reads unknown, which is the fail-safe above and below the bounds.
func TestClassifyHighInsideRangeLowOutside(t *testing.T) {
	p := rangePattern()
	p.Rows = []Row{{Prefix: "Press Enter after signing in"}}
	screen := rows("Press Enter after signing in\n")
	for v, want := range map[string]State{
		"2.1.285": StateLoggedOut, "2.1.288": StateLoggedOut, "2.1.293": StateLoggedOut,
		"2.1.284": StateUnknown, "2.1.294": StateUnknown,
	} {
		got := Classify([]Pattern{p}, "claude", v, screen)
		if got.State != want {
			t.Errorf("version %s: state %s, want %s", v, got.State, want)
		}
		if want == StateUnknown && got.Confidence != ConfLow {
			t.Errorf("version %s: confidence %s, want low", v, got.Confidence)
		}
	}
}

const rangeYAML = "id: x\nharness: h\nharness_version: \"2.1.290\"\n%s\nrows:\n  - \"a\"\n"

func loadRange(t *testing.T, extra string) ([]Pattern, error) {
	t.Helper()
	body := strings.Replace(rangeYAML, "%s", extra, 1)
	return Load(fstest.MapFS{"h/2.1.290/x.yaml": {Data: []byte(body)}}, ".")
}

func TestLoadReadsAVersionRange(t *testing.T) {
	sets, err := loadRange(t, "version_range:\n  min: \"2.1.285\"\n  max: \"2.1.293\"")
	if err != nil || len(sets) != 1 {
		t.Fatalf("load: %v, %d sets", err, len(sets))
	}
	if sets[0].MinVersion != "2.1.285" || sets[0].MaxVersion != "2.1.293" || sets[0].HarnessVersion != "2.1.290" {
		t.Errorf("pattern = %+v", sets[0])
	}
	plain, err := loadRange(t, "")
	if err != nil || plain[0].MinVersion != "" || plain[0].MaxVersion != "" {
		t.Errorf("no range: %v, %+v", err, plain)
	}
}

// A range that cannot be read as a range, or that leaves out the version the
// sample was captured from, is refused at load.
func TestLoadRefusesABadVersionRange(t *testing.T) {
	for name, extra := range map[string]string{
		"min only":           "version_range:\n  min: \"2.1.285\"",
		"max only":           "version_range:\n  max: \"2.1.293\"",
		"min above max":      "version_range:\n  min: \"2.1.293\"\n  max: \"2.1.285\"",
		"sample below range": "version_range:\n  min: \"2.1.291\"\n  max: \"2.1.293\"",
		"sample above range": "version_range:\n  min: \"2.1.285\"\n  max: \"2.1.289\"",
		"not a version":      "version_range:\n  min: \"old\"\n  max: \"2.1.293\"",
	} {
		if _, err := loadRange(t, extra); err == nil {
			t.Errorf("%s: load accepted %q", name, extra)
		}
	}
}

// The control runs a range pattern against its own sample at the sampled
// version, so a range pattern passes it like any other.
func TestRangePatternPassesItsControl(t *testing.T) {
	p := rangePattern()
	p.Rows = []Row{{Prefix: "Press Enter after signing in"}}
	res := Control([]Pattern{p}, func(Pattern) (string, error) { return "Press Enter after signing in\n", nil })
	if len(res) != 1 || !res[0].Pass {
		t.Fatalf("control = %+v, want a pass", res)
	}
}

// The shipped claude set covers the versions that were sampled, 2.1.285 to
// 2.1.293, and nothing outside them.
func TestShippedLoggedOutCoversTheSampledVersions(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EmbeddedSample(sets[0])
	if err != nil {
		t.Fatal(err)
	}
	screen := rows(raw)
	for v, want := range map[string]bool{
		"2.1.283": false, "2.1.284": false, "2.1.285": true, "2.1.288": true, "2.1.290": true,
		"2.1.293": true, "2.1.294": false, "2.2.0": false,
	} {
		got := Classify(sets, "claude", v, screen).State == StateLoggedOut
		if got != want {
			t.Errorf("claude %s: logged-out = %v, want %v", v, got, want)
		}
	}
}
