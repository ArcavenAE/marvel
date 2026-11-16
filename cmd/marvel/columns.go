package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// sessionColumn is one selectable `get sessions` column. name is the long
// name the --columns flag and display.session_columns use; header is what
// the table prints; pick reads the cell from a derived row. fromRest marks a
// column the `rest` token added, which is the only kind the width fit cuts
// from a list the operator named.
type sessionColumn struct {
	name     string
	header   string
	pick     func(sessionRow) string
	fromRest bool
}

// sessionColumnRegistry is every selectable column, in the order the
// default table prints them. A new column is one entry here; the
// selector, the error text and the renderer all read this list.
var sessionColumnRegistry = []sessionColumn{
	{name: "workspace", header: "WORKSPACE", pick: func(r sessionRow) string { return r.workspace }},
	{name: "team", header: "TEAM", pick: func(r sessionRow) string { return r.team }},
	{name: "role", header: "ROLE", pick: func(r sessionRow) string { return r.role }},
	{name: "generation", header: "GEN", pick: func(r sessionRow) string { return r.generation }},
	{name: "name", header: "AGENT NAME", pick: func(r sessionRow) string { return r.name }},
	{name: "state", header: "STATE", pick: func(r sessionRow) string { return r.state }},
	{name: "health", header: "HEALTH", pick: func(r sessionRow) string { return r.health }},
	{name: "composer", header: "COMPOSER", pick: func(r sessionRow) string { return r.composer }},
	{name: "pane", header: "PANE", pick: func(r sessionRow) string { return r.pane }},
	{name: "context", header: "CTX%", pick: func(r sessionRow) string { return r.context }},
	{name: "cpu", header: "mCPU", pick: func(r sessionRow) string { return r.cpu }},
	{name: "rss", header: "RSS", pick: func(r sessionRow) string { return r.rss }},
	{name: "desk", header: "DESK", pick: func(r sessionRow) string { return r.desk }},
	{name: "runtime", header: "RUNTIME", pick: func(r sessionRow) string { return r.runtime }},
	{name: "llm", header: "LLM", pick: func(r sessionRow) string { return r.llm }},
	{name: "workdir", header: "WORKDIR", pick: func(r sessionRow) string { return r.workdir }},
	{name: "tout", header: "TOUT", pick: func(r sessionRow) string { return r.tout }},
	{name: "rate", header: "RATE", pick: func(r sessionRow) string { return r.rate }},
	{name: "last-active", header: "LAST-ACTIVE", pick: func(r sessionRow) string { return r.lastActive }},
	{name: "last-turn", header: "LAST-TURN", pick: func(r sessionRow) string { return r.lastTurn }},
	{name: "age", header: "AGE", pick: func(r sessionRow) string { return r.age }},
	{name: "active", header: "ACTIVE%", pick: func(r sessionRow) string { return r.active }},
	{name: "prompt", header: "PROMPT", pick: func(r sessionRow) string { return r.prompt }},
	{name: "spec", header: "SPEC", pick: func(r sessionRow) string { return r.spec }},
	{name: "account", header: "ACCOUNT", pick: func(r sessionRow) string { return r.account }},
	{name: "5h", header: "5H%", pick: func(r sessionRow) string { return r.fiveH }},
	{name: "7d", header: "7D%", pick: func(r sessionRow) string { return r.sevenD }},
	{name: "5h-reset", header: "5H-RESET", pick: func(r sessionRow) string { return r.fiveReset }},
	{name: "7d-reset", header: "7D-RESET", pick: func(r sessionRow) string { return r.sevenReset }},
	{name: "read", header: "READ", pick: func(r sessionRow) string { return r.read }},
	{name: "backend", header: "BACKEND", pick: func(r sessionRow) string { return r.backend }},
	{name: "cred", header: "CRED", pick: func(r sessionRow) string { return r.cred }},
	{name: "home", header: "HOME", pick: func(r sessionRow) string { return r.home }},
	{name: "login", header: "LOGIN", pick: func(r sessionRow) string { return r.login }},
	{name: "cluster", header: "CLUSTER", pick: func(r sessionRow) string { return r.cluster }},
	{name: "key", header: "KEY", pick: func(r sessionRow) string { return r.key }},
}

// optInColumns are registered and selectable but not in the default table.
// The width fit's wide tier is where they show by default.
var optInColumns = map[string]bool{"composer": true, "pane": true, "workdir": true, "tout": true, "rate": true, "prompt": true, "last-turn": true, "spec": true, "account": true, "5h": true, "7d": true, "5h-reset": true, "7d-reset": true, "read": true, "backend": true, "cred": true, "home": true, "login": true, "cluster": true, "key": true, "cpu": true, "rss": true, "desk": true, "runtime": true}

// wantsComposer reports whether the COMPOSER or PANE column is selected. Only
// then does the listing ask the daemon to read each seat's pane; both are
// opt-in and in no named set, so no default view captures a pane.
func wantsComposer(cols []sessionColumn) bool {
	for _, c := range cols {
		if c.name == "composer" || c.name == "pane" {
			return true
		}
	}
	return false
}

// columnSetWide is a named set. A set expands in place wherever its name
// appears, because no `-o wide` exists (`-w` is --watch). wide starts as
// today's columns plus the account figures; acct and service are the sets the
// retired composite cells became (I-450), so a saved preference of acct or
// service keeps working.
const columnSetWide = "wide"

var wideSessionColumns = []string{
	"workspace", "team", "role", "generation", "name", "state", "health",
	"context", "cpu", "rss", "desk", "runtime", "llm", "tout", "rate", "prompt", "last-active", "last-turn", "age", "active",
	"account", "7d", "7d-reset", "read",
}

// columnSets maps each set name to its members, in print order.
var columnSets = map[string][]string{
	columnSetWide: wideSessionColumns,
	"acct":        {"account", "5h", "7d", "5h-reset", "7d-reset", "read"},
	"service":     {"runtime", "backend", "cred", "home", "login"},
}

// columnSetNames lists the sets in a fixed order for the error text.
var columnSetNames = []string{columnSetWide, "acct", "service"}

// columnRest is a reserved name that expands in place to every registered
// column the list does not name elsewhere. No registry entry may take it.
const columnRest = "rest"

// restReserved are the registered columns `rest` never holds, because
// reading them costs something the operator should ask for by name:
// composer and pane capture every seat's pane.
var restReserved = map[string]bool{"composer": true, "pane": true}

// defaultColumnNames is the order the default table prints: who the seat is,
// whether it is doing work, then which model.
var defaultColumnNames = []string{
	"workspace", "team", "role", "generation", "name", "state", "health",
	"context", "active", "last-active", "age", "llm",
}

// defaultSessionColumns is the default table: the registered columns that are
// not opt-in, in defaultColumnNames order.
func defaultSessionColumns() []sessionColumn {
	out := make([]sessionColumn, 0, len(sessionColumnRegistry))
	for _, c := range sessionColumnRegistry {
		if !optInColumns[c.name] {
			out = append(out, c)
		}
	}
	return inPrintOrder(out)
}

// inPrintOrder sorts cols into the order the default table prints: the
// defaultColumnNames first, in that order, then any other column in registry
// order. The sort is stable, so a list with a repeated name keeps its copies
// together.
func inPrintOrder(cols []sessionColumn) []sessionColumn {
	rank := func(name string) int {
		if i := slices.Index(defaultColumnNames, name); i >= 0 {
			return i
		}
		for i, c := range sessionColumnRegistry {
			if c.name == name {
				return len(defaultColumnNames) + i
			}
		}
		return len(defaultColumnNames) + len(sessionColumnRegistry)
	}
	out := slices.Clone(cols)
	slices.SortStableFunc(out, func(a, b sessionColumn) int { return rank(a.name) - rank(b.name) })
	return out
}

// validColumnNames lists what a name may be, for an error message.
func validColumnNames() string {
	names := make([]string, 0, len(sessionColumnRegistry)+1)
	for _, c := range sessionColumnRegistry {
		names = append(names, c.name)
	}
	names = append(names, columnSetNames...)
	names = append(names, columnRest)
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
//
// `rest` is filled in a second pass, once every other name is known, so it
// takes what is left wherever it is written: `rest,wide` and `wide,rest`
// both give it only the columns wide does not hold. It adds no duplicate;
// duplicates the operator wrote outside it stay as written.
func expandColumnNames(names []string, source string) ([]sessionColumn, error) {
	var out []sessionColumn
	restAt := -1
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("%s: empty column name (valid: %s)", source, validColumnNames())
		}
		if name == columnRest {
			if restAt >= 0 {
				return nil, fmt.Errorf("%s: %q appears more than once; it expands to every column not named elsewhere, so it is allowed once", source, columnRest)
			}
			restAt = len(out)
			continue
		}
		if members, isSet := columnSets[name]; isSet {
			cols, err := expandColumnNames(members, source)
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
	if restAt < 0 {
		return out, nil
	}
	named := make(map[string]bool, len(out))
	for _, c := range out {
		named[c.name] = true
	}
	var rest []sessionColumn
	for _, c := range sessionColumnRegistry {
		if named[c.name] || restReserved[c.name] {
			continue
		}
		c.fromRest = true
		rest = append(rest, c)
	}
	return slices.Concat(out[:restAt], rest, out[restAt:]), nil
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

// useAccountNames sets the ACCOUNT display names from a loaded config. A map the
// loader refused is not shown: accountNames stays empty and one note goes to
// stderr, without the refused value. The rest of the config is unaffected.
func useAccountNames(cfg *config.Config) {
	accountNames = nil
	if cfg == nil {
		return
	}
	if cfg.AccountNamesRefused != nil {
		fmt.Fprintln(os.Stderr, "warning: display.account_names was refused and is ignored; ACCOUNT prints labels and keys")
		return
	}
	accountNames = cfg.Display.AccountNames
}

// loadSessionColumnsSel is loadSessionColumns that also says whether the
// operator named the columns, by the flag or the preference. The width fit
// cuts only a default it chose itself.
func loadSessionColumnsSel(flag string) ([]sessionColumn, bool, error) {
	cfg, _ := config.Load()
	useAccountNames(cfg)
	if flag != "" {
		cols, err := selectSessionColumns(flag, nil)
		return cols, true, err
	}
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
	homes := countServiceHomes(sessions)
	for _, s := range sessions {
		row := newSessionRow(s, homes)
		for i, c := range cols {
			cells[i] = c.pick(row)
		}
		_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	_ = w.Flush()
	return buf.String()
}
