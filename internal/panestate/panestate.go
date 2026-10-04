// Package panestate classifies what a harness's pane is showing, from the
// visible screen alone (docs/design/harness-state-watchdog-p1.md section 4).
//
// It reads text and returns a verdict. It sends nothing and owns no key.
// Patterns are data built from captured samples; with no pattern set for a
// harness version nothing matches, so a harness change makes it stop
// recognising and never misfire. A confident wrong answer is the injury, so
// only a full, bottom-aligned, same-version match sets a state.
//
// No captured row ever leaves this package. Evidence is rendered from the
// pattern: its fixed rows, with each variable span replaced by Masked.
package panestate

import (
	"fmt"
	"strings"
)

// State is what the screen was classified as.
type State string

const (
	// StateLoggedOut is a harness sitting at its login prompt.
	StateLoggedOut State = "logged-out"
	// StateUnknown is every other answer, including a low-confidence match.
	StateUnknown State = "unknown"
)

// Confidence tiers a match. Only ConfHigh sets a state.
type Confidence string

const (
	ConfHigh Confidence = "high"
	ConfLow  Confidence = "low"
)

// Masked replaces a pattern row's variable span in every rendered surface.
const Masked = "<masked>"

// varMarker marks the one variable span a pattern row may carry.
const varMarker = "{{var}}"

// EscError is the refusal to normalise a screen that holds an ESC byte. It
// names the pane and the byte offset only: the row may hold a span to mask.
type EscError struct {
	Pane   string
	Offset int
}

func (e *EscError) Error() string {
	return fmt.Sprintf("pane %s: escape byte at offset %d", e.Pane, e.Offset)
}

// Normalize turns a joined visible capture into rows: trailing spaces are
// stripped per row and trailing blank rows dropped. capture-pane -p without -e
// emits no escape sequences, so an ESC byte is refused rather than guessed at.
func Normalize(pane, raw string) ([]string, error) {
	if i := strings.IndexByte(raw, 0x1b); i >= 0 {
		return nil, &EscError{Pane: pane, Offset: i}
	}
	lines := strings.Split(raw, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// Row is one pattern row: a fixed row, or a fixed prefix and suffix around a
// variable span.
type Row struct {
	Prefix, Suffix string
	Var            bool
}

func parseRow(s string) (Row, error) {
	switch n := strings.Count(s, varMarker); n {
	case 0:
		return Row{Prefix: s}, nil
	case 1:
		i := strings.Index(s, varMarker)
		return Row{Prefix: s[:i], Suffix: s[i+len(varMarker):], Var: true}, nil
	default:
		return Row{}, fmt.Errorf("pattern row carries %d variable spans, at most one is allowed", n)
	}
}

func (r Row) matches(line string) bool {
	if !r.Var {
		return line == r.Prefix
	}
	return len(line) > len(r.Prefix)+len(r.Suffix) &&
		strings.HasPrefix(line, r.Prefix) && strings.HasSuffix(line, r.Suffix)
}

// Render is the row as every surface shows it.
func (r Row) Render() string {
	if !r.Var {
		return r.Prefix
	}
	return r.Prefix + Masked + r.Suffix
}

// Pattern is one versioned block of consecutive rows, matched whole at the
// bottom of the normalised screen.
type Pattern struct {
	ID             string
	Version        int
	Harness        string
	HarnessVersion string
	SampleWidth    int
	Rows           []Row
}

// Result is a classification. Evidence is pattern text only.
type Result struct {
	State          State
	Confidence     Confidence
	PatternID      string
	PatternVersion int
	HarnessVersion string
	Evidence       []string
}

// match aligns the pattern to the bottom of rows and returns the pattern rows
// that matched, in order, and whether all of them did.
func (p Pattern) match(rows []string) (matched []Row, full bool) {
	n := len(p.Rows)
	if n == 0 {
		return nil, false
	}
	off := len(rows) - n
	for i, pr := range p.Rows {
		j := off + i
		if j < 0 || j >= len(rows) {
			continue
		}
		if pr.matches(rows[j]) {
			matched = append(matched, pr)
		}
	}
	return matched, len(matched) == n
}

// Classify matches the screen against the harness's pattern sets. sessVersion
// is the harness version the pane reports. A full match from a set of the same
// version is high and the only one that sets a state; a full match from another
// version, or a partial match, is low and reads unknown.
func Classify(sets []Pattern, harness, sessVersion string, rows []string) Result {
	unknown := Result{State: StateUnknown}
	var low *Result
	for _, p := range sets {
		if p.Harness != harness {
			continue
		}
		matched, full := p.match(rows)
		if len(matched) == 0 {
			continue
		}
		res := Result{
			State: StateUnknown, Confidence: ConfLow,
			PatternID: p.ID, PatternVersion: p.Version, HarnessVersion: p.HarnessVersion,
		}
		for _, m := range matched {
			res.Evidence = append(res.Evidence, m.Render())
		}
		if full && p.HarnessVersion != "" && p.HarnessVersion == sessVersion {
			res.State, res.Confidence = StateLoggedOut, ConfHigh
			return res
		}
		if low == nil || len(res.Evidence) > len(low.Evidence) {
			r := res
			low = &r
		}
	}
	if low != nil {
		return *low
	}
	return unknown
}
