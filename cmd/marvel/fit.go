package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/api"
)

// The width fit for `get sessions` (docs/design/get-sessions-output.md
// section 4.2). Diagnostic only: nothing gates on what it hides.
//
// A fit drops columns by a fixed priority and never reflows. The tiers are
// data: a name that is not a registered column yet (LAST-ACTIVE, TOUT, RATE,
// AGE, ACTIVE%) is skipped, so each of those joins by one entry here when its
// ticket lands.

// fitPriority is every default column, most important first. Columns are
// dropped from the tail.
var fitPriority = []string{
	"name", "state", "health", "context", "last-active",
	"llm", "tout", "rate",
	"team", "role", "workdir", "age", "active",
	// Today's other columns close the wide tier, so a wide terminal shows
	// what it always did plus the new columns, and a narrow one drops these
	// first. They stay selectable by name and in the wide set at any width.
	"runtime", "cpu", "rss", "desk", "generation", "workspace",
}

// fitTiers say how much of fitPriority a width class starts from: the
// number of leading entries allowed at or above minWidth. Section 4.2's table
// names the tiers; the six trailing entries are the architect's ruling on
// where today's columns sit (aae-orc-fmgxd).
var fitTiers = []struct{ minWidth, upTo int }{
	{200, 19},
	{120, 8},
	{0, 5},
}

// workdirMaxWidth is the cell width a long WORKDIR is cut to.
const workdirMaxWidth = 32

// fitOptions carry what the fit needs from the command. width 0 means stdout
// is not a terminal, and nothing is fitted.
type fitOptions struct {
	width    int  // terminal width in columns, 0 off a terminal
	explicit bool // the operator named the columns (--columns or the config)
	noTrunc  bool // --no-trunc: print RUNTIME and WORKDIR in full
}

type fitResult struct {
	cols   []sessionColumn // the columns the table prints
	table  string
	hidden int    // default columns the width left out
	warn   string // an explicit list that does not fit, or empty
}

// terminalWidth is the width of stdout when it is a terminal, else 0. A seam
// so tests set it.
var terminalWidth = func() int {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 0
	}
	w, _, err := term.GetSize(fd)
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// columnTrim cuts a cell for the width. Only these two columns are cut, and
// never AGENT NAME: the pane verbs take the printed name (#337).
var columnTrim = map[string]func(string) string{
	"runtime": runtimeBasename,
	"workdir": workdirTrim,
}

// workdirTrim shortens the home prefix to ~ and then cuts, so the cut works on
// the shorter form. The home is the client's own ($HOME), which is the home of
// the daemon's host only when the daemon is local; a path that is not under it
// is left as it is (aae-orc-qe9nn).
func workdirTrim(s string) string {
	return middleEllipsis(tildeHome(s, os.Getenv("HOME")))
}

// tildeHome replaces a leading home directory with ~ when home is a whole path
// element of s. An unset or root home replaces nothing, and a sibling that
// shares the prefix (/home/user2 against /home/user) is not under it.
func tildeHome(s, home string) string {
	home = strings.TrimRight(home, "/")
	if home == "" {
		return s
	}
	switch {
	case s == home:
		return "~"
	case strings.HasPrefix(s, home+"/"):
		return "~" + s[len(home):]
	}
	return s
}

// runtimeBasename is the last path element of a runtime command, marked with
// an ellipsis where the path was cut. A name with no path is already short.
func runtimeBasename(s string) string {
	i := strings.LastIndex(s, "/")
	if i < 0 || i == len(s)-1 {
		return s
	}
	return "…/" + s[i+1:]
}

// middleEllipsis cuts a long directory in the middle, keeping a head and the
// tail, where the directory that tells two seats apart is.
func middleEllipsis(s string) string {
	r := []rune(s)
	if len(r) <= workdirMaxWidth {
		return s
	}
	head := workdirMaxWidth / 4
	tail := workdirMaxWidth - head - 1
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// tierColumns is the default set for a width: the leading part of
// fitPriority the width class allows, as registered columns.
func tierColumns(width int) []sessionColumn {
	upTo := fitTiers[len(fitTiers)-1].upTo
	for _, t := range fitTiers {
		if width >= t.minWidth {
			upTo = t.upTo
			break
		}
	}
	return namedColumns(fitPriority[:upTo])
}

// namedColumns looks names up in the registry and skips any that is not
// registered yet.
func namedColumns(names []string) []sessionColumn {
	var out []sessionColumn
	for _, n := range names {
		if c, ok := lookupSessionColumn(n); ok {
			out = append(out, c)
		}
	}
	return out
}

// tableWidth is the widest printed line of a rendered table.
func tableWidth(table string) int {
	w := 0
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
		w = max(w, utf8.RuneCountInString(line))
	}
	return w
}

// fitSessionTable renders the table for the options. With no width it is
// renderSessionTableCols unchanged. The default is fitted to the width by
// dropping columns from the tail of the priority list; a list the operator
// named is never cut, and warns when it does not fit.
func fitSessionTable(sessions []api.Session, cols []sessionColumn, o fitOptions) fitResult {
	if o.width <= 0 {
		// Not a terminal: byte for byte what get sessions printed before the
		// fit, nothing cut, whatever the columns hold.
		return fitResult{table: renderSessionTableCols(sessions, cols), cols: cols}
	}
	if o.explicit {
		table := renderTrimmed(sessions, cols, o)
		var warn string
		if w := tableWidth(table); w > o.width {
			warn = fmt.Sprintf("note: the selected columns are %d wide, wider than the terminal (%d); printing them all", w, o.width)
		}
		return fitResult{table: table, warn: warn, cols: cols}
	}

	fitted := tierColumns(o.width)
	table := renderTrimmed(sessions, fitted, o)
	for len(fitted) > 1 && tableWidth(table) > o.width {
		fitted = fitted[:len(fitted)-1]
		table = renderTrimmed(sessions, fitted, o)
	}
	shown := make(map[string]bool, len(fitted))
	for _, c := range fitted {
		shown[c.name] = true
	}
	hidden := 0
	for _, c := range namedColumns(fitPriority) {
		if !shown[c.name] {
			hidden++
		}
	}
	return fitResult{table: table, hidden: hidden, cols: fitted}
}

// renderTrimmed renders cols, cutting RUNTIME and WORKDIR unless --no-trunc.
func renderTrimmed(sessions []api.Session, cols []sessionColumn, o fitOptions) string {
	if o.noTrunc {
		return renderSessionTableCols(sessions, cols)
	}
	out := make([]sessionColumn, len(cols))
	for i, c := range cols {
		if trim, ok := columnTrim[c.name]; ok {
			pick := c.pick
			c.pick = func(r sessionRow) string { return trim(pick(r)) }
		}
		out[i] = c
	}
	return renderSessionTableCols(sessions, out)
}
