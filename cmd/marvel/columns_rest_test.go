package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// registryHeadersWithout is the registry's headers in registry order, leaving
// out the named columns and composer, which `rest` never holds.
func registryHeadersWithout(named ...string) []string {
	var out []string
	for _, c := range sessionColumnRegistry {
		if restReserved[c.name] || slices.Contains(named, c.name) {
			continue
		}
		out = append(out, c.header)
	}
	return out
}

func headerCount(cols []sessionColumn, header string) int {
	n := 0
	for _, c := range cols {
		if c.header == header {
			n++
		}
	}
	return n
}

// `rest` expands in place to the registry in registry order, without any
// column named elsewhere in the list and without the reserved ones.
func TestColumnsRestExpandsInPlace(t *testing.T) {
	got := headersOf(columnsOrFatal(t, "name,state,rest,llm", nil))
	want := append(append([]string{"AGENT NAME", "STATE"}, registryHeadersWithout("name", "state", "llm")...), "LLM")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("name,state,rest,llm = %v, want %v", got, want)
	}
	// The same list from the config preference reads the same.
	if pref := headersOf(columnsOrFatal(t, "", []string{"name", "state", "rest", "llm"})); !reflect.DeepEqual(pref, want) {
		t.Errorf("preference = %v, want %v", pref, want)
	}
}

// A column added to the registry shows up in `rest` with no other change.
func TestColumnsRestPicksUpANewRegistryEntry(t *testing.T) {
	saved := sessionColumnRegistry
	t.Cleanup(func() { sessionColumnRegistry = saved })
	sessionColumnRegistry = append(slices.Clone(saved), sessionColumn{
		name: "zzz-new", header: "ZZZ", pick: func(sessionRow) string { return "z" },
	})

	got := headersOf(columnsOrFatal(t, "name,rest", nil))
	if n := slices.Index(got, "ZZZ"); n != len(got)-1 {
		t.Errorf("name,rest = %v, want ZZZ last (the new registry entry)", got)
	}
	if want := append([]string{"AGENT NAME"}, registryHeadersWithout("name")...); !reflect.DeepEqual(got, want) {
		t.Errorf("name,rest = %v, want %v", got, want)
	}
}

// Reading composer captures every seat's pane, so only a direct name reaches
// it. `rest` alone does not ask the daemon for it.
func TestColumnsRestNeverHoldsComposer(t *testing.T) {
	for _, flag := range []string{"rest", "name,rest", "rest,wide"} {
		cols := columnsOrFatal(t, flag, nil)
		if n := headerCount(cols, "COMPOSER"); n != 0 {
			t.Errorf("%s holds COMPOSER %d times, want 0", flag, n)
		}
		if wantsComposer(cols) {
			t.Errorf("%s asks the daemon to read every pane", flag)
		}
	}
	for _, flag := range []string{"composer,rest", "rest,composer", "name,composer,rest"} {
		cols := columnsOrFatal(t, flag, nil)
		if n := headerCount(cols, "COMPOSER"); n != 1 {
			t.Errorf("%s holds COMPOSER %d times, want 1", flag, n)
		}
		if !wantsComposer(cols) {
			t.Errorf("%s names composer and does not ask for it", flag)
		}
	}
}

// A second `rest` is an error that names where the list came from. `rest`
// adds no duplicate beside `wide`, in either order.
func TestColumnsRestAtMostOnceAndNoDuplicates(t *testing.T) {
	_, err := selectSessionColumns("name,rest,rest", nil)
	if err == nil || !strings.Contains(err.Error(), "--columns") || !strings.Contains(err.Error(), "rest") {
		t.Errorf("flag with two rest: err = %v, want one naming --columns and rest", err)
	}
	_, err = selectSessionColumns("", []string{"rest", "rest"})
	if err == nil || !strings.Contains(err.Error(), "display.session_columns") {
		t.Errorf("preference with two rest: err = %v, want one naming display.session_columns", err)
	}

	wide := headersOf(columnsOrFatal(t, "wide", nil))
	extra := registryHeadersWithout(wideNames()...)
	for flag, want := range map[string][]string{
		"wide,rest": append(slices.Clone(wide), extra...),
		"rest,wide": append(slices.Clone(extra), wide...),
	} {
		got := headersOf(columnsOrFatal(t, flag, nil))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", flag, got, want)
		}
		seen := map[string]int{}
		for _, h := range got {
			seen[h]++
		}
		for h, n := range seen {
			if n != 1 {
				t.Errorf("%s prints %s %d times, want once", flag, h, n)
			}
		}
	}
}

func wideNames() []string { return slices.Clone(wideSessionColumns) }

// The order `rest` columns are cut in at a narrow terminal: the ones not in
// fitPriority first, in reverse registry order, then the others in reverse
// fitPriority order.
var restCutOrder = []string{
	"ACCT", "SPEC", "PROMPT", "LAST-TURN",
	"WORKSPACE", "GEN", "DESK", "RSS", "CPU%", "RUNTIME", "ACTIVE%", "AGE", "WORKDIR", "ROLE", "TEAM",
	"RATE", "TOUT", "LAST-ACTIVE", "CTX%", "HEALTH", "STATE",
}

// At 80 columns the pinned NAME and LLM stay and only `rest` columns are
// cut, the ones outside fitPriority first. The printed order is the list's.
// Off a terminal nothing is cut.
func TestColumnsRestIsTheOnlyThingCutAtANarrowWidth(t *testing.T) {
	s := fitSession("agent-0")
	cols := columnsOrFatal(t, "name,llm,rest", nil)

	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 80, explicit: true})
	shown := fitHeaders(got.table)
	if w := widest(got.table); w > 80 {
		t.Errorf("table is %d wide, want at most 80:\n%s", w, got.table)
	}
	for _, pinned := range []string{"AGENT NAME", "LLM"} {
		if !slices.Contains(shown, pinned) {
			t.Errorf("%s was cut; pinned columns are never cut:\n%s", pinned, got.table)
		}
	}
	for _, first := range []string{"ACCT", "SPEC", "PROMPT"} {
		if slices.Contains(shown, first) {
			t.Errorf("%s printed at 80; it is cut first:\n%s", first, got.table)
		}
	}
	// What was cut is a prefix of the cut order, never a column from the middle.
	cut := 0
	for cut < len(restCutOrder) && !slices.Contains(shown, restCutOrder[cut]) {
		cut++
	}
	for _, h := range restCutOrder[cut:] {
		if !slices.Contains(shown, h) {
			t.Errorf("%s was cut after %v, out of order; shown %v", h, restCutOrder[:cut], shown)
		}
	}
	// Nothing more is cut than the width needs: the last column cut would not
	// have fit back in, measured the way the fit measures (cells trimmed).
	if cut > 0 {
		back := restCutOrder[cut-1]
		var again []sessionColumn
		for _, c := range cols {
			if slices.Contains(shown, c.header) || c.header == back {
				again = append(again, c)
			}
		}
		if w := widest(renderTrimmed([]api.Session{s}, again, fitOptions{})); w <= 80 {
			t.Errorf("%s fits back in at %d wide, so it should not have been cut", back, w)
		}
	}
	if got.hidden != cut || got.hidden == 0 {
		t.Errorf("hidden = %d, want %d (the rest columns cut)", got.hidden, cut)
	}
	// Printed order is the list's order: the shown headers are a subsequence.
	full := headersOf(cols)
	at := -1
	for _, h := range shown {
		i := slices.Index(full, h)
		if i <= at {
			t.Errorf("%s printed out of list order: shown %v, list %v", h, shown, full)
		}
		at = i
	}
	if got.warn != "" {
		t.Errorf("pinned columns fit but the note printed: %s", got.warn)
	}

	off := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 0, explicit: true})
	if want := renderSessionTableCols([]api.Session{s}, cols); off.table != want || off.hidden != 0 || off.warn != "" {
		t.Errorf("off a terminal something was cut (hidden %d, warn %q):\n%s", off.hidden, off.warn, off.table)
	}
	if n := len(fitHeaders(off.table)); n != len(cols) {
		t.Errorf("off a terminal %d columns printed, want %d", n, len(cols))
	}
}

// When the pinned columns alone are too wide, today's note prints and `rest`
// adds nothing.
func TestColumnsRestAddsNothingWhenPinnedColumnsAreTooWide(t *testing.T) {
	s := fitSession(strings.Repeat("n", 90))
	cols := columnsOrFatal(t, "name,rest", nil)
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 80, explicit: true})
	if want := []string{"AGENT NAME"}; !reflect.DeepEqual(fitHeaders(got.table), want) {
		t.Errorf("columns = %v, want only the pinned %v", fitHeaders(got.table), want)
	}
	if !strings.Contains(got.warn, "wider than the terminal") {
		t.Errorf("warn = %q, want today's note", got.warn)
	}
	if got.hidden != len(cols)-1 {
		t.Errorf("hidden = %d, want %d (every rest column)", got.hidden, len(cols)-1)
	}
}

// `rest` is a reserved name: no registry entry may take it, and the error
// text lists it.
func TestColumnsRestIsReserved(t *testing.T) {
	for _, c := range sessionColumnRegistry {
		if c.name == "rest" {
			t.Errorf("registry entry %q is named rest, a reserved name that expands to the other columns", c.header)
		}
	}
	if !strings.Contains(validColumnNames(), "rest") {
		t.Errorf("validColumnNames() = %q, want it to list rest", validColumnNames())
	}
}

// A list of `rest` alone is cut to one column at the narrowest, never to a
// blank table.
func TestColumnsRestAloneStopsAtOneColumn(t *testing.T) {
	s := fitSession("agent-0")
	cols := columnsOrFatal(t, "rest", nil)
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 3, explicit: true})
	if n := len(fitHeaders(got.table)); n != 1 {
		t.Errorf("%d columns at width 3, want 1:\n%s", n, got.table)
	}
	if got.hidden != len(cols)-1 {
		t.Errorf("hidden = %d, want %d", got.hidden, len(cols)-1)
	}
	if got.warn == "" {
		t.Error("the one column is wider than the terminal and no note printed")
	}
}

// The screen get sessions prints cuts `rest` columns too, so the mark the fit
// reads survives the step before it, and the hidden line counts them.
func TestColumnsRestIsCutOnTheSessionsScreen(t *testing.T) {
	s := fitSession("agent-0")
	out := renderSessionsView([]api.Session{s}, columnsOrFatal(t, "name,llm,rest", nil), "", fitOptions{width: 80, explicit: true})
	if w := widest(out); w > 80 {
		t.Errorf("screen is %d wide, want at most 80:\n%s", w, out)
	}
	for _, kept := range []string{"AGENT NAME", "LLM"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%s missing from the screen:\n%s", kept, out)
		}
	}
	if strings.Contains(out, "ACCT") {
		t.Errorf("ACCT printed at 80, so no rest column was cut:\n%s", out)
	}
	if !strings.Contains(out, " columns hidden at this width\n") {
		t.Errorf("no hidden-columns line:\n%s", out)
	}
}
