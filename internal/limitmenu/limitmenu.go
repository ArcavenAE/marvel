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
	"time"
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

// Validate reports whether the sample can be matched against.
func (s Sample) Validate() error { return nil }

// Match looks for the sample's menu in a capture of the visible screen.
func Match(s Sample, capture string) Result {
	_ = s
	_ = capture
	return Result{Refusal: RefusalNoSample}
}

// Until is a reset time read from the menu's time span.
type Until struct {
	// Time is the instant, in UTC.
	Time time.Time
	// Zone names the zone the span was read in, for the condition's
	// provenance.
	Zone string
}

// ParseUntil reads a span such as "Oct 6 at 9pm". See the design, section 9.3.
func ParseUntil(span string, captured time.Time, seatTZ string, host *time.Location) (Until, error) {
	_ = span
	_ = captured
	_ = seatTZ
	_ = host
	return Until{}, nil
}

// Errors from ParseUntil. A caller treats every one the same way: the until is
// empty and the reset is unknown. They differ so the provenance can say why.
var (
	ErrUnparseable = errors.New("limitmenu: time span not understood")
	ErrAmbiguous   = errors.New("limitmenu: local time occurs twice")
	ErrNonexistent = errors.New("limitmenu: local time does not occur")
	ErrUnknownZone = errors.New("limitmenu: seat zone not known")
)
