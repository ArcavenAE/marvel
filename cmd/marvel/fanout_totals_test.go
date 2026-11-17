package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The totals block of a one-shot fan-out read: docs/design/cluster-rollup-header.md,
// sections 2.3, 2.5 and 2.8, ticket T3 (aae-orc-onvts).

func clusterRow(cluster, name string, cpuPercent float64) api.Session {
	s := sampled(name, api.SessionRunning, cpuPercent, 1<<20)
	s.Cluster = cluster
	return s
}

func answeredRecord(name string) clusterRecord {
	return clusterRecord{Cluster: name, Outcome: outcomeAnswered}
}

// totalsBlockLines is the block's lines, without the final newline.
func totalsBlockLines(t *testing.T, records []clusterRecord, rows []api.Session, cols string) []string {
	t.Helper()
	withRateClock(t)
	out := fanOutTotalsBlock(records, rows, columnsOrFatal(t, cols, nil), rateNow, 0, nil)
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Each cluster that answered gets one line, from its own rows, in the order of
// the status lines. An answered cluster with no sessions prints 0 sessions and
// dashes, so it does not read as a cluster that did not answer; a cluster that
// did not answer and shows no rows prints none (design section 2.9).
func TestFanOutTotalsOneLinePerCluster(t *testing.T) {
	records := []clusterRecord{
		answeredRecord("a"),
		{Cluster: "gone", Outcome: outcomeUnreachable},
		answeredRecord("b"),
		answeredRecord("empty"),
	}
	rows := []api.Session{
		clusterRow("b", "b1", 40),
		clusterRow("a", "a1", 10),
		clusterRow("a", "a2", 15),
	}
	got := totalsBlockLines(t, records, rows, "name,cpu")
	want := []string{
		"totals a   2 sessions   mCPU 250 (2)",
		"totals b   1 session   mCPU 400 (1)",
		"totals empty   0 sessions   mCPU -",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Two names that reach one daemon are one load. The member gets no line; its
// kept rows count once on the leader's line, which names it.
func TestFanOutTotalsSameDaemonFoldsIntoLeader(t *testing.T) {
	n := 1
	records := []clusterRecord{
		answeredRecord("a"),
		{Cluster: "b", Outcome: outcomeSameDaemon, SameDaemonAs: "a", Sessions: &n},
	}
	rows := []api.Session{
		clusterRow("a", "a1", 10),
		clusterRow("a", "a2", 15),
		clusterRow("b", "b-only", 5), // the one row the member kept after the fold
	}
	got := totalsBlockLines(t, records, rows, "name,cpu")
	want := []string{"totals a (+ b, same daemon)   3 sessions   mCPU 300 (3)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A starting cluster may not have returned every row, so its line says so with
// a word, never a second +.
func TestFanOutTotalsStartingMarked(t *testing.T) {
	records := []clusterRecord{answeredRecord("a"), {Cluster: "c", Outcome: outcomeStarting}}
	rows := []api.Session{clusterRow("a", "a1", 10), clusterRow("c", "c1", 20)}
	got := totalsBlockLines(t, records, rows, "name,cpu")
	want := []string{
		"totals a   1 session   mCPU 100 (1)",
		"totals c (starting)   1 session   mCPU 200 (1)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Several members folded into one leader read "(+ b, c, same daemon)", in the
// order of the status lines, and each member's kept rows count once.
func TestFanOutTotalsSeveralFoldedMembers(t *testing.T) {
	n := 1
	records := []clusterRecord{
		answeredRecord("a"),
		{Cluster: "b", Outcome: outcomeSameDaemon, SameDaemonAs: "a", Sessions: &n},
		{Cluster: "c", Outcome: outcomeSameDaemon, SameDaemonAs: "a", Sessions: &n},
	}
	rows := []api.Session{
		clusterRow("c", "c-only", 20),
		clusterRow("a", "a1", 10),
		clusterRow("b", "b-only", 5),
	}
	got := totalsBlockLines(t, records, rows, "name,cpu")
	want := []string{"totals a (+ b, c, same daemon)   3 sessions   mCPU 350 (3)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A starting leader marks the folded line as a lower bound, after the note.
func TestFanOutTotalsStartingLeaderMarksTheFoldedLine(t *testing.T) {
	n := 1
	records := []clusterRecord{
		{Cluster: "a", Outcome: outcomeStarting},
		{Cluster: "b", Outcome: outcomeSameDaemon, SameDaemonAs: "a", Sessions: &n},
	}
	rows := []api.Session{clusterRow("a", "a1", 10), clusterRow("b", "b-only", 5)}
	got := totalsBlockLines(t, records, rows, "name,cpu")
	want := []string{"totals a (+ b, same daemon) (starting)   2 sessions   mCPU 150 (2)"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("block =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A starting member folded into an answered leader marks the leader's line: the
// fold overwrote the member's outcome, so the caller hands the block the names
// of the clusters that were starting when they were folded.
func TestFanOutTotalsStartingFoldedMemberMarksTheLeader(t *testing.T) {
	withRateClock(t)
	n := 1
	records := []clusterRecord{
		answeredRecord("a"),
		{Cluster: "b", Outcome: outcomeSameDaemon, SameDaemonAs: "a", Sessions: &n},
	}
	rows := []api.Session{clusterRow("a", "a1", 10), clusterRow("b", "b-only", 5)}
	want := "totals a (+ b, same daemon) (starting)   2 sessions   mCPU 150 (2)"
	got := strings.TrimRight(fanOutTotalsBlock(records, rows, columnsOrFatal(t, "name,cpu", nil), rateNow, 0, map[string]bool{"b": true}), "\n")
	if got != want {
		t.Errorf("a starting folded member should mark the leader's line:\n got %q\nwant %q", got, want)
	}
	// The control: the same records with no starting member carry no mark.
	got = strings.TrimRight(fanOutTotalsBlock(records, rows, columnsOrFatal(t, "name,cpu", nil), rateNow, 0, nil), "\n")
	if strings.Contains(got, "(starting)") {
		t.Errorf("no member was starting, so no mark:\n%q", got)
	}
}

// Through the command: --header prints the block above the merged table, even
// into a pipe; a plain pipe prints none; there is no grand total across
// clusters.
func TestFanOutTotalsNotIntoPipe(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{sampled("a1", api.SessionRunning, 10, 1<<20), sampled("a2", api.SessionRunning, 15, 1<<20)})
	b := startFake(t, fakeAnswers, []api.Session{sampled("b1", api.SessionRunning, 40, 1<<20)})
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: a.sock},
		config.Cluster{Name: "b", Socket: b.sock},
	)

	_, plain, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "name,cpu")
	if strings.Contains(plain, "totals") {
		t.Errorf("a plain pipe must not carry the block:\n%s", plain)
	}

	_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "name,cpu", "--header")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 || lines[0] != "totals a   2 sessions   mCPU 250 (2)" || lines[1] != "totals b   1 session   mCPU 400 (1)" {
		t.Errorf("--header should print one line per cluster above the table:\n%s", out)
	}
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "totals") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("want exactly two totals lines and no grand total, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "AGENT NAME") {
		t.Errorf("fixture: the merged table is missing:\n%s", out)
	}
}

// -o json on a fan-out read stays one document with no totals in it, with or
// without --header. (The document carries read times, so two runs differ; the
// check is that it still decodes as the one object and holds no totals line.)
func TestFanOutTotalsNotInTheJSONDocument(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{sampled("a1", api.SessionRunning, 10, 1<<20)})
	b := startFake(t, fakeAnswers, []api.Session{sampled("b1", api.SessionRunning, 40, 1<<20)})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--header", "-o", "json")
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("the document should open with the one object:\n%s", out)
	}
	if doc := decodeSet(t, out); len(doc.Clusters) != 2 || len(doc.Sessions) != 2 {
		t.Errorf("%d cluster records and %d rows, want 2 and 2", len(doc.Clusters), len(doc.Sessions))
	}
	if strings.Contains(out, "totals   ") {
		t.Errorf("-o json must not carry a totals line:\n%s", out)
	}
	// The premise: the same read with --header and no -o does print the block.
	_, table, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "name,cpu", "--header")
	if !strings.HasPrefix(table, "totals a   ") {
		t.Errorf("fixture: --header on the table should print the block first:\n%s", table)
	}
}

// A block wider than the terminal breaks between fields, each continuation
// starting with the same lead, and no line passes the width.
func TestFanOutTotalsBlockBreaksAtTheWidth(t *testing.T) {
	withRateClock(t)
	a := clusterRow("a", "a1", 10)
	a.SpendOut, a.SpendFresh, a.SpendCacheRead, a.SpendCacheWrite, a.SpendPromptTokens = tokens(1_000_000), tokens(1_000_000), tokens(1_000_000), tokens(1_000_000), tokens(1_000_000)
	const width = 60
	out := fanOutTotalsBlock([]clusterRecord{answeredRecord("a")}, []api.Session{a}, columnsOrFatal(t, "name,cpu,rss,tout,tin,cr,cw,prompt", nil), rateNow, width, nil)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("the block should break onto more than one line at width %d:\n%s", width, out)
	}
	for _, l := range lines {
		if len(l) > width || !strings.HasPrefix(l, "totals a   ") {
			t.Errorf("line %q should be at most %d wide and start with its lead", l, width)
		}
	}
	for _, want := range []string{"mCPU 100 (1)", "TOUT 1.0M (1)", "PROMPT 1.0M (1)"} {
		if !strings.Contains(strings.Join(lines, "   "), want) {
			t.Errorf("field %q should print whole:\n%s", want, out)
		}
	}
}

// The block follows the columns the fit drew, not the columns asked for: at 120
// columns the default table shows TOUT and RATE but not mCPU, so the block has
// no mCPU field; at 200 it has one.
func TestFanOutTotalsFollowTheDrawnColumns(t *testing.T) {
	a := sampled("a1", api.SessionRunning, 10, 1<<20)
	a.SpendOut = tokens(1000)
	f := startFake(t, fakeAnswers, []api.Session{a})
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock})
	for _, c := range []struct {
		width      int
		wantMCPU   bool
		wantFields string
	}{{120, false, "TOUT 1k (1)"}, {200, true, "mCPU 100 (1)"}} {
		setWidth(t, c.width)
		_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a", "--header")
		var block, table string
		for _, l := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(l, "totals"):
				block += l + "\n"
			case strings.Contains(l, "AGENT NAME"):
				table = l
			}
		}
		if table == "" || block == "" {
			t.Errorf("width %d: the block and the table should both print:\n%s", c.width, out)
			continue
		}
		if has := strings.Contains(block, "mCPU "); has != c.wantMCPU || strings.Contains(table, "mCPU") != c.wantMCPU {
			t.Errorf("width %d: mCPU in the table = %v, in the block = %v, want %v\ntable: %q\nblock: %q", c.width, strings.Contains(table, "mCPU"), has, c.wantMCPU, table, block)
		}
		if !strings.Contains(block, c.wantFields) {
			t.Errorf("width %d: the block should carry %q:\n%s", c.width, c.wantFields, block)
		}
	}
}
