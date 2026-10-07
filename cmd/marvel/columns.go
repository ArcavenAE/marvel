package main

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// sessionColumn is one selectable `get sessions` column. name is the long
// name the --columns flag and display.session_columns use; header is what
// the table prints; pick reads the cell from a derived row.
type sessionColumn struct {
	name   string
	header string
	pick   func(sessionRow) string
}

// sessionColumnRegistry is every selectable column, in the order the
// default table prints them. A new column is one entry here; the
// selector, the error text and the renderer all read this list.
var sessionColumnRegistry = []sessionColumn{
	{"workspace", "WORKSPACE", func(r sessionRow) string { return r.workspace }},
	{"team", "TEAM", func(r sessionRow) string { return r.team }},
	{"role", "ROLE", func(r sessionRow) string { return r.role }},
	{"generation", "GEN", func(r sessionRow) string { return r.generation }},
	{"name", "AGENT NAME", func(r sessionRow) string { return r.name }},
	{"state", "STATE", func(r sessionRow) string { return r.state }},
	{"health", "HEALTH", func(r sessionRow) string { return r.health }},
	{"context", "CTX%", func(r sessionRow) string { return r.context }},
	{"cpu", "CPU%", func(r sessionRow) string { return r.cpu }},
	{"rss", "RSS", func(r sessionRow) string { return r.rss }},
	{"desk", "DESK", func(r sessionRow) string { return r.desk }},
	{"runtime", "RUNTIME", func(r sessionRow) string { return r.runtime }},
	{"llm", "LLM", func(r sessionRow) string { return r.llm }},
	{"workdir", "WORKDIR", func(r sessionRow) string { return r.workdir }},
	{"tout", "TOUT", func(r sessionRow) string { return r.tout }},
	{"rate", "RATE", func(r sessionRow) string { return r.rate }},
	{"last-active", "LAST-ACTIVE", func(r sessionRow) string { return r.lastActive }},
	{"prompt", "PROMPT", func(r sessionRow) string { return r.prompt }},
}

// optInColumns are registered and selectable but not in the full default
// table, so a pipe prints what it always printed. The width fit's wide tier
// is where they show by default.
var optInColumns = map[string]bool{"workdir": true, "tout": true, "rate": true, "prompt": true, "last-active": true}

// columnSetWide is the one named set. A set expands in place wherever its
// name appears, because no `-o wide` exists (`-w` is --watch). It starts
// as today's columns and is where later columns that belong in a wide view
// are added.
const columnSetWide = "wide"

var wideSessionColumns = []string{
	"workspace", "team", "role", "generation", "name", "state", "health",
	"context", "cpu", "rss", "desk", "runtime", "llm", "tout", "rate", "prompt", "last-active",
}

// defaultSessionColumns is today's table, in today's order.
func defaultSessionColumns() []sessionColumn {
	out := make([]sessionColumn, 0, len(sessionColumnRegistry))
	for _, c := range sessionColumnRegistry {
		if !optInColumns[c.name] {
			out = append(out, c)
		}
	}
	return out
}

// validColumnNames lists what a name may be, for an error message.
func validColumnNames() string {
	names := make([]string, 0, len(sessionColumnRegistry)+1)
	for _, c := range sessionColumnRegistry {
		names = append(names, c.name)
	}
	names = append(names, columnSetWide)
	return strings.Join(names, ", ")
}

// lookupSessionColumn finds a column by long name.
func lookupSessionColumn(name string) (sessionColumn, bool) {
	for _, c := range sessionColumnRegistry {
		if c.name == name {
			return c, true
		}
	}
	return sessionColumn{}, false
}

// expandColumnNames turns names into columns, in the order given, with a
// set name expanding in place. source names where the list came from, so
// an error says which setting to fix.
func expandColumnNames(names []string, source string) ([]sessionColumn, error) {
	var out []sessionColumn
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("%s: empty column name (valid: %s)", source, validColumnNames())
		}
		if name == columnSetWide {
			cols, err := expandColumnNames(wideSessionColumns, source)
			if err != nil {
				return nil, err
			}
			out = append(out, cols...)
			continue
		}
		c, ok := lookupSessionColumn(name)
		if !ok {
			return nil, fmt.Errorf("%s: unknown column %q (valid: %s)", source, name, validColumnNames())
		}
		out = append(out, c)
	}
	return out, nil
}

// selectSessionColumns resolves the columns to print: the flag over the
// preference over the default. An empty flag means it was not given.
func selectSessionColumns(flag string, preference []string) ([]sessionColumn, error) {
	switch {
	case flag != "":
		return expandColumnNames(strings.Split(flag, ","), "--columns")
	case len(preference) > 0:
		return expandColumnNames(preference, "display.session_columns")
	default:
		return defaultSessionColumns(), nil
	}
}

// loadSessionColumns reads the preference from the client config and
// resolves it against the flag. The config is not read when the flag is
// given. A config that cannot be read leaves the default table, as an
// unreadable config already leaves the default socket: a display
// preference is never a reason to refuse a listing.
func loadSessionColumns(flag string) ([]sessionColumn, error) {
	cols, _, err := loadSessionColumnsSel(flag)
	return cols, err
}

// loadSessionColumnsSel is loadSessionColumns that also says whether the
// operator named the columns, by the flag or the preference. The width fit
// cuts only a default it chose itself.
func loadSessionColumnsSel(flag string) ([]sessionColumn, bool, error) {
	if flag != "" {
		cols, err := selectSessionColumns(flag, nil)
		return cols, true, err
	}
	cfg, _ := config.Load()
	if cfg == nil {
		cols, err := selectSessionColumns("", nil)
		return cols, false, err
	}
	cols, err := selectSessionColumns("", cfg.Display.SessionColumns)
	return cols, len(cfg.Display.SessionColumns) > 0, err
}

// renderSessionTableCols renders the table with the given columns.
func renderSessionTableCols(sessions []api.Session, cols []sessionColumn) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.header
	}
	_, _ = fmt.Fprintln(w, strings.Join(headers, "\t"))
	cells := make([]string, len(cols))
	for _, s := range sessions {
		row := newSessionRow(s)
		for i, c := range cols {
			cells[i] = c.pick(row)
		}
		_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	_ = w.Flush()
	return buf.String()
}
