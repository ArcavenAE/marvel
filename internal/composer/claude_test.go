package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are real captures of Claude Code v2.1.288 in a scratch sandbox,
// scrubbed (internal/composer/testdata/claude): .txt is capture-pane -p, and
// .ansi.txt is capture-pane -p -e.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClaudeReaderReadsTheCapturedStates(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	cases := []struct {
		name      string
		plain, an State
	}{
		// Plain cannot tell the dim placeholder from typed text, so it is unknown.
		{"1-empty-idle", Unknown, Empty},
		{"2-staged-draft", HoldsText, HoldsText},
		{"3a-mid-turn-1s5", MidTurn, MidTurn},
		// Text streaming: no spinner and an empty composer. That is neither
		// idle nor empty, so the reader says unknown.
		{"3b-mid-turn-3s", Unknown, Unknown},
		{"3c-mid-turn-6s", Unknown, Unknown},
		{"4-idle-after-submit", Empty, Empty},
	}
	for _, tc := range cases {
		if got := r.Read(fixture(t, tc.name+".txt")); got != tc.plain {
			t.Errorf("%s plain: Read = %q, want %q", tc.name, got, tc.plain)
		}
		if got := r.Read(fixture(t, tc.name+".ansi.txt")); got != tc.an {
			t.Errorf("%s with escapes: Read = %q, want %q", tc.name, got, tc.an)
		}
	}
}

func TestClaudeReaderContract(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	if r.Name() != "claude" || !r.Escapes() || r.Preflight() {
		t.Errorf("claude reader = name %q escapes %v preflight %v, want claude, escapes, no preflight", r.Name(), r.Escapes(), r.Preflight())
	}
}

const (
	rule = "──────────────────────────────"
	nb   = " "
)

func frame(above string, composer ...string) string {
	return "\n\n" + above + "\n" + rule + "\n" + strings.Join(composer, "\n") + "\n" + rule + "\n  [Model] scratch\n"
}

func TestClaudeReaderEdges(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	dim := "\x1b[2m"
	cases := []struct {
		name    string
		capture string
		want    State
	}{
		{"no composer on screen", "OpenAI Codex\n\n  Update available\n", Unknown},
		{"an empty capture", "", Unknown},
		{"only the echo of a submitted prompt", "❯ Reply with one word\n\n⏺ ok\n", Unknown},
		{"the echo above is not the composer", frame("❯ earlier prompt\n\n⏺ ok\n\n✻ Worked for 2s · done 5:23 PM", "❯"+nb), Empty},
		{"a spinner with another verb and a token count", frame("✶ Pondering… (12s · ↓ 300 tokens · thinking)", "❯"+nb), MidTurn},
		{"a spinner with text typed ahead is the harness busy", frame("✳ Cooking… (3s · thinking)", "❯"+nb+"next thing"), MidTurn},
		{"idle with text typed", frame("✻ Cooked for 1m 3s · done 5:23 PM", "❯"+nb+"a half written message"), HoldsText},
		{"a multi line draft", frame("", "❯"+nb+"line one", "  line two", "  line three"), HoldsText},
		// The reply text is model-controlled, so a reply must not be able to
		// pass for the harness's own idle marker or spinner. Only the done line's
		// and spinner's own glyphs count; the reply bullet and tool-output marker
		// do not.
		{"a reply that imitates the done line", frame("⏺ Worked for 3s · done 5:23 PM", "❯"+nb), Unknown},
		{"a reply that imitates the done line with another verb", frame("⏺ Ran for 3s · done 5:23 PM", "❯"+nb), Unknown},
		{"tool output that imitates the done line", frame("  ⎿ Worked for 3s · done 5:23 PM", "❯"+nb), Unknown},
		{"tool output marker at the margin that imitates the done line", frame("⎿ Worked for 3s · done 5:23 PM", "❯"+nb), Unknown},
		{"a reply that imitates the spinner", frame("⏺ Cooking… (3s · thinking)", "❯"+nb), Unknown},
		{"an indented line that imitates the spinner", frame("  ✳ Cooking… (3s · thinking)", "❯"+nb), Unknown},
		{"a combined dim attribute marks the placeholder", frame("", "❯"+nb+"\x1b[2;37mTry \"fix the build\"\x1b[0m"), Empty},
		{"a colour index of 2 is not dim", "\x1b[39m" + frame("", "❯"+nb+"\x1b[38;5;2mTry \"fix the build\"\x1b[0m"), HoldsText},
		{"reset to normal intensity ends dim", "\x1b[39m" + frame("", "❯"+nb+"\x1b[2m\x1b[22mTry \"fix the build\""), HoldsText},
		// A clear key sent while a turn streams would interrupt it, so a draft
		// typed ahead of streaming text is not reported as an idle draft.
		{"text typed ahead of streaming text is not an idle draft", frame("⏺ Reply text still arriving", "❯"+nb+"typed ahead"), Unknown},
		{"text typed ahead after an earlier turn with no done line", frame("❯ earlier prompt\n\n⏺ reply", "❯"+nb+"typed"), Unknown},
		{"a draft in a session that has run no turn", frame("", "❯"+nb+"first message"), HoldsText},
		{"a hint line right of the composer is not content", frame("✻ Worked for 2s · done 5:23 PM\n"+strings.Repeat(" ", 100)+"auto mode unavailable for this model", "❯"+nb), Empty},
		{"streaming text over an older done marker", frame("✻ Worked for 2s · done 5:23 PM\n\n❯ next prompt\n\n⏺ Reply text still arriving", "❯"+nb), Unknown},
		{"placeholder text with escapes is dim and so empty", frame("", "❯"+nb+dim+"Try \"fix the build\""+"\x1b[0m"), Empty},
		{"typed text shaped like the placeholder, with escapes elsewhere, is text", "\x1b[39m" + frame("", "❯"+nb+"Try \"fix the build\""), HoldsText},
		{"the same shape in a plain capture is ambiguous", frame("", "❯"+nb+"Try \"fix the build\""), Unknown},
		{"a prompt with a plain space is not the live composer", frame("", "❯ Try something"), Unknown},
		{"a composer with no closing rule", "\n" + rule + "\n❯" + nb + "typed\n", Unknown},
	}
	for _, tc := range cases {
		if got := r.Read(tc.capture); got != tc.want {
			t.Errorf("%s: Read = %q, want %q", tc.name, got, tc.want)
		}
	}
}

const tipLine = "  ⎿  Tip: Use /clear to start fresh when switching topics"

// insertAfter returns frame with line added after the first line containing
// marker. A frame is built from a real capture so the rest of it stays real.
func insertAfter(t *testing.T, frame, marker, line string) string {
	t.Helper()
	lines := strings.Split(frame, "\n")
	for i, l := range lines {
		if strings.Contains(l, marker) {
			out := append(append(append([]string{}, lines[:i+1]...), line), lines[i+1:]...)
			return strings.Join(out, "\n")
		}
	}
	t.Fatalf("no line containing %q in the fixture", marker)
	return ""
}

// While a turn runs, Claude draws a "⎿  Tip: ..." line between the spinner and
// the composer. The first line above the composer is then the tip, not the
// spinner, and a frame that is plainly mid-turn stopped reading as mid_turn.
func TestClaudeReaderSeesPastTheTipLineUnderTheSpinner(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	for _, name := range []string{"3a-mid-turn-1s5.txt", "3a-mid-turn-1s5.ansi.txt"} {
		frame := insertAfter(t, fixture(t, name), "Boogieing", tipLine)
		if got := r.Read(frame); got != MidTurn {
			t.Errorf("%s with a tip line: Read = %q, want %q", name, got, MidTurn)
		}
	}
}

// With a draft in the composer the same frame read as holds_text, which would
// let a clear key or a stage through in the middle of a turn.
func TestClaudeReaderReadsATipFrameWithADraftAsMidTurn(t *testing.T) {
	t.Parallel()
	frame := insertAfter(t, fixture(t, "3a-mid-turn-1s5.txt"), "Boogieing", tipLine)
	frame = strings.Replace(frame, "❯ \n", "❯ a draft\n", 1)
	if !strings.Contains(frame, "a draft") {
		t.Fatal("the draft was not placed in the composer")
	}
	if got := ReaderFor("claude").Read(frame); got != MidTurn {
		t.Errorf("Read = %q, want %q", got, MidTurn)
	}
}

// A hint line is skipped, and only skipped: text in it that looks like a
// spinner is not one (the spinner is a glyph at the margin), so it never makes
// an idle frame read as mid-turn. The finished turn's done line above it is
// what the frame is read from.
func TestClaudeReaderDoesNotTakeAHintLineForASpinner(t *testing.T) {
	t.Parallel()
	imitation := "  ⎿  ✻ Boogieing… (1s · thinking)"
	frame := insertAfter(t, fixture(t, "4-idle-after-submit.txt"), "Worked for", imitation)
	got := ReaderFor("claude").Read(frame)
	if got == MidTurn {
		t.Fatalf("a hint line imitating a spinner read as %q", got)
	}
	if got != Empty {
		t.Errorf("Read = %q, want %q: the done line above the hint line is what the frame says", got, Empty)
	}
}
