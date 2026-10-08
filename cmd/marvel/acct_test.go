package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// acctSession is a session on the claude harness whose account home is home,
// with the reading state and text the daemon would stamp on it.
func acctSession(name, home string, state api.ReadingState, text string) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.Runtime.Name = "claude"
	s.AccountHome = home
	s.LimitReading = state
	s.LimitReadingText = text
	return s
}

func acctCells(t *testing.T, sessions ...api.Session) (map[string]string, string) {
	t.Helper()
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,acct", nil))
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n")[1:] {
		cells := splitColumns(line)
		out[cells[0]] = cells[1]
	}
	return out, table
}

var acctKeyShape = regexp.MustCompile(`^[0-9a-f]{6}$`)

// ACCT is registered, selectable by name, opt-in, and not in the wide set: an
// account reading is a view of one account, so it appears only when asked for.
func TestAcctColumnIsOptIn(t *testing.T) {
	c, ok := lookupSessionColumn("acct")
	if !ok {
		t.Fatal("acct is not a registered column")
	}
	if c.header != "ACCT" {
		t.Errorf("header = %q, want ACCT", c.header)
	}
	if !optInColumns["acct"] {
		t.Error("acct is not opt-in, so the default table would gain a column")
	}
	if slices.Contains(wideSessionColumns, "acct") {
		t.Error("acct is in the wide set")
	}
}

// Every row prints its own account's key and reading. Two sessions on one
// account print the same cell; a session on another account prints its own.
// Nothing is summed, averaged or totalled across rows.
func TestAcctColumnRepeatsNeverAggregates(t *testing.T) {
	got, table := acctCells(t,
		acctSession("a-one", "/acct/a", api.ReadingFresh, "fresh 87% (seven_day)"),
		acctSession("b-two", "/acct/a", api.ReadingFresh, "fresh 87% (seven_day)"),
		acctSession("c-other", "/acct/b", api.ReadingFresh, "fresh 40% (five_hour)"),
	)
	if got["a-one"] != got["b-two"] {
		t.Errorf("two sessions on one account print %q and %q, want the same cell", got["a-one"], got["b-two"])
	}
	for name, want := range map[string]string{"a-one": "87% (seven_day)", "b-two": "87% (seven_day)", "c-other": "40% (five_hour)"} {
		key, reading, _ := strings.Cut(got[name], " ")
		if !acctKeyShape.MatchString(key) || reading != want {
			t.Errorf("%s: cell = %q, want a 6 hex key then %q", name, got[name], want)
		}
	}
	if k1, _, _ := strings.Cut(got["a-one"], " "); k1 == strings.Fields(got["c-other"])[0] {
		t.Errorf("two accounts share the key %q", k1)
	}
	if n := len(strings.Split(strings.TrimRight(table, "\n"), "\n")); n != 4 {
		t.Errorf("the table has %d lines, want a header and 3 rows and no total row:\n%s", n, table)
	}
	for _, sum := range []string{"174%", "127%", "214%", "58%", "total", "sum"} {
		if strings.Contains(strings.ToLower(table), sum) {
			t.Errorf("the table carries %q, a figure aggregated across rows:\n%s", sum, table)
		}
	}
}

// A stale reading prints the word, never the old number or its timestamp.
func TestAcctStaleReadingPrintsWordNotNumber(t *testing.T) {
	got, _ := acctCells(t, acctSession("a-stale", "/acct/a", api.ReadingStale, "stale (last 2026-10-03T22:10:00Z)"))
	key, reading, _ := strings.Cut(got["a-stale"], " ")
	if !acctKeyShape.MatchString(key) || reading != "stale" {
		t.Errorf("a stale reading = %q, want a 6 hex key then the word stale", got["a-stale"])
	}
}

// A seat with no reading shows a dash after its key, and a row the daemon did
// not stamp (a held role's synthetic row) shows a bare dash: nothing is
// measured, so no 0%.
func TestAcctNoReadingIsADash(t *testing.T) {
	got, _ := acctCells(t,
		acctSession("a-none", "/acct/a", api.ReadingNone, "none"),
		acctSession("b-unstamped", "/acct/a", "", ""),
	)
	if key, reading, _ := strings.Cut(got["a-none"], " "); !acctKeyShape.MatchString(key) || reading != "-" {
		t.Errorf("no reading = %q, want a 6 hex key then -", got["a-none"])
	}
	if got["b-unstamped"] != "-" {
		t.Errorf("an unstamped row = %q, want -", got["b-unstamped"])
	}
}

// The client may run on another account than the daemon (a --cluster client).
// The daemon groups accounts by its own home, so the client must not fold the
// default home into a spelling using its own: with the client's HOME at
// /home/c, a default-home row and a row naming /home/c/.claude are two
// logins as far as the client can tell, and the same row must keep one key
// whatever HOME the client has.
func TestAcctKeyDoesNotUseTheClientHome(t *testing.T) {
	t.Setenv("HOME", "/home/c")
	got, _ := acctCells(t,
		acctSession("a-default", "", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("b-clienthome", "/home/c/.claude", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("c-daemonhome", "/home/d/.claude", api.ReadingFresh, "fresh 10% (five_hour)"),
	)
	key := func(n string) string { k, _, _ := strings.Cut(got[n], " "); return k }
	if key("a-default") == key("b-clienthome") {
		t.Errorf("a default-home row and a /home/c/.claude row share the key %q: a false merge", key("a-default"))
	}

	before := key("c-daemonhome")
	t.Setenv("HOME", "/home/other")
	again, _ := acctCells(t, acctSession("c-daemonhome", "/home/d/.claude", api.ReadingFresh, "fresh 10% (five_hour)"))
	if k, _, _ := strings.Cut(again["c-daemonhome"], " "); k != before {
		t.Errorf("the same row prints %q under one client HOME and %q under another", before, k)
	}
}

// The key is built from the fields of the account, not from the text a view
// prints for them: AccountKey.String spells an empty home "default-home", so a
// home literally named that must not read as the default row.
func TestAcctKeyKeepsTheDefaultHomeApartFromAHomeNamedLikeIt(t *testing.T) {
	got, _ := acctCells(t,
		acctSession("a-default", "", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("b-literal", "default-home", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("c-dotslash", "./default-home", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("d-slash", "default-home/", api.ReadingFresh, "fresh 10% (five_hour)"),
	)
	def, _, _ := strings.Cut(got["a-default"], " ")
	for _, n := range []string{"b-literal", "c-dotslash", "d-slash"} {
		if k, _, _ := strings.Cut(got[n], " "); k == def {
			t.Errorf("%s shares the key %q with the default-home row: a false merge", n, k)
		}
	}
}

// Homes that differ only in the directories above their last element are
// different logins and keep different keys, so a key built from the base name
// alone would merge them.
func TestAcctKeyKeepsHomesWithOneBaseNameApart(t *testing.T) {
	got, _ := acctCells(t,
		acctSession("a-c", "/home/c/.claude", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("b-d", "/home/d/.claude", api.ReadingFresh, "fresh 10% (five_hour)"),
	)
	k1, _, _ := strings.Cut(got["a-c"], " ")
	k2, _, _ := strings.Cut(got["b-d"], " ")
	if k1 == k2 {
		t.Errorf("two homes with one base name share the key %q: a false merge", k1)
	}
}
