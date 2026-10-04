package limitmenu

import (
	"strings"
	"testing"
)

// testScreen is SYNTHETIC and test-only, like testSample.
func testScreen() Screen {
	return Screen{
		Version: "test-only-0",
		Rows: []string{
			"Waiting for your limit to reset",
			"Will continue automatically at Oct 6 at 9pm",
		},
		SpanRow: 1,
	}
}

func TestScreenValidate(t *testing.T) {
	t.Parallel()
	if err := testScreen().Validate(); err != nil {
		t.Fatalf("valid screen refused: %v", err)
	}
	for name, s := range map[string]Screen{
		"empty":            {},
		"no version":       {Rows: []string{"x"}, SpanRow: -1},
		"span row range":   {Version: "v", Rows: []string{"x"}, SpanRow: 3},
		"no span on row":   {Version: "v", Rows: []string{"x"}, SpanRow: 0},
		"trailing space":   {Version: "v", Rows: []string{"x "}, SpanRow: -1},
		"span out of low":  {Version: "v", Rows: []string{"x"}, SpanRow: -2},
		"two spans on row": {Version: "v", Rows: []string{"Oct 6 at 9pm or Oct 7 at 9pm"}, SpanRow: 0},
	} {
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (Screen{Version: "v", Rows: []string{"x"}, SpanRow: -1}).Validate(); err != nil {
		t.Errorf("a screen with no span row refused: %v", err)
	}
}

func TestMatchScreen(t *testing.T) {
	t.Parallel()
	s := testScreen()
	cap := func(span, tail string) string {
		return strings.Join([]string{"> earlier", "Waiting for your limit to reset", "Will continue automatically at " + span, tail}, "\n")
	}
	if got := MatchScreen(s, cap("Oct 6 at 9pm", "")); !got.Matched || got.Span != "Oct 6 at 9pm" {
		t.Fatalf("own span: %+v", got)
	}
	if got := MatchScreen(s, cap("Nov 12 at 10:30am", "\n")); !got.Matched || got.Span != "Nov 12 at 10:30am" {
		t.Fatalf("another span with blank rows after: %+v", got)
	}
	for name, tc := range map[string]struct {
		capture string
		want    Refusal
	}{
		"a row differs":         {strings.Replace(cap("Oct 6 at 9pm", ""), "Waiting", "waiting", 1), RefusalNoBlock},
		"span with a remainder": {cap("Oct 6 at 9pm sharp", ""), RefusalNoBlock},
		"a row follows":         {cap("Oct 6 at 9pm", "something else"), RefusalTrailingRows},
		"empty":                 {"", RefusalNoBlock},
	} {
		if got := MatchScreen(s, tc.capture); got.Matched || got.Refusal != tc.want {
			t.Errorf("%s: %+v, want refusal %q", name, got, tc.want)
		}
	}
	if got := MatchScreen(Screen{}, "x"); got.Matched || got.Refusal != RefusalNoSample {
		t.Errorf("no screen: %+v", got)
	}
}
