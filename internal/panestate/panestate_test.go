package panestate

import (
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"
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

// A chat-quoted copy of the shipped screen puts text in front of each row, so
// the cursor column grows past its real two characters. It must not read as
// logged-out (marvel#598 review).
func TestShippedPatternRefusesAQuotedCopyOfItsSample(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	for _, p := range sets {
		raw, err := EmbeddedSample(p)
		if err != nil {
			t.Fatalf("sample for %s %s: %v", p.Harness, p.HarnessVersion, err)
		}
		var quoted []string
		for _, l := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
			// Quote only the option rows: the fixed rows above them are kept
			// whole, so the cursor column is the only thing that differs.
			if t := strings.TrimLeft(l, " ❯"); len(t) > 2 && t[0] >= '1' && t[0] <= '3' && t[1] == '.' {
				l = "> " + l
			}
			quoted = append(quoted, l)
		}
		if r := Classify(sets, p.Harness, p.HarnessVersion, rows(strings.Join(quoted, "\n"))); r.State == StateLoggedOut {
			t.Fatalf("a quoted copy of %s %s matched: %+v", p.Harness, p.HarnessVersion, r)
		}
	}
}

func TestVarRunesBoundsTheSpan(t *testing.T) {
	row, err := parseRow("{{var}} 1. x")
	if err != nil {
		t.Fatal(err)
	}
	row.MaxVar = 2
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"   1. x", true},
		{" ❯ 1. x", true},
		{"> ❯ 1. x", false},
		{"    1. x", false},
		{" 1. x", false},
	} {
		if got := row.matches(tc.line); got != tc.want {
			t.Errorf("matches(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
	row.MaxVar = 0
	if !row.matches("anything at all 1. x") {
		t.Error("an unbounded span stopped matching")
	}
}

// A shipped pattern with a variable span must bound it: the span is where a
// quoted or pasted copy gets in.
func TestShippedVariableSpansAreBounded(t *testing.T) {
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	for _, p := range sets {
		for _, r := range p.Rows {
			if r.Var && r.MaxVar == 0 {
				t.Errorf("%s/%s/%s: row %q has an unbounded variable span", p.Harness, p.HarnessVersion, p.ID, r.Render())
			}
		}
	}
}

func TestLoadRefusesANegativeVarRunes(t *testing.T) {
	fsys := fstest.MapFS{"h/1/x.yaml": {Data: []byte("id: x\nharness: h\nharness_version: \"1\"\nvar_runes: -1\nrows:\n  - \"a\"\n")}}
	if _, err := Load(fsys, "."); err == nil {
		t.Fatal("a negative var_runes was accepted")
	}
}

// A pattern row of blank text matches any blank line, so a quiet screen with
// blank rows at a pattern's blank offsets matched two rows of nothing and was
// stored as a low partial match of that pattern, with evidence ["", ""]. Blank
// rows are layout, not evidence: a partial needs at least one matched row that
// carries text.
func TestClassifyBlankRowsAloneAreNotEvidence(t *testing.T) {
	var pattern Pattern
	pattern.ID, pattern.Version, pattern.Harness, pattern.HarnessVersion = "p", 1, "claude", "1.0.0"
	for _, text := range []string{"Header text", "", "Menu {{var}} row", ""} {
		row, err := parseRow(text)
		if err != nil {
			t.Fatal(err)
		}
		pattern.Rows = append(pattern.Rows, row)
	}
	sets := []Pattern{pattern}

	// Only the two blank rows line up: nothing is matched, nothing is stored.
	r := Classify(sets, "claude", "1.0.0", []string{"other", "", "different", ""})
	if r.Confidence != "" || r.PatternID != "" || len(r.Evidence) != 0 {
		t.Fatalf("blank rows alone were stored: %+v", r)
	}

	// A row with text lining up keeps the partial, blank rows and all.
	r = Classify(sets, "claude", "1.0.0", []string{"Header text", "", "different", ""})
	if r.Confidence != ConfLow || r.State != StateUnknown || len(r.Evidence) != 3 || r.Evidence[0] != "Header text" {
		t.Fatalf("a partial with a text row was dropped or changed: %+v", r)
	}
}

// A variable row is text: a partial whose only matched row is a variable row
// is kept, so carriesText cannot count fixed rows alone.
func TestClassifyVariableRowAloneIsEvidence(t *testing.T) {
	var pattern Pattern
	pattern.ID, pattern.Version, pattern.Harness, pattern.HarnessVersion = "p", 1, "claude", "1.0.0"
	for _, text := range []string{"Header text", "Menu {{var}} row"} {
		row, err := parseRow(text)
		if err != nil {
			t.Fatal(err)
		}
		pattern.Rows = append(pattern.Rows, row)
	}

	r := Classify([]Pattern{pattern}, "claude", "1.0.0", []string{"other", "Menu x row"})
	if r.Confidence != ConfLow || r.State != StateUnknown || len(r.Evidence) != 1 {
		t.Fatalf("a partial matched only on a variable row was dropped or changed: %+v", r)
	}
}

// Row.matches holds each part of the match: a fixed row is the whole line, and
// a variable row needs its prefix, its suffix and a non-empty span between them.
func TestRowMatchesEachPart(t *testing.T) {
	fixed, err := parseRow(" abc")
	if err != nil {
		t.Fatal(err)
	}
	variable, err := parseRow("ab{{var}}yz")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		row  Row
		line string
		want bool
	}{
		{"fixed, exact", fixed, " abc", true},
		{"fixed, leading space missing", fixed, "abc", false},
		{"fixed, text before", fixed, "x abc", false},
		{"fixed, text after", fixed, " abcx", false},
		{"variable, a span between", variable, "abXyz", true},
		{"variable, prefix wrong", variable, "xbXyz", false},
		{"variable, suffix wrong", variable, "abXyx", false},
		{"variable, empty span", variable, "abyz", false},
	} {
		if got := tc.row.matches(tc.line); got != tc.want {
			t.Errorf("%s: matches(%q) = %v, want %v", tc.name, tc.line, got, tc.want)
		}
	}
}
