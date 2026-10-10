package panestate

import (
	"strings"
	"testing"
	"testing/fstest"
)

// parkedYAML is a synthetic parked pattern: a harness that stops at a
// permission prompt. It carries one variable row, as a real prompt names a
// directory.
const parkedYAML = `id: ask
version: 1
harness: synth
harness_version: 1.0.0
state: parked
reason: permission
sample_width: 80
var_runes: 40
rows:
  - "| Permission required"
  - "| Access directory {{var}}"
  - "  Allow once   Reject"
`

const parkedScreen = "earlier output\n| Permission required\n| Access directory /some/where\n  Allow once   Reject\n"

func parkedSet(t *testing.T, yaml string) []Pattern {
	t.Helper()
	fsys := fstest.MapFS{"synth/1.0.0/ask.yaml": {Data: []byte(yaml)}}
	sets, err := Load(fsys, ".")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return sets
}

func TestClassifyParkedPatternSetsParkedWithItsReason(t *testing.T) {
	r := Classify(parkedSet(t, parkedYAML), "synth", "1.0.0", rows(parkedScreen))
	if r.State != StateParked || r.Confidence != ConfHigh || r.Reason != "permission" {
		t.Fatalf("result = %+v, want parked/high/permission", r)
	}
	want := "| Permission required\n| Access directory <masked>\n  Allow once   Reject"
	if got := strings.Join(r.Evidence, "\n"); got != want {
		t.Fatalf("evidence = %q, want %q", got, want)
	}
}

func TestClassifyParkedOtherVersionStaysUnknown(t *testing.T) {
	r := Classify(parkedSet(t, parkedYAML), "synth", "1.0.1", rows(parkedScreen))
	if r.State != StateUnknown || r.Confidence != ConfLow {
		t.Fatalf("result = %+v, want unknown/low", r)
	}
}

func TestClassifyParkedPromptAboveLaterOutputDoesNotMatch(t *testing.T) {
	r := Classify(parkedSet(t, parkedYAML), "synth", "1.0.0", rows(parkedScreen+"working again\n"))
	if r.State == StateParked {
		t.Fatalf("parked read from a prompt that scrolled away: %+v", r)
	}
}

func TestClassifyLoggedOutPatternKeepsItsStateWithNoStateField(t *testing.T) {
	r := Classify(fixtureSet(t), "claude", "2.1.283", rows(sample))
	if r.State != StateLoggedOut || r.Reason != "" {
		t.Fatalf("result = %+v, want logged-out with no reason", r)
	}
}

func TestLoadRefusesABadStateOrReason(t *testing.T) {
	cases := map[string]string{
		"unknown state":         strings.Replace(parkedYAML, "state: parked", "state: sleeping", 1),
		"parked with no reason": strings.Replace(parkedYAML, "reason: permission\n", "", 1),
		"unknown reason":        strings.Replace(parkedYAML, "reason: permission", "reason: boredom", 1),
		"reason on logged-out":  strings.Replace(parkedYAML, "state: parked", "state: logged-out", 1),
	}
	for name, y := range cases {
		fsys := fstest.MapFS{"synth/1.0.0/ask.yaml": {Data: []byte(y)}}
		if _, err := Load(fsys, "."); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}

func TestControlPassesAParkedPatternOnItsOwnSample(t *testing.T) {
	sets := parkedSet(t, parkedYAML)
	sample := func(Pattern) (string, error) { return parkedScreen, nil }
	res := Control(sets, sample)
	if len(res) != 1 || !res[0].Pass {
		t.Fatalf("control = %+v, want one pass", res)
	}
	edited := func(Pattern) (string, error) { return strings.Replace(parkedScreen, "Reject", "Cancel", 1), nil }
	if res := Control(sets, edited); res[0].Pass {
		t.Fatalf("control passed a pattern against a sample it does not match: %+v", res[0])
	}
}
