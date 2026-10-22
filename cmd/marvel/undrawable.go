package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// placeholder stands for every character the filter replaces. It is never a
// separator, so a filtered cell cannot fake a qualified key (docs/design/
// multicluster-read-view-plan.md, section 3.4).
const placeholder = '�'

// invalidByte names an invalid UTF-8 byte in the held list.
const invalidByte = "invalid UTF-8"

// keyLookalikes are the characters that read as a key separator. They are
// replaced in the workspace and name parts of a key, and nowhere else.
var keyLookalikes = map[rune]bool{
	0xFF1A: true, 0x2236: true, 0xA789: true, 0xFE55: true,
	0x2215: true, 0x2044: true, 0xFF0F: true, 0x29F8: true,
}

// filterDrawn replaces each C0 (tab, LF and CR included), DEL, C1 and Unicode
// format character (category Cf, which holds the bidi controls and the
// zero-width characters), and each invalid UTF-8 byte, with one placeholder.
// The rune count is unchanged. It returns what it replaced, one entry per
// replacement ("U+001B", or "invalid UTF-8"), or nil when the text was clean.
func filterDrawn(s string) (string, []string) { return filterText(s, false) }

// filterKeyPart is filterDrawn for the workspace and name parts of a key: it
// also replaces the separator lookalikes.
func filterKeyPart(s string) (string, []string) { return filterText(s, true) }

func filterText(s string, key bool) (string, []string) {
	var (
		b    strings.Builder
		held []string
	)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			held = append(held, invalidByte)
			b.WriteRune(placeholder)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || (key && keyLookalikes[r]):
			held = append(held, fmt.Sprintf("U+%04X", r))
			b.WriteRune(placeholder)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	if held == nil {
		return s, nil
	}
	return b.String(), held
}

// drawn is filterDrawn for a caller that only wants the text.
func drawn(s string) string {
	out, _ := filterDrawn(s)
	return out
}

// keyPartColumns are the columns that print a part of a session key.
var keyPartColumns = map[string]bool{"workspace": true, "name": true}

func filterForColumn(name string) func(string) (string, []string) {
	if keyPartColumns[name] {
		return filterKeyPart
	}
	return filterDrawn
}

// drawableColumns wraps each column so its cell is filtered before the table
// measures it.
func drawableColumns(cols []sessionColumn) []sessionColumn {
	out := make([]sessionColumn, len(cols))
	for i, c := range cols {
		pick, filter := c.pick, filterForColumn(c.name)
		c.pick = func(r sessionRow) string {
			cell, _ := filter(pick(r))
			return cell
		}
		out[i] = c
	}
	return out
}

// shownColumns is the columns of all, unwrapped, that the fit kept in shown.
func shownColumns(all, shown []sessionColumn) []sessionColumn {
	keep := make(map[string]bool, len(shown))
	for _, c := range shown {
		keep[c.name] = true
	}
	var out []sessionColumn
	for _, c := range all {
		if keep[c.name] {
			out = append(out, c)
		}
	}
	return out
}

// undrawTally counts the fields that held a replaced character and names the
// characters, so the operator learns which were there.
type undrawTally struct {
	fields int
	seen   map[string]bool
}

func (t *undrawTally) add(held []string) {
	if len(held) == 0 {
		return
	}
	t.fields++
	if t.seen == nil {
		t.seen = map[string]bool{}
	}
	for _, h := range held {
		t.seen[h] = true
	}
}

// draw filters one field and counts it.
func (t *undrawTally) draw(s string) string {
	out, held := filterDrawn(s)
	t.add(held)
	return out
}

// key filters one workspace or name part and counts it.
func (t *undrawTally) key(s string) string {
	out, held := filterKeyPart(s)
	t.add(held)
	return out
}

// cells counts the cells drawn in cols that held a replaced character.
func (t *undrawTally) cells(sessions []api.Session, cols []sessionColumn) {
	for _, s := range sessions {
		row := newSessionRow(s)
		for _, c := range cols {
			_, held := filterForColumn(c.name)(c.pick(row))
			t.add(held)
		}
	}
}

// line is the one line that follows the output, or "" when nothing was
// replaced.
func (t *undrawTally) line() string {
	if t.fields == 0 {
		return ""
	}
	names := make([]string, 0, len(t.seen))
	for h := range t.seen {
		names = append(names, h)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) < len(names[j])
		}
		return names[i] < names[j]
	})
	noun := "fields"
	if t.fields == 1 {
		noun = "field"
	}
	return fmt.Sprintf("%d %s held undrawable characters (%s); -o json shows them\n",
		t.fields, noun, strings.Join(names, ", "))
}

// drawnError is an error whose text is filtered and whose cause is kept, so
// errors.As still finds a typed connection error under it.
type drawnError struct{ err error }

func (e drawnError) Error() string { return drawn(e.err.Error()) }
func (e drawnError) Unwrap() error { return e.err }

// drawnErr filters an error that carries daemon text. A nil error and the
// watch-unsupported sentinel pass through untouched.
func drawnErr(err error) error {
	if err == nil || errors.Is(err, daemonrpc.ErrWatchUnsupported) {
		return err
	}
	return drawnError{err}
}

// noteDaemonHome prints the home-mismatch warning for a daemon that reported
// a different home, with the daemon's text filtered.
func noteDaemonHome(w io.Writer, addr, daemonHome, clientHome string) {
	if m := config.DaemonHomeWarning(addr, daemonHome, clientHome); m != "" {
		_, _ = fmt.Fprintln(w, drawn(m))
	}
}

// flushWithNote flushes a table and follows it with the tally's note line.
func flushWithNote(tw *tabwriter.Writer, out io.Writer, tally *undrawTally) error {
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(out, tally.line())
	return err
}
