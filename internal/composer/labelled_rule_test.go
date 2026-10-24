package composer

import (
	"strings"
	"testing"
)

// Some Claude panes draw the session name inside the rule above the composer,
// for example "──── name ─", so the rule is no longer a bare run of ─ and the
// reader found no composer: every inject to such a seat read Unknown and could
// not be confirmed (aae-orc-jzmvo).
//
// The fixtures are cut from a real capture of an idle seat (capture -e), kept
// to the done line, the rules and the composer, in the bytes the pane drew.
// 7-bare-rule-empty is that capture as it came. 8 and 9 are the same frame with
// a label put into the top rule by hand, because the panes on the host that
// took the capture draw a bare rule: the label "example-session" is inserted,
// not captured. 9 also carries typed text in the composer, likewise inserted.
// The .txt files are the same frames with the escape sequences removed.

// While the label shape stays unseen on a real capture, the rule is read by the
// shape the reports describe: a run of at least ten ─ at the margin, one space,
// a label with no ─ in it, one space, and a trailing run of ─ that ends the line.
const labelled = "────────────────────────────────────────────────────────────── example-session ─"

// framedBy is a finished turn followed by a composer whose top rule is top and
// whose bottom rule is bottom.
func framedBy(top, bottom string) string {
	return "\n\n✻ Worked for 2s · done 5:23 PM\n\n" + top + "\n❯" + nb + "\n" + bottom + "\n  [Model] scratch\n"
}

func TestClaudeReaderReadsALabelledRuleOverAnEmptyComposer(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	for _, name := range []string{"8-labelled-rule-empty.ansi.txt", "8-labelled-rule-empty.txt"} {
		if got := r.Read(fixture(t, name)); got != Empty {
			t.Errorf("%s: Read = %q, want %q", name, got, Empty)
		}
	}
}

func TestClaudeReaderReadsATextOverALabelledRuleAsThatText(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	for _, name := range []string{"9-labelled-rule-typed.ansi.txt", "9-labelled-rule-typed.txt"} {
		if got := r.Read(fixture(t, name)); got != HoldsText {
			t.Errorf("%s: Read = %q, want %q", name, got, HoldsText)
		}
	}
	// A draft that spans lines is still the composer's text.
	multi := framedBy(labelled, rule)
	multi = strings.Replace(multi, "❯"+nb+"\n", "❯"+nb+"line one\n  line two\n", 1)
	if got := r.Read(multi); got != HoldsText {
		t.Errorf("a multi line draft under a labelled rule: Read = %q, want %q", got, HoldsText)
	}
}

// Guard: the bare rule from the same capture reads as before, in both captures.
func TestClaudeReaderStillReadsTheBareRuleFromTheSameCapture(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	for _, name := range []string{"7-bare-rule-empty.ansi.txt", "7-bare-rule-empty.txt"} {
		if got := r.Read(fixture(t, name)); got != Empty {
			t.Errorf("%s: Read = %q, want %q", name, got, Empty)
		}
	}
}

// The closing rule is read by the same shape, so a pane that labels it too
// still has a closed composer. Text at the margin cannot be a draft line (a
// draft's continuation lines are indented), so a rule there is the harness's.
// A label may carry colour; the escape sequences are removed before matching.
func TestClaudeReaderAcceptsALabelledClosingRule(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	cases := map[string]string{
		"both rules labelled":       framedBy(labelled, labelled),
		"only the closing labelled": framedBy(rule, labelled),
		"a coloured label":          framedBy("\x1b[38;5;244m──────────────────────── \x1b[1mexample-session\x1b[22m ─\x1b[39m", rule),
		"a label with punctuation":  framedBy("──────────────────────── example-session (2) ─", rule),
		"a longer trailing run":     framedBy("──────────────────────── example-session ────", rule),
	}
	for name, capture := range cases {
		if got := r.Read(capture); got != Empty {
			t.Errorf("%s: Read = %q, want %q", name, got, Empty)
		}
	}
}

// The reader must still refuse text that only imitates a rule. A reply's text
// is model-controlled and starts with the reply bullet or is indented, so a
// line that is not at the margin, or is not a run of ─ with at most one label
// and a trailing run of ─, does not open the composer.
func TestClaudeReaderRefusesLinesThatImitateALabelledRule(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	cases := map[string]string{
		"indented":                           "  " + labelled,
		"after a reply bullet":               "⏺ " + labelled,
		"after a tool marker":                "⎿ " + labelled,
		"a run shorter than ten":             "───────── example-session ─",
		"no trailing rule":                   "──────────────────────── example-session",
		"no space before the trailing rule":  "──────────────────────── example-session─",
		"no space after the leading run":     "────────────────────────example-session ─",
		"a rule inside the label":            "──────────────────────── example ── session ─",
		"a second label":                     "──────────────────────── one ─ two ─",
		"text before the leading run":        "x ──────────────────────── example-session ─",
		"text after the trailing rule":       "──────────────────────── example-session ─ x",
		"a label with nothing in it":         "────────────────────────  ─",
		"a different dash":                   "-------------------------- example-session -",
		"a lone trailing rule with no label": "──────────────────────── ─",
	}
	for name, top := range cases {
		if got := r.Read(framedBy(top, rule)); got != Unknown {
			t.Errorf("%s: Read = %q, want %q", name, got, Unknown)
		}
		// The same line as the closing rule does not close the composer either.
		if got := r.Read(framedBy(rule, top)); got != Unknown {
			t.Errorf("%s as the closing rule: Read = %q, want %q", name, got, Unknown)
		}
	}
	// A reply that quotes a labelled rule above an ordinary prompt line is not a
	// composer: the prompt has to sit directly under the rule.
	quoted := "\n✻ Worked for 2s · done 5:23 PM\n⏺ " + labelled + "\n❯" + nb + "\n" + rule + "\n"
	if got := r.Read(quoted); got != Unknown {
		t.Errorf("a quoted labelled rule: Read = %q, want %q", got, Unknown)
	}
}

// Claude draws "Press up to edit queued messages" while a message is queued
// behind a running turn. The line is a mark line: a grey (38;5;246) prompt
// glyph, a no-break space, then the hint in dim, as the pane drew it (the
// captured bytes are quoted in queuedMarkANSI; queuedMarkPlain is the same line
// with the escape sequences removed, as the .txt fixtures are). Its position is
// still assumed, between the spinner and the composer, because taking a full
// frame would mean typing into a pane. The reader has no state of its own for
// it, and the mark line hides the spinner from the line-above rule, so what the
// code can honestly report is that the frame is not confirmed: a frame the
// reader cannot place reads Unknown, which confirms nothing.
func TestClaudeReaderDoesNotConfirmAFrameWithTheQueuedBanner(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	const (
		queuedMarkANSI  = "\x1b[38;5;246m❯\u00a0\x1b[2m\x1b[39mPress up to edit queued messages\x1b[0m"
		queuedMarkPlain = "❯\u00a0Press up to edit queued messages"
	)
	for _, name := range []string{"3a-mid-turn-1s5.txt", "3a-mid-turn-1s5.ansi.txt"} {
		for label, top := range map[string]string{"bare rule": "", "labelled rule": labelled} {
			banner := queuedMarkPlain
			if strings.HasSuffix(name, ".ansi.txt") {
				banner = queuedMarkANSI
			}
			f := insertAfter(t, fixture(t, name), "Boogieing", banner)
			if top != "" {
				lines := strings.Split(f, "\n")
				for i, l := range lines {
					if strings.Contains(l, "────────────") {
						lines[i] = labelled
						break
					}
				}
				f = strings.Join(lines, "\n")
			}
			got := r.Read(f)
			if Confirms(Submit, got) || Confirms(Stage, got) {
				t.Errorf("%s with %s and the queued banner: Read = %q confirms an inject", name, label, got)
			}
			if got != Unknown {
				t.Errorf("%s with %s and the queued banner: Read = %q, want %q", name, label, got, Unknown)
			}
		}
	}
}

// Fixture 10 is a real capture, not a hand-built frame: a read-only capture -e
// of an idle seat on another host (2026-10-24), cut like fixture 8 (the done
// line, one blank line standing in for an omitted banner line, the labelled
// rule, the composer and the bare rule). The label is replaced with
// "example-session" and the run of ─ is shortened to 80 columns. The composer
// holds a dim suggestion (SGR 2), not a draft.
//
// A labelled rule must read the way a bare rule does for the same composer, so
// the verdicts are those of fixture 5 (the dim suggestion under a bare rule):
// the colored capture reads Unknown, because the reader sees the dim, and the
// plain one reads HoldsText, because a plain capture cannot see dim. Neither
// reads Empty, and neither confirms a stage or a submit.
func TestClaudeReaderReadsACapturedDimSuggestionUnderALabelledRuleLikeABareOne(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	for name, want := range map[string]State{
		"10-labelled-rule-suggestion.ansi.txt": Unknown,
		"10-labelled-rule-suggestion.txt":      HoldsText,
	} {
		got := r.Read(fixture(t, name))
		if got != want {
			t.Errorf("%s: Read = %q, want %q", name, got, want)
		}
		if bare := r.Read(fixture(t, strings.Replace(name, "10-labelled-rule-suggestion", "5-idle-suggestion", 1))); got != bare {
			t.Errorf("%s: Read = %q, but the bare-rule suggestion reads %q", name, got, bare)
		}
	}
	if state := r.Read(fixture(t, "10-labelled-rule-suggestion.ansi.txt")); Confirms(Submit, state) || Confirms(Stage, state) {
		t.Errorf("the dim suggestion under a labelled rule reads %q, which confirms an inject", state)
	}
}
