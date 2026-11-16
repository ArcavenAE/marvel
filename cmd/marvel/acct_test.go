package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
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

// acctCells renders name,account and returns the ACCOUNT cell by agent name.
func acctCells(t *testing.T, sessions ...api.Session) map[string]string {
	t.Helper()
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,account", nil))
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n")[1:] {
		cells := splitColumns(line)
		out[cells[0]] = cells[1]
	}
	return out
}

var acctKeyShape = regexp.MustCompile(`^[0-9a-f]{6}$`)

// Two sessions on one account print the same cells; a session on another
// account prints its own. Nothing is summed, averaged or totalled across rows.
func TestAcctColumnRepeatsNeverAggregates(t *testing.T) {
	other := splitSession("c-other")
	other.AccountHome = "/acct/b"
	other.LimitWindows[0].UsedPercent = pct(40)
	other.LimitWindows[1].UsedPercent = pct(87)
	got := cellsOf(t, "account,5h,7d", splitSession("a-one"), splitSession("b-two"), other)
	if !slices.Equal(got["a-one"], got["b-two"]) {
		t.Errorf("two sessions on one account print %q and %q, want the same cells", got["a-one"], got["b-two"])
	}
	for name, want := range map[string][]string{"a-one": {"44%", "12%"}, "b-two": {"44%", "12%"}, "c-other": {"40%", "87%"}} {
		if !acctKeyShape.MatchString(got[name][0]) || !slices.Equal(got[name][1:], want) {
			t.Errorf("%s: cells = %v, want a 6 hex key then %v", name, got[name], want)
		}
	}
	if got["a-one"][0] == got["c-other"][0] {
		t.Errorf("two accounts share the key %q", got["a-one"][0])
	}
	table := renderSessionTableCols([]api.Session{splitSession("a-one"), splitSession("b-two"), other}, columnsOrFatal(t, "name,acct", nil))
	if n := len(strings.Split(strings.TrimRight(table, "\n"), "\n")); n != 4 {
		t.Errorf("the table has %d lines, want a header and 3 rows and no total row:\n%s", n, table)
	}
	for _, sum := range []string{"88%", "56%", "99%", "total", "sum"} {
		if strings.Contains(strings.ToLower(table), sum) {
			t.Errorf("the table carries %q, a figure aggregated across rows:\n%s", sum, table)
		}
	}
}

// A seat on an account with no reading shows its key and dashes for every
// figure, and a row the daemon did not stamp (a held role's synthetic row) shows
// a bare dash: nothing is measured, so no 0%.
func TestAcctNoReadingIsADash(t *testing.T) {
	none := acctSession("a-none", "/acct/a", api.ReadingNone, "none")
	got := cellsOf(t, "account,5h,7d,5h-reset,7d-reset,read", none, acctSession("b-unstamped", "/acct/a", "", ""))
	if !acctKeyShape.MatchString(got["a-none"][0]) || !slices.Equal(got["a-none"][1:], []string{"-", "-", "-", "-", "-"}) {
		t.Errorf("no reading = %v, want a 6 hex key then five dashes", got["a-none"])
	}
	if got["b-unstamped"][0] != "-" {
		t.Errorf("an unstamped row = %v, want a bare - for ACCOUNT", got["b-unstamped"])
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
	got := acctCells(t,
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
	again := acctCells(t, acctSession("c-daemonhome", "/home/d/.claude", api.ReadingFresh, "fresh 10% (five_hour)"))
	if k, _, _ := strings.Cut(again["c-daemonhome"], " "); k != before {
		t.Errorf("the same row prints %q under one client HOME and %q under another", before, k)
	}
}

// The key is built from the fields of the account, not from the text a view
// prints for them: AccountKey.String spells an empty home "default-home", so a
// home literally named that must not read as the default row.
func TestAcctKeyKeepsTheDefaultHomeApartFromAHomeNamedLikeIt(t *testing.T) {
	got := acctCells(t,
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
	got := acctCells(t,
		acctSession("a-c", "/home/c/.claude", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctSession("b-d", "/home/d/.claude", api.ReadingFresh, "fresh 10% (five_hour)"),
	)
	k1, _, _ := strings.Cut(got["a-c"], " ")
	k2, _, _ := strings.Cut(got["b-d"], " ")
	if k1 == k2 {
		t.Errorf("two homes with one base name share the key %q: a false merge", k1)
	}
}

// Each field of the account key keeps two accounts apart on its own. Rows that
// differ in only one of harness, backend or credential source, with the same
// home, must print different keys: a key that ignored one would show two logins
// as one account, the false merge the column exists to prevent.
func TestAcctKeyDiffersWhenAnyOneFieldDiffers(t *testing.T) {
	base := api.AccountKey{
		Harness:          "claude",
		Backend:          api.BackendBedrock,
		CredentialSource: api.BackendCredentialAWSProfile,
		ConfigHome:       "/acct/a",
	}
	for _, tc := range []struct {
		field  string
		change func(*api.AccountKey)
	}{
		{"harness", func(k *api.AccountKey) { k.Harness = "codex" }},
		{"backend", func(k *api.AccountKey) { k.Backend = api.BackendVertex }},
		{"credential source", func(k *api.AccountKey) { k.CredentialSource = api.BackendCredentialAmbient }},
		{"config home", func(k *api.AccountKey) { k.ConfigHome = "/acct/b" }},
	} {
		other := base
		tc.change(&other)
		if acctKey(base) == acctKey(other) {
			t.Errorf("keys do not differ when only the %s differs: %+v and %+v both print %s",
				tc.field, base, other, acctKey(base))
		}
	}
	// The case the column's own comment names: the default backend against
	// bedrock, with no home on either side.
	a := api.AccountKey{Harness: "claude", Backend: api.BackendDefaultName}
	b := api.AccountKey{Harness: "claude", Backend: api.BackendBedrock}
	if acctKey(a) == acctKey(b) {
		t.Errorf("claude default and claude bedrock with no home print one key %s", acctKey(a))
	}
}

// acctEnvSession is a claude session with no recorded account home whose role
// names dir as its config directory.
func acctEnvSession(name, dir string) api.Session {
	s := acctSession(name, "", api.ReadingFresh, "fresh 10% (five_hour)")
	if dir != "" {
		s.Runtime.Env = map[string]string{"CLAUDE_CONFIG_DIR": dir}
	}
	return s
}

// Two claude seats on different config directories are two logins, so the
// column must not print one key for both: it is how an operator sees which
// rows share a limit.
func TestAcctKeyFollowsAClaudeSeatsConfigDir(t *testing.T) {
	got := acctCells(t,
		acctEnvSession("a-none", ""),
		acctEnvSession("b-one", "/home/d/.claude-a"),
		acctEnvSession("c-one-again", "/home/d/.claude-a/"),
		acctEnvSession("d-two", "/home/d/.claude-b"),
	)
	key := func(n string) string { k, _, _ := strings.Cut(got[n], " "); return k }
	if key("a-none") == key("b-one") || key("b-one") == key("d-two") || key("a-none") == key("d-two") {
		t.Errorf("distinct logins share a key: none %q, a %q, b %q", key("a-none"), key("b-one"), key("d-two"))
	}
	if key("b-one") != key("c-one-again") {
		t.Errorf("one directory spelled two ways prints %q and %q", key("b-one"), key("c-one-again"))
	}
}

// A recorded account home is the harness's own answer and wins over the
// variable: a seat with a home and a variable prints the key of the home, not
// of the variable.
func TestAcctKeyPrefersARecordedHomeOverTheConfigDir(t *testing.T) {
	both := acctEnvSession("a-both", "/home/d/.claude-env")
	both.AccountHome = "/home/d/.claude-home"
	got := acctCells(t,
		both,
		acctSession("b-home-only", "/home/d/.claude-home", api.ReadingFresh, "fresh 10% (five_hour)"),
		acctEnvSession("c-env-only", "/home/d/.claude-env"),
	)
	key := func(n string) string { k, _, _ := strings.Cut(got[n], " "); return k }
	if key("a-both") != key("b-home-only") {
		t.Errorf("a seat with a recorded home prints %q, want the key of that home %q", key("a-both"), key("b-home-only"))
	}
	if key("a-both") == key("c-env-only") {
		t.Errorf("a seat with a recorded home prints the key of its variable %q", key("c-env-only"))
	}
}

// acctKeyOf is the key an unmapped, unlabelled session's ACCOUNT cell prints.
func acctKeyOf(s api.Session) string { return accountCell(s) }

// A row with no cluster hashes as it did before the cluster was folded in. The
// hex was captured on main at 76cd6950 for this session (svccol-4).
func TestAcctUnchangedWithoutCluster(t *testing.T) {
	s := acctSession("a", "/acct/a", api.ReadingFresh, "fresh 87% (seven_day)")
	if got := acctKeyOf(s); got != "613756" {
		t.Errorf("key = %q, want 613756 (unchanged from main)", got)
	}
}

// On a read of several clusters the cluster joins the fingerprint, so one
// home path on two clusters is two accounts, and two rows of one cluster still
// share a key.
func TestAcctFoldsClusterOnFanOut(t *testing.T) {
	mk := func(name, cluster string) api.Session {
		s := acctSession(name, "/acct/a", api.ReadingFresh, "fresh 87% (seven_day)")
		s.Cluster = cluster
		return s
	}
	a1, a2, b := acctKeyOf(mk("x", "a")), acctKeyOf(mk("y", "a")), acctKeyOf(mk("z", "b"))
	if a1 != a2 {
		t.Errorf("two rows of one cluster print %q and %q, want one key", a1, a2)
	}
	if a1 == b {
		t.Errorf("one home on two clusters prints one key %q", a1)
	}
	if a1 == "613756" || b == "613756" {
		t.Errorf("a cluster row kept the cluster-free key 613756")
	}
}

// One --cluster names one daemon, so the key is the one a plain read prints.
func TestAcctSingleClusterFlagDoesNotFold(t *testing.T) {
	s := acctSession("a-one", "/acct/a", api.ReadingFresh, "fresh 87% (seven_day)")
	a := startFake(t, fakeAnswers, []api.Session{s})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock})
	code, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a", "--columns", "name,acct")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "613756") {
		t.Errorf("a single-cluster read changed the key:\n%s", out)
	}
}

// --all-clusters with one configured cluster is a set read and folds.
func TestAcctAllClustersWithOneClusterFolds(t *testing.T) {
	s := acctSession("a-one", "/acct/a", api.ReadingFresh, "fresh 87% (seven_day)")
	a := startFake(t, fakeAnswers, []api.Session{s})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock})
	code, out, _ := runFanGet(t, "get", "sessions", "--all-clusters", "--columns", "name,acct")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(out, "613756") {
		t.Errorf("a one-cluster set read kept the cluster-free key:\n%s", out)
	}
}
