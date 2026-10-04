package panestate

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func fixtureSet(t *testing.T) []Pattern {
	t.Helper()
	sets, err := Load(os.DirFS("testdata"), ".")
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("fixture patterns = %d, want 1", len(sets))
	}
	return sets
}

func rows(s string) []string {
	r, err := Normalize("%1", s)
	if err != nil {
		panic(err)
	}
	return r
}

const sample = "Synthetic harness: not signed in\nOpen https://example.invalid/device?code=ABCD-1234 to continue\nPress Enter after signing in\n"

func TestNormalizeTrimsTrailingSpaceAndBlankRows(t *testing.T) {
	got, err := Normalize("%1", "a  \nb\t\n\n   \n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "a|b" {
		t.Fatalf("rows = %q", got)
	}
}

func TestNormalizeRefusesEscapeNamingPaneAndOffsetOnly(t *testing.T) {
	secret := "https://example.invalid/device?code=ABCD-1234"
	_, err := Normalize("%7", "ok\n"+secret+"\x1b[0m\n")
	var esc *EscError
	if !errors.As(err, &esc) {
		t.Fatalf("err = %v, want *EscError", err)
	}
	if esc.Pane != "%7" || esc.Offset != 3+len(secret) {
		t.Fatalf("esc = %+v", esc)
	}
	if strings.Contains(err.Error(), "example.invalid") || strings.Contains(err.Error(), "ABCD") {
		t.Fatalf("error carries row text: %q", err.Error())
	}
}

func TestClassifyHighWhenBlockAtBottomAndVersionEqual(t *testing.T) {
	r := Classify(fixtureSet(t), "claude", "2.1.283", rows(sample))
	if r.State != StateLoggedOut || r.Confidence != ConfHigh {
		t.Fatalf("result = %+v", r)
	}
	want := []string{"Synthetic harness: not signed in", "Open <masked> to continue", "Press Enter after signing in"}
	if strings.Join(r.Evidence, "|") != strings.Join(want, "|") {
		t.Fatalf("evidence = %q", r.Evidence)
	}
	if r.PatternID != "logged-out" || r.PatternVersion != 1 {
		t.Fatalf("pattern = %s@%d", r.PatternID, r.PatternVersion)
	}
}

func TestClassifyBlockNotAtBottomDoesNotMatch(t *testing.T) {
	r := Classify(fixtureSet(t), "claude", "2.1.283", rows(sample+"working on it\nmore output\nand more\n"))
	if r.State == StateLoggedOut {
		t.Fatalf("matched above the last block: %+v", r)
	}
}

func TestClassifyQuotedInTheMiddleDoesNotMatch(t *testing.T) {
	screen := "tool result:\n" + sample + "\nback to work\nstill working\nprompt >\n"
	if r := Classify(fixtureSet(t), "claude", "2.1.283", rows(screen)); r.State == StateLoggedOut {
		t.Fatalf("matched mid-screen: %+v", r)
	}
}

func TestClassifyOtherVersionIsLowAndUnknown(t *testing.T) {
	for _, v := range []string{"2.1.285", ""} {
		r := Classify(fixtureSet(t), "claude", v, rows(sample))
		if r.State != StateUnknown || r.Confidence != ConfLow || len(r.Evidence) != 3 {
			t.Fatalf("version %q: %+v", v, r)
		}
	}
}

func TestClassifyPartialMatchIsLowWithOnlyMatchedPatternRows(t *testing.T) {
	screen := "Synthetic harness: not signed in\nsomething unexpected here with https://example.invalid/x?code=ZZZ\nPress Enter after signing in\n"
	r := Classify(fixtureSet(t), "claude", "2.1.283", rows(screen))
	if r.State != StateUnknown || r.Confidence != ConfLow {
		t.Fatalf("result = %+v", r)
	}
	for _, e := range r.Evidence {
		if strings.Contains(e, "example.invalid") || strings.Contains(e, "unexpected") {
			t.Fatalf("evidence carries a captured row: %q", e)
		}
	}
	if len(r.Evidence) != 2 {
		t.Fatalf("evidence = %q, want the 2 matched pattern rows", r.Evidence)
	}
}

func TestClassifyNoPatternsForHarness(t *testing.T) {
	if r := Classify(fixtureSet(t), "codex", "1.0.0", rows(sample)); r.State != StateUnknown || r.Confidence != "" || len(r.Evidence) != 0 {
		t.Fatalf("result = %+v", r)
	}
	if r := Classify(nil, "claude", "2.1.283", rows(sample)); r.State != StateUnknown || r.Confidence != "" {
		t.Fatalf("result = %+v", r)
	}
	if r := Classify(fixtureSet(t), "claude", "2.1.283", nil); r.State != StateUnknown || r.Confidence != "" {
		t.Fatalf("empty screen: %+v", r)
	}
}

func TestWrappedURLJoinedByCaptureStillMatches(t *testing.T) {
	long := "https://example.invalid/device?code=" + strings.Repeat("A", 300)
	screen := "Synthetic harness: not signed in\nOpen " + long + " to continue\nPress Enter after signing in\n"
	r := Classify(fixtureSet(t), "claude", "2.1.283", rows(screen))
	if r.State != StateLoggedOut {
		t.Fatalf("result = %+v", r)
	}
	for _, e := range r.Evidence {
		if strings.Contains(e, "AAAA") {
			t.Fatalf("evidence carries the url: %q", e)
		}
	}
}

// The rot test: each stored sample matches its own pattern set, and a sample
// edited by one character in a fixed span does not.
func TestEachSampleMatchesItsOwnPatternAndRotFails(t *testing.T) {
	fsys := os.DirFS("testdata")
	sets := fixtureSet(t)
	for _, p := range sets {
		raw, err := Sample(fsys, ".", p)
		if err != nil {
			t.Fatalf("sample for %s %s: %v", p.Harness, p.HarnessVersion, err)
		}
		if r := Classify(sets, p.Harness, p.HarnessVersion, rows(raw)); r.State != StateLoggedOut || r.Confidence != ConfHigh {
			t.Fatalf("own sample: %+v", r)
		}
		rot := strings.Replace(raw, "not signed in", "not signed On", 1)
		if r := Classify(sets, p.Harness, p.HarnessVersion, rows(rot)); r.State == StateLoggedOut {
			t.Fatalf("a one-character edit still matched: %+v", r)
		}
	}
}

func TestShippedPatternsLoadAndMatchTheirSamples(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	for _, p := range sets {
		raw, err := EmbeddedSample(p)
		if err != nil {
			t.Fatalf("sample for %s %s: %v", p.Harness, p.HarnessVersion, err)
		}
		if r := Classify(sets, p.Harness, p.HarnessVersion, rows(raw)); r.State != StateLoggedOut {
			t.Fatalf("shipped sample %s %s: %+v", p.Harness, p.HarnessVersion, r)
		}
	}
}

func TestLoadRefusesMoreThanOneVariableSpanPerRow(t *testing.T) {
	_, err := parseRow("a {{var}} b {{var}}")
	if err == nil {
		t.Fatal("two spans in a row were accepted")
	}
}

// Tripwire: no pattern set ships until probe P-WD1 captures one. A set is a claim
// that a real harness drew that screen; the fixtures here are synthetic. Shipping
// any set under patterns/ must change this test in the same commit, alongside the
// capture record (isolated scratch capture, harness version, pane_current_command)
// that justifies it. The test over the shipped sets passes vacuously with none, so
// this is what keeps an uncaptured fixture from shipping (marvel#557 review).
func TestNoPatternSetShipsWithoutAPWD1Capture(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	if len(sets) != 0 {
		var names []string
		for _, p := range sets {
			names = append(names, p.Harness+"/"+p.HarnessVersion+"/"+p.ID)
		}
		t.Fatalf("pattern sets are shipped (%v) with no P-WD1 capture record. Probe P-WD1 (docs/design/harness-state-watchdog-p1.md section 7) captures the screen on an isolated scratch server; add that record (harness version, pane_current_command, how it was isolated) and update this test with the set", names)
	}
}
