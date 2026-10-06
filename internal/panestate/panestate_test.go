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

// Tripwire: a pattern set ships only with a P-WD1 capture record, and this test
// names every shipped set with its record. Shipping another set must change this
// test in the same commit (marvel#557 review). The test over the shipped sets
// passes vacuously with none, so TestShippedPatternSetsAreNotEmpty pins that the
// watchdog does not go quietly off again (marvel#598).
//
// claude/2.1.290/logged-out, captured 2026-10-06 under probe P-WD1
// (docs/design/harness-state-watchdog-p1.md section 7): a scratch tmux server
// (`tmux -L wd1-scratch`), claude 2.1.290 started under `env -i` with an empty
// scratch HOME and an empty scratch CLAUDE_CONFIG_DIR, no credentials in the
// environment, pane width 120, pane_current_command "2.1.290". The first run
// showed the theme step, one Enter later the login-method picker, which is the
// sample; the first capture showed no logged-in prompt, so the isolation held.
// No live seat's pane, config or credentials were read.
func TestOnlyCapturedPatternSetsShip(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	var names []string
	for _, p := range sets {
		names = append(names, p.Harness+"/"+p.HarnessVersion+"/"+p.ID)
	}
	if got, want := strings.Join(names, ","), "claude/2.1.290/logged-out"; got != want {
		t.Fatalf("shipped pattern sets = %q, want %q. A set is a claim that a real harness drew that screen: add its P-WD1 capture record (harness version, pane_current_command, how it was isolated) to this test's comment and name it here", got, want)
	}
}

// With no set embedded the daemon starts no watchdog loop and `get sessions`
// gives no sign the fleet is unwatched (marvel#598).
func TestShippedPatternSetsAreNotEmpty(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	if len(sets) == 0 {
		t.Fatal("LoadEmbedded returned no pattern set, so the watchdog starts no loop")
	}
}
