package limitmenu

import (
	"errors"
	"fmt"
	"strings"
)

// Screen is a captured screen with no options on it, such as the one a seat
// shows after the second menu option was chosen: a run of rows, one of which
// may carry a reset time span. It is matched under the same rules as the menu
// (whole rows after trailing spaces are stripped, the last place the run
// matches, only blank rows after it), minus the cursor rules, which a screen
// without options does not have.
type Screen struct {
	Version string
	Rows    []string
	// SpanRow is the index of the row that carries a time span, or -1 for none.
	SpanRow int
}

// Validate reports whether the screen can be matched against.
func (s Screen) Validate() error {
	if s.Version == "" {
		return errors.New("limitmenu: screen has no harness version")
	}
	if len(s.Rows) == 0 {
		return errors.New("limitmenu: screen has no rows")
	}
	for i, r := range s.Rows {
		if r != strings.TrimRight(r, " ") {
			return fmt.Errorf("limitmenu: screen row %d has trailing spaces", i)
		}
	}
	if s.SpanRow < -1 || s.SpanRow >= len(s.Rows) {
		return fmt.Errorf("limitmenu: screen span row %d is out of range", s.SpanRow)
	}
	if s.SpanRow >= 0 {
		if n := len(spanFind.FindAllStringIndex(s.Rows[s.SpanRow], -1)); n != 1 {
			return fmt.Errorf("limitmenu: screen span row has %d time spans, want 1", n)
		}
	}
	return nil
}

// MatchScreen looks for the screen in a capture of the visible screen.
func MatchScreen(s Screen, capture string) Result {
	if s.Validate() != nil {
		return Result{Refusal: RefusalNoSample}
	}
	lines := splitCapture(capture)
	for start := len(lines) - len(s.Rows); start >= 0; start-- {
		span, ok := matchScreenRun(s, lines[start:start+len(s.Rows)])
		if !ok {
			continue
		}
		res := Result{Span: span, Start: start}
		if !trailingMatches(nil, lines[start+len(s.Rows):]) {
			res.Refusal = RefusalTrailingRows
			return res
		}
		res.Matched = true
		return res
	}
	return Result{Refusal: RefusalNoBlock}
}

func matchScreenRun(s Screen, window []string) (span string, ok bool) {
	for i, want := range s.Rows {
		got := window[i]
		if i != s.SpanRow {
			if got != want {
				return "", false
			}
			continue
		}
		loc := spanFind.FindStringIndex(want)
		prefix, suffix := want[:loc[0]], want[loc[1]:]
		if len(got) < len(prefix)+len(suffix) || !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
			return "", false
		}
		mid := got[len(prefix) : len(got)-len(suffix)]
		if !spanFull.MatchString(mid) {
			return "", false
		}
		span = mid
	}
	return span, true
}

func splitCapture(capture string) []string {
	lines := strings.Split(capture, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \r")
	}
	return lines
}
