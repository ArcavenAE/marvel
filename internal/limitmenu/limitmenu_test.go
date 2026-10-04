package limitmenu

import (
	"strings"
	"testing"
)

func TestSampleValidate(t *testing.T) {
	t.Parallel()
	ok := testSample()
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid sample refused: %v", err)
	}
	mut := func(f func(*Sample)) Sample {
		s := testSample()
		s.Rows = append([]string(nil), s.Rows...)
		f(&s)
		return s
	}
	tests := []struct {
		name string
		s    Sample
	}{
		{"empty", Sample{}},
		{"no version", mut(func(s *Sample) { s.Version = "" })},
		{"no glyph", mut(func(s *Sample) { s.Glyph = "" })},
		{"option index out of range", mut(func(s *Sample) { s.Options[2] = 9 })},
		{"options out of order", mut(func(s *Sample) { s.Options = [3]int{2, 1, 3} })},
		{"two glyph rows", mut(func(s *Sample) { s.Rows[2] = "❯ 2. Wait here, then continue automatically at Oct 6 at 9pm" })},
		{"no glyph row", mut(func(s *Sample) { s.Rows[1] = "  1. Stop and wait for limit to reset" })},
		{"no time span", mut(func(s *Sample) { s.Rows[2] = "  2. Wait here, then continue automatically" })},
		{"two time spans", mut(func(s *Sample) { s.Rows[2] += " or Oct 7 at 9pm" })},
		{"trailing space", mut(func(s *Sample) { s.Rows[0] += " " })},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.s.Validate(); err == nil {
				t.Fatal("invalid sample accepted")
			}
		})
	}
}

// Test 23: the matcher accepts the sample with its own span and others, and
// refuses everything that is not the same menu.
func TestMatchAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, capture, span string
	}{
		{"own span", screen(2, "Oct 6 at 9pm"), "Oct 6 at 9pm"},
		{"another span", screen(2, "Nov 12 at 10:30am"), "Nov 12 at 10:30am"},
		{"trailing spaces on rows", strings.ReplaceAll(screen(2, "Oct 6 at 9pm"), "\n", "   \n"), "Oct 6 at 9pm"},
		{"quoted copy above the real menu", screen(2, "Oct 6 at 9pm") + "\n" + screen(2, "Oct 7 at 9pm"), "Oct 7 at 9pm"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Match(testSample(), tc.capture)
			if !got.Matched || got.Refusal != "" {
				t.Fatalf("not matched: %+v", got)
			}
			if got.Span != tc.span {
				t.Fatalf("span = %q, want %q", got.Span, tc.span)
			}
		})
	}
}

func TestMatchRefuses(t *testing.T) {
	t.Parallel()
	real2 := screen(2, "Oct 6 at 9pm")
	reordered := strings.Join([]string{
		"What do you want to do?",
		"  1. Stop and wait for limit to reset",
		"❯ 2. Switch to usage credits",
		"  3. Wait here, then continue automatically at Oct 6 at 9pm",
		"Enter to confirm · Esc to cancel",
	}, "\n")
	tests := []struct {
		name, capture string
		want          Refusal
	}{
		{"span with another remainder", strings.Replace(real2, "Oct 6 at 9pm", "Oct 6 at 9pm (or sooner)", 1), RefusalNoBlock},
		{"first row differs by one byte", strings.Replace(real2, "Stop and wait", "Stop and Wait", 1), RefusalNoBlock},
		{"third row differs by one byte", strings.Replace(real2, "usage credits", "usage credit", 1), RefusalNoBlock},
		{"span not a date", screen(2, "tomorrow at 9pm"), RefusalNoBlock},
		{"(i) options reordered", reordered, RefusalNoBlock},
		{"(ii) quoted real menu above a reordered one", real2 + "\n" + reordered, RefusalTrailingRows},
		{"(ii) quoted real menu below a real one", real2 + "\n> quoted: " + "\nWhat do you want to do?", RefusalTrailingRows},
		{"(iii) real menu then a row the sample lacks", real2 + "\nsomething else\n", RefusalTrailingRows},
		{"(iv) cursor on row 3", screen(3, "Oct 6 at 9pm"), RefusalCursorRow},
		{"(v) cursor on row 1", screen(1, "Oct 6 at 9pm"), RefusalCursorRow},
		{"no cursor", screen(0, "Oct 6 at 9pm"), RefusalCursorCount},
		{"cursor on every row", screen(4, "Oct 6 at 9pm"), RefusalCursorCount},
		{"empty capture", "", RefusalNoBlock},
		{"not a menu", "hello\nworld\n", RefusalNoBlock},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Match(testSample(), tc.capture)
			if got.Matched {
				t.Fatalf("matched: %+v", got)
			}
			if got.Refusal != tc.want {
				t.Fatalf("refusal = %q, want %q", got.Refusal, tc.want)
			}
		})
	}
}

func TestMatchWithNoSampleMatchesNothing(t *testing.T) {
	t.Parallel()
	got := Match(Sample{}, screen(2, "Oct 6 at 9pm"))
	if got.Matched || got.Refusal != RefusalNoSample {
		t.Fatalf("got %+v, want a no-sample refusal", got)
	}
}

func TestMatchAllowsTheSamplesOwnTrailingRows(t *testing.T) {
	t.Parallel()
	s := testSample()
	s.Trailing = []string{"model: test"}
	if got := Match(s, screen(2, "Oct 6 at 9pm")+"\nmodel: test\n"); !got.Matched {
		t.Fatalf("sample's own trailing row refused: %+v", got)
	}
	if got := Match(s, screen(2, "Oct 6 at 9pm")+"\nmodel: other\n"); got.Refusal != RefusalTrailingRows {
		t.Fatalf("other trailing row: %+v", got)
	}
}
