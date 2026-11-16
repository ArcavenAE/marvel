package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The I-450 account split (docs/design/get-sessions-output.md section 4.1 and
// 4.3; rulings i450c1113-1 to -4). Every cell reads the fields the daemon stamps
// on the row (api.Session.LimitWindows, LimitReadAt, LimitStale), never the
// limit_reading_text the old ACCT cell parsed.

func pct(v float64) *float64 { return &v }

// splitSession is a claude seat on /acct/a with both windows read five minutes
// before lastActiveNow, the fixed clock withLastActiveClock sets.
func splitSession(name string) api.Session {
	s := acctSession(name, "/acct/a", api.ReadingFresh, "fresh 44% (five_hour)")
	five, seven := lastActiveNow.Add(2*time.Hour+30*time.Minute), lastActiveNow.Add(50*time.Hour+10*time.Minute)
	at := lastActiveNow.Add(-5 * time.Minute)
	s.LimitWindows = []api.LimitWindowView{
		{Name: "five_hour", UsedPercent: pct(44), ResetsAt: &five},
		{Name: "seven_day", UsedPercent: pct(12), ResetsAt: &seven},
	}
	s.LimitReadAt = &at
	return s
}

// cellsOf renders name plus cols for the sessions and returns each row's cells
// after the name, by agent name.
func cellsOf(t *testing.T, cols string, sessions ...api.Session) map[string][]string {
	t.Helper()
	withLastActiveClock(t)
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,"+cols, nil))
	out := map[string][]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n")[1:] {
		cells := splitColumns(line)
		out[cells[0]] = cells[1:]
	}
	return out
}

func TestAcctSetExpandsToSplitColumns(t *testing.T) {
	wantAcct := []string{"ACCOUNT", "5H%", "7D%", "5H-RESET", "7D-RESET", "READ"}
	if got := headersOf(columnsOrFatal(t, "acct", nil)); !slices.Equal(got, wantAcct) {
		t.Errorf("--columns acct = %v, want %v", got, wantAcct)
	}
	if got := headersOf(columnsOrFatal(t, "", []string{"acct"})); !slices.Equal(got, wantAcct) {
		t.Errorf("a saved display.session_columns of acct = %v, want %v, so a saved preference keeps working", got, wantAcct)
	}
	if got := headersOf(columnsOrFatal(t, "service", nil)); !slices.Equal(got, []string{"RUNTIME", "BACKEND", "CRED", "HOME", "LOGIN"}) {
		t.Errorf("--columns service = %v, want RUNTIME BACKEND CRED HOME LOGIN", got)
	}
	got := headersOf(columnsOrFatal(t, "name,acct,age", nil))
	want := []string{"AGENT NAME", "ACCOUNT", "5H%", "7D%", "5H-RESET", "7D-RESET", "READ", "AGE"}
	if !slices.Equal(got, want) {
		t.Errorf("name,acct,age = %v, want the set expanded in place: %v", got, want)
	}
}

func TestSplitColumnsRegistrySnapshot(t *testing.T) {
	want := map[string]string{
		"account": "ACCOUNT", "5h": "5H%", "7d": "7D%", "5h-reset": "5H-RESET", "7d-reset": "7D-RESET",
		"read": "READ", "backend": "BACKEND", "cred": "CRED", "home": "HOME", "login": "LOGIN",
		"cluster": "CLUSTER", "key": "KEY",
	}
	for name, header := range want {
		c, ok := lookupSessionColumn(name)
		if !ok {
			t.Errorf("column %q is not registered", name)
			continue
		}
		if c.header != header {
			t.Errorf("column %q header = %q, want %q", name, c.header, header)
		}
		if !optInColumns[name] {
			t.Errorf("column %q is not opt-in, so the default table would gain it", name)
		}
	}
	for _, gone := range []string{"acct", "service"} {
		if _, ok := lookupSessionColumn(gone); ok {
			t.Errorf("%q is still a registered column; no composite cell remains (ruling: acct and service are named sets)", gone)
		}
		if !strings.Contains(validColumnNames(), gone) {
			t.Errorf("validColumnNames does not list the set %q", gone)
		}
	}
}

// i450c1113-2: "No start column. Show only the figures the harness reports:
// percent used and the reset offset."
func TestNoStartColumnExists(t *testing.T) {
	for _, c := range sessionColumnRegistry {
		if strings.Contains(strings.ToLower(c.name), "start") || strings.Contains(strings.ToLower(c.header), "start") {
			t.Errorf("column %q (%s) is a start column", c.name, c.header)
		}
	}
}

func TestColumnsAccountAnd7dGiveExactlyTwoCells(t *testing.T) {
	cells := cellsOf(t, "account,7d", splitSession("a"), splitSession("b"))
	for name, row := range cells {
		if len(row) != 2 || row[1] != "12%" {
			t.Errorf("%s: cells = %v, want exactly ACCOUNT and 7D%% (12%%)", name, row)
		}
	}
}

// i450c1113-1: "The list alone decides. KEY becomes a normal column."
func TestKeyColumnSelectableAndMovable(t *testing.T) {
	if c, ok := lookupSessionColumn("key"); !ok || c.header != "KEY" {
		t.Fatalf("key is not a registered column with header KEY: %v %v", c, ok)
	}
	if got := headersOf(columnsOrFatal(t, "name,key", nil)); !slices.Equal(got, []string{"AGENT NAME", "KEY"}) {
		t.Errorf("name,key = %v", got)
	}
	if got := headersOf(columnsOrFatal(t, "key,name", nil)); !slices.Equal(got, []string{"KEY", "AGENT NAME"}) {
		t.Errorf("key,name = %v, want KEY moved to the front", got)
	}
	s := splitSession("a")
	s.Cluster = "k1"
	if got := cellsOf(t, "key", s)["a"]; len(got) != 1 || got[0] != "k1:ws/a" {
		t.Errorf("KEY cell = %v, want k1:ws/a", got)
	}
}

func setRows() []api.Session {
	a, b := splitSession("a"), splitSession("b")
	a.Cluster, b.Cluster = "k1", "k2"
	return []api.Session{a, b}
}

// On a set read with no list, KEY still leads; once a list is given, naming key
// places it and leaving it out drops it.
func TestSetReadKeyFollowsTheList(t *testing.T) {
	withLastActiveClock(t)
	if lead := fanOutLead(false); len(lead) != 1 || lead[0].name != "key" {
		t.Errorf("fanOutLead(no list) = %v, want the KEY column", lead)
	}
	if lead := fanOutLead(true); len(lead) != 0 {
		t.Errorf("fanOutLead(a list) = %v, want none: a list without key drops it", headersOf(lead))
	}
	cols := columnsOrFatal(t, "name", nil)
	noKey := renderSessionsView(setRows(), cols, "", fitOptions{lead: fanOutLead(true)})
	if strings.Contains(noKey, "KEY") {
		t.Errorf("a set read with --columns name printed KEY:\n%s", noKey)
	}
	withKey := renderSessionsView(setRows(), cols, "", fitOptions{lead: fanOutLead(false)})
	if !strings.HasPrefix(strings.TrimSpace(withKey), "KEY") {
		t.Errorf("a set read with no list does not lead with KEY:\n%s", withKey)
	}
	named := renderSessionsView(setRows(), columnsOrFatal(t, "name,key", nil), "", fitOptions{lead: fanOutLead(true)})
	if strings.Count(named, "KEY") != 1 || !strings.Contains(named, "AGENT NAME") {
		t.Errorf("name,key on a set read did not print KEY once, after the name:\n%s", named)
	}
}

// i450c1113-3, option 2: "wide gains account,7d,7d-reset,read."
func TestWideGainsTheAccountFigures(t *testing.T) {
	today := []string{
		"workspace", "team", "role", "generation", "name", "state", "health",
		"context", "cpu", "rss", "desk", "runtime", "llm", "tout", "rate", "prompt", "last-active", "last-turn", "age", "active",
	}
	want := append(slices.Clone(today), "account", "7d", "7d-reset", "read")
	if !slices.Equal(wideSessionColumns, want) {
		t.Errorf("wide = %v, want today's wide plus account, 7d, 7d-reset, read: %v", wideSessionColumns, want)
	}
}

func TestResetPrintsAnOffsetFromNow(t *testing.T) {
	cases := []struct {
		name  string
		ahead time.Duration
		want  string
	}{
		{"59 minutes", 59 * time.Minute, "+59m"},
		{"60 minutes", 60 * time.Minute, "+1h0m"},
		{"23 hours 59 minutes", 23*time.Hour + 59*time.Minute, "+23h59m"},
		{"seconds are dropped", 59*time.Minute + 59*time.Second, "+59m"},
		{"under a minute ahead", 30 * time.Second, "+0m"},
		{"two days three hours", 51*time.Hour + 20*time.Minute, "+2d3h"},
		{"exactly now", 0, "now"},
		{"one second past", -time.Second, "now"},
		{"long past", -48 * time.Hour, "now"},
	}
	for _, c := range cases {
		s := splitSession("a")
		at := lastActiveNow.Add(c.ahead)
		s.LimitWindows = []api.LimitWindowView{
			{Name: "five_hour", UsedPercent: pct(44), ResetsAt: &at},
			{Name: "seven_day", UsedPercent: pct(12), ResetsAt: &at},
		}
		got := cellsOf(t, "5h-reset,7d-reset", s)["a"]
		if len(got) != 2 || got[0] != c.want || got[1] != c.want {
			t.Errorf("%s: resets = %v, want %q for both windows", c.name, got, c.want)
		}
	}
}

func TestResetOfAWindowWithNoResetTimeIsADash(t *testing.T) {
	s := splitSession("a")
	s.LimitWindows = []api.LimitWindowView{{Name: "five_hour", UsedPercent: pct(44)}}
	got := cellsOf(t, "5h,5h-reset,7d,7d-reset", s)["a"]
	if !slices.Equal(got, []string{"44%", "-", "-", "-"}) {
		t.Errorf("cells = %v, want 44%% then dashes for the missing reset and the absent seven_day window", got)
	}
}

// A stale figure keeps its number and takes a trailing ?, and "-" never stands
// in for a stale figure (section 4.3).
func TestStaleFigureKeepsNumberWithMark(t *testing.T) {
	fresh := splitSession("fresh")
	stale := splitSession("stale")
	stale.LimitReading, stale.LimitStale = api.ReadingStale, true
	cells := cellsOf(t, "5h,7d", fresh, stale)
	if !slices.Equal(cells["fresh"], []string{"44%", "12%"}) {
		t.Errorf("fresh = %v, want 44%% 12%%", cells["fresh"])
	}
	if !slices.Equal(cells["stale"], []string{"44%?", "12%?"}) {
		t.Errorf("stale = %v, want the numbers kept with a trailing ?", cells["stale"])
	}
}

func TestFigureRoundsToAWholePercent(t *testing.T) {
	s := splitSession("a")
	s.LimitWindows[0].UsedPercent = pct(44.6)
	s.LimitWindows[1].UsedPercent = pct(0)
	if got := cellsOf(t, "5h,7d", s)["a"]; !slices.Equal(got, []string{"45%", "0%"}) {
		t.Errorf("cells = %v, want 45%% and 0%% (a reported zero is a figure, not a dash)", got)
	}
}

func TestReadPrintsTheAgeOfTheReading(t *testing.T) {
	five, twoH := splitSession("five"), splitSession("twoh")
	at := lastActiveNow.Add(-2*time.Hour - 5*time.Minute)
	twoH.LimitReadAt = &at
	none := splitSession("none")
	none.LimitReadAt = nil
	cells := cellsOf(t, "read", five, twoH, none)
	if cells["five"][0] != "5m" || cells["twoh"][0] != "2h5m" || cells["none"][0] != "-" {
		t.Errorf("READ = %q %q %q, want 5m 2h5m -", cells["five"], cells["twoh"], cells["none"])
	}
}

// Against a daemon that sends no per-window figures, the new columns print "-"
// and the old text is never parsed for a figure.
func TestOldDaemonRowPrintsDashesInTheNewColumns(t *testing.T) {
	old := acctSession("old", "/acct/a", api.ReadingFresh, "fresh 99% (five_hour)")
	got := cellsOf(t, "5h,7d,5h-reset,7d-reset,read", old)["old"]
	if !slices.Equal(got, []string{"-", "-", "-", "-", "-"}) {
		t.Errorf("an old daemon's row = %v, want five dashes", got)
	}
	if strings.Contains(strings.Join(got, " "), "99") {
		t.Errorf("the figure came from limit_reading_text: %v", got)
	}
}

func TestAccountCellIsTheKeyAndADashOnAnUnstampedRow(t *testing.T) {
	stamped := splitSession("stamped")
	unstamped := splitSession("unstamped")
	unstamped.LimitReading, unstamped.LimitWindows, unstamped.LimitReadAt = "", nil, nil
	cells := cellsOf(t, "account", stamped, unstamped)
	if !acctKeyShape.MatchString(cells["stamped"][0]) {
		t.Errorf("ACCOUNT = %q, want 6 hex characters and nothing else", cells["stamped"][0])
	}
	if cells["unstamped"][0] != "-" {
		t.Errorf("an unstamped row's ACCOUNT = %q, want -", cells["unstamped"][0])
	}
}

// A default-home seat on each cluster hashes to its own id, because the cluster
// is part of the hash on a set read; a plain read hashes without it.
func TestAcctKeyDiffersAcrossClusterForEmptyHome(t *testing.T) {
	mk := func(name, cluster string) api.Session {
		s := splitSession(name)
		s.AccountHome, s.Cluster = "", cluster
		return s
	}
	cells := cellsOf(t, "account", mk("one", "k1"), mk("two", "k2"), mk("plain", ""))
	if cells["one"][0] == cells["two"][0] {
		t.Errorf("two clusters' default-home seats share the ACCOUNT %q", cells["one"][0])
	}
	if want := acctKey(clientAccountKey(mk("plain", ""))); cells["plain"][0] != want {
		t.Errorf("a plain read prints %q, want the cluster-free key %q", cells["plain"][0], want)
	}
}

// The client config map keys on the cluster-free id, so one account carries one
// name on a plain read and on a set read; "cluster/id" names it on one cluster.
func TestAccountNameMapsToTheSameNameOnPlainAndSetReads(t *testing.T) {
	plain, k1, k2 := splitSession("plain"), splitSession("k1"), splitSession("k2")
	k1.Cluster, k2.Cluster = "k1", "k2"
	id := acctKey(clientAccountKey(plain))
	old := accountNames
	t.Cleanup(func() { accountNames = old })
	accountNames = map[string]string{id: "work", "k2/" + id: "ultra"}

	cells := cellsOf(t, "account", plain, k1, k2)
	if cells["plain"][0] != "work" || cells["k1"][0] != "work" {
		t.Errorf("plain and set reads print %q and %q, want the same mapped name work", cells["plain"][0], cells["k1"][0])
	}
	if cells["k2"][0] != "ultra" {
		t.Errorf("the cluster-qualified entry did not apply on k2: %q", cells["k2"][0])
	}
}

func TestAccountPrefersTheDeclaredLabelOverTheKeyAndTheMapOverBoth(t *testing.T) {
	labelled, bare := splitSession("labelled"), splitSession("bare")
	labelled.Runtime.AccountLabel = "alt-ultra"
	bare.AccountHome = "/acct/b"
	cells := cellsOf(t, "account", labelled, bare)
	if cells["labelled"][0] != "alt-ultra" {
		t.Errorf("labelled = %q, want the declared account_label", cells["labelled"][0])
	}
	if !acctKeyShape.MatchString(cells["bare"][0]) {
		t.Errorf("bare = %q, want the hex key", cells["bare"][0])
	}
	old := accountNames
	t.Cleanup(func() { accountNames = old })
	accountNames = map[string]string{acctKey(clientAccountKey(labelled)): "mapped"}
	if got := cellsOf(t, "account", labelled)["labelled"][0]; got != "mapped" {
		t.Errorf("with a map entry, ACCOUNT = %q, want mapped", got)
	}
}

func TestSplitServiceCells(t *testing.T) {
	s := svcSession("a", api.BackendSubscription, api.BackendDefaultName, api.BackendCredentialAmbient, "/h/alt-ultra", "ultra")
	s.Cluster = "k1"
	got := cellsOf(t, "backend,cred,home,login,cluster", s)["a"]
	want := []string{"subscription", "ambient", "alt-ultra", "ultra", "k1"}
	if !slices.Equal(got, want) {
		t.Errorf("cells = %v, want %v", got, want)
	}
	bare := svcSession("b", "", "", "", "", "")
	if got := cellsOf(t, "backend,cred,login,cluster", bare)["b"]; !slices.Equal(got, []string{"-", "-", "-", "-"}) {
		t.Errorf("a never-classified row = %v, want four dashes", got)
	}
}

// The default table is byte for byte what it was: the new view fields, set or
// not, change nothing in it.
func TestDefaultTableIgnoresTheNewFields(t *testing.T) {
	withLastActiveClock(t)
	plain := splitSession("a")
	bare := plain
	bare.LimitWindows, bare.LimitReadAt, bare.LimitStale = nil, nil, false
	cols := defaultSessionColumns()
	if a, b := renderSessionTableCols([]api.Session{plain}, cols), renderSessionTableCols([]api.Session{bare}, cols); a != b {
		t.Errorf("the default table changed with the new fields:\n%s\nvs\n%s", a, b)
	}
	want := []string{"WORKSPACE", "TEAM", "ROLE", "GEN", "AGENT NAME", "STATE", "HEALTH", "CTX%", "ACTIVE%", "LAST-ACTIVE", "AGE", "LLM"}
	if got := headersOf(cols); !slices.Equal(got, want) {
		t.Errorf("default headers = %v, want %v", got, want)
	}
}

// -o json is the daemon's rows as sent, so the numeric percent, the reset time,
// the reading time and the stale boolean reach a script unchanged, and the
// column work adds nothing to it.
func TestGetSessionsJSONCarriesTheLimitFiguresUnchanged(t *testing.T) {
	s := splitSession("a")
	s.LimitStale = true
	cannedDaemon(t, map[string]any{"session": []api.Session{s}})
	out, err := runGet(t, "sessions", "-o", "json")
	if err != nil {
		t.Fatalf("get sessions -o json: %v", err)
	}
	var list []struct {
		Windows []struct {
			Name        string   `json:"name"`
			UsedPercent *float64 `json:"used_percent"`
		} `json:"limit_windows"`
		Stale  bool   `json:"limit_stale"`
		ReadAt string `json:"limit_read_at"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	r := list[0]
	if len(r.Windows) != 2 || r.Windows[0].UsedPercent == nil || *r.Windows[0].UsedPercent != 44 || !r.Stale || r.ReadAt == "" {
		t.Errorf("json = %+v, want two windows with numeric percents, limit_stale true and a read time", r)
	}
	if strings.Contains(out, "44%?") {
		t.Errorf("a table cell with its stale mark leaked into the json:\n%s", out)
	}
}
