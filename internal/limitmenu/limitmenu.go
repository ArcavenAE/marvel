// Package limitmenu recognises the usage-limit menu a harness shows when an
// account runs out, and reads the reset time that menu names.
//
// It is the shared matcher of docs/design/usage-limit-pause.md section 9.4. It
// reads text and returns a verdict. It sends nothing, owns no key, and starts
// no timer: whatever acts on a verdict lives elsewhere. The matcher refuses on
// any mismatch, so a harness change makes it stop recognising, not misfire.
//
// A Sample is a captured menu together with the harness version that drew it.
// With no Sample, nothing matches. That is the shipped state until a capture is
// supplied: no samples, no match.
package limitmenu

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Refusal names why a capture was not accepted as the limit menu.
type Refusal string

const (
	// RefusalNoSample means the sample is missing or invalid.
	RefusalNoSample Refusal = "no-sample"
	// RefusalNoBlock means no run of rows in the capture matches the sample.
	RefusalNoBlock Refusal = "no-block"
	// RefusalTrailingRows means a non-blank row the sample does not carry
	// follows the last matching block.
	RefusalTrailingRows Refusal = "trailing-rows"
	// RefusalCursorCount means the option rows do not carry exactly one
	// cursor glyph.
	RefusalCursorCount Refusal = "cursor-count"
	// RefusalCursorRow means the cursor glyph is on an option row other
	// than the second.
	RefusalCursorRow Refusal = "cursor-not-on-option-2"
)

// Sample is one captured menu. Rows are the menu block as captured, trailing
// spaces stripped, with the cursor glyph in its column on exactly one option
// row. Options holds the index in Rows of the "1.", "2." and "3." rows.
// Trailing are the non-blank rows the sample shows after the block, if any.
type Sample struct {
	Version  string
	Glyph    string
	Rows     []string
	Options  [3]int
	Trailing []string
}

// Result is the verdict on one capture. Span is the reset time text of the
// second option row when a block was found, whether or not it was accepted.
type Result struct {
	Matched bool
	Refusal Refusal
	Span    string
	Start   int
}

// Validate reports whether the sample can be matched against: the glyph is
// set, the option rows are in order, exactly one of them carries the glyph and
// the others its blank form, and the second names one time span.
func (s Sample) Validate() error {
	if s.Version == "" {
		return errors.New("limitmenu: sample has no harness version")
	}
	if s.Glyph == "" {
		return errors.New("limitmenu: sample has no cursor glyph")
	}
	if len(s.Rows) == 0 {
		return errors.New("limitmenu: sample has no rows")
	}
	for i, r := range s.Rows {
		if r != strings.TrimRight(r, " ") {
			return fmt.Errorf("limitmenu: sample row %d has trailing spaces", i)
		}
	}
	prev := -1
	for k, idx := range s.Options {
		if idx <= prev || idx >= len(s.Rows) {
			return fmt.Errorf("limitmenu: option %d row index %d is out of range or order", k+1, idx)
		}
		prev = idx
	}
	glyphs := 0
	for _, idx := range s.Options {
		cell, _, ok := splitCursor(s.Rows[idx], s.Glyph)
		if !ok {
			return fmt.Errorf("limitmenu: sample row %d has neither the glyph nor its blank form", idx)
		}
		if cell == s.Glyph {
			glyphs++
		}
	}
	if glyphs != 1 {
		return fmt.Errorf("limitmenu: sample has %d glyph rows, want 1", glyphs)
	}
	_, rest, _ := splitCursor(s.Rows[s.Options[1]], s.Glyph)
	if n := len(spanFind.FindAllStringIndex(rest, -1)); n != 1 {
		return fmt.Errorf("limitmenu: sample option 2 has %d time spans, want 1", n)
	}
	return nil
}

// splitCursor splits an option row into its cursor cell and the rest. The cell
// is the glyph or the blank of the same width; anything else is not a menu row.
func splitCursor(row, glyph string) (cell, rest string, ok bool) {
	n := utf8.RuneCountInString(glyph)
	if utf8.RuneCountInString(row) < n {
		return "", "", false
	}
	cut := 0
	for i := 0; i < n; i++ {
		_, w := utf8.DecodeRuneInString(row[cut:])
		cut += w
	}
	cell, rest = row[:cut], row[cut:]
	if cell != glyph && cell != strings.Repeat(" ", n) {
		return "", "", false
	}
	return cell, rest, true
}

// Match looks for the sample's menu in a capture of the visible screen. Rows
// are compared whole after trailing spaces are stripped. The block is the last
// place where the whole run matches, and only blank rows (and the sample's own
// trailing rows) may follow it. The cursor glyph must be on the second option,
// and on no other. See the design, section 9.4.
func Match(s Sample, capture string) Result {
	if s.Validate() != nil {
		return Result{Refusal: RefusalNoSample}
	}
	lines := strings.Split(capture, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \r")
	}
	for start := len(lines) - len(s.Rows); start >= 0; start-- {
		span, glyphOn, ok := matchRun(s, lines[start:start+len(s.Rows)])
		if !ok {
			continue
		}
		res := Result{Span: span, Start: start}
		if !trailingMatches(s.Trailing, lines[start+len(s.Rows):]) {
			res.Refusal = RefusalTrailingRows
			return res
		}
		if glyphOn != [3]bool{false, true, false} {
			if count(glyphOn) != 1 {
				res.Refusal = RefusalCursorCount
			} else {
				res.Refusal = RefusalCursorRow
			}
			return res
		}
		res.Matched = true
		return res
	}
	return Result{Refusal: RefusalNoBlock}
}

func count(b [3]bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

// matchRun compares one window of the capture with the sample's rows. Option
// rows may carry the glyph or its blank form; which one they carry is
// returned, and judged by the caller.
func matchRun(s Sample, window []string) (span string, glyphOn [3]bool, ok bool) {
	opt := map[int]int{s.Options[0]: 0, s.Options[1]: 1, s.Options[2]: 2}
	for i, want := range s.Rows {
		got := window[i]
		k, isOpt := opt[i]
		if !isOpt {
			if got != want {
				return "", glyphOn, false
			}
			continue
		}
		_, wantRest, _ := splitCursor(want, s.Glyph)
		cell, gotRest, cok := splitCursor(got, s.Glyph)
		if !cok {
			return "", glyphOn, false
		}
		glyphOn[k] = cell == s.Glyph
		if k != 1 {
			if gotRest != wantRest {
				return "", glyphOn, false
			}
			continue
		}
		loc := spanFind.FindStringIndex(wantRest)
		prefix, suffix := wantRest[:loc[0]], wantRest[loc[1]:]
		if len(gotRest) < len(prefix)+len(suffix) || !strings.HasPrefix(gotRest, prefix) || !strings.HasSuffix(gotRest, suffix) {
			return "", glyphOn, false
		}
		mid := gotRest[len(prefix) : len(gotRest)-len(suffix)]
		if !spanFull.MatchString(mid) {
			return "", glyphOn, false
		}
		span = mid
	}
	return span, glyphOn, true
}

// trailingMatches reports whether the non-blank rows after the block are
// exactly the sample's own trailing rows.
func trailingMatches(want, rest []string) bool {
	var got []string
	for _, r := range rest {
		if r != "" {
			got = append(got, r)
		}
	}
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Until is a reset time read from the menu's time span.
type Until struct {
	// Time is the instant, in UTC.
	Time time.Time
	// Zone names the zone the span was read in, for the condition's
	// provenance.
	Zone string
}

// Errors from ParseUntil. A caller treats every one the same way: the until is
// empty and the reset is unknown. They differ so the provenance can say why.
var (
	ErrUnparseable = errors.New("limitmenu: time span not understood")
	ErrAmbiguous   = errors.New("limitmenu: local time occurs twice")
	ErrNonexistent = errors.New("limitmenu: local time does not occur")
	ErrUnknownZone = errors.New("limitmenu: seat zone not known")
)

// spanCore is the one fixed pattern for the reset time text in the menu's
// second option, taken from the 2026-10-03 capture ("Oct 6 at 9pm"). Only
// that span is a pattern; every other byte of a row is compared exactly.
const spanCore = `([A-Z][a-z]{2}) ([0-9]{1,2}) at ([0-9]{1,2})(?::([0-9]{2}))?(am|pm)`

var (
	spanFind = regexp.MustCompile(spanCore)
	spanFull = regexp.MustCompile("^" + spanCore + "$")
)
