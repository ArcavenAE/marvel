package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// fanOutTotalsBlock is the watch frame's totals block with nothing held and the
// table not cut, which is what these tests read.
func fanOutTotalsBlock(records []clusterRecord, rows []api.Session, shown []sessionColumn, now time.Time, width int, startedFolded map[string]bool) string {
	return fanTotalsBlock(records, rows, shown, now, width, startedFolded, nil, false)
}

func answeredRecord(name string) clusterRecord {
	return clusterRecord{Cluster: name, Outcome: outcomeAnswered}
}

// totalsBlockLines is the block's lines, without the final newline.
func totalsBlockLines(t *testing.T, records []clusterRecord, rows []api.Session) []string {
	t.Helper()
	withRateClock(t)
	out := fanOutTotalsBlock(records, rows, columnsOrFatal(t, "name,cpu", nil), rateNow, 0, nil)
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
	got := totalsBlockLines(t, records, rows)
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
	got := totalsBlockLines(t, records, rows)
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
	got := totalsBlockLines(t, records, rows)
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
	got := totalsBlockLines(t, records, rows)
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
	got := totalsBlockLines(t, records, rows)
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

// Through the command: --header prints the summary above the merged table, even
// into a pipe, with a TOTAL row; a plain pipe prints none.
func TestFanOutTotalsNotIntoPipe(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{sampled("a1", api.SessionRunning, 10, 1<<20), sampled("a2", api.SessionRunning, 15, 1<<20)})
	b := startFake(t, fakeAnswers, []api.Session{sampled("b1", api.SessionRunning, 40, 1<<20)})
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: a.sock},
		config.Cluster{Name: "b", Socket: b.sock},
	)

	_, plain, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "name,cpu")
	if strings.Contains(plain, "CLUSTER") || strings.Contains(plain, "TOTAL") {
		t.Errorf("a plain pipe must not carry the summary:\n%s", plain)
	}

	_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "name,cpu", "--header")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 || strings.Fields(lines[0])[0] != "CLUSTER" ||
		strings.Join(strings.Fields(lines[1]), " ") != "a 2 250" ||
		strings.Join(strings.Fields(lines[2]), " ") != "b 1 400" ||
		strings.Join(strings.Fields(lines[3]), " ") != "TOTAL 3 650" {
		t.Errorf("--header should print a row per cluster and a total above the table:\n%s", out)
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
	if strings.Contains(out, "TOTAL") || strings.Contains(out, "CLUSTER ") {
		t.Errorf("-o json must not carry the summary:\n%s", out)
	}
	// The premise: the same read with --header and no -o does print the summary.
	_, table, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "name,cpu", "--header")
	if !strings.HasPrefix(table, "CLUSTER ") {
		t.Errorf("fixture: --header on the table should print the summary first:\n%s", table)
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

// The summary follows the columns the fit drew, not the columns asked for: at
// 120 columns the default table shows TOUT and RATE but not mCPU, so the
// summary has no mCPU column; at 200 it has one.
func TestFanOutTotalsFollowTheDrawnColumns(t *testing.T) {
	a := sampled("a1", api.SessionRunning, 10, 1<<20)
	a.SpendOut = tokens(1000)
	f := startFake(t, fakeAnswers, []api.Session{a})
	g := startFake(t, fakeAnswers, []api.Session{a})
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: g.sock})
	for _, c := range []struct {
		width      int
		wantMCPU   bool
		wantFields string
		wantTotal  string
	}{{120, false, "1k", "2k"}, {200, true, "100", "200"}} {
		setWidth(t, c.width)
		_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--header")
		block, rest, found := strings.Cut(out, "\n\n")
		var table string
		for _, l := range strings.Split(rest, "\n") {
			if strings.Contains(l, "AGENT NAME") {
				table = l
			}
		}
		if !found || table == "" || !strings.HasPrefix(block, "CLUSTER") {
			t.Errorf("width %d: the summary and the table should both print:\n%s", c.width, out)
			continue
		}
		lines := strings.Split(block, "\n")
		if has := strings.Contains(lines[0], "mCPU"); has != c.wantMCPU || strings.Contains(table, "mCPU") != c.wantMCPU {
			t.Errorf("width %d: mCPU in the table = %v, in the summary = %v, want %v\ntable: %q\nsummary: %q", c.width, strings.Contains(table, "mCPU"), has, c.wantMCPU, table, lines[0])
		}
		if len(lines) != 4 {
			t.Errorf("width %d: want a header, a row per cluster and a total, got %d lines:\n%s", c.width, len(lines), block)
		}
		for i, l := range lines[1:] {
			want := c.wantFields
			if i == len(lines)-2 {
				want = c.wantTotal // the TOTAL row adds the two clusters
			}
			if !strings.Contains(l, want) {
				t.Errorf("width %d: line %d should carry %q:\n%s", c.width, i+1, want, l)
			}
		}
	}
}

// foldSameDaemon returns the names of the members that were starting when it
// overwrote their outcome, and none for a member that was not.
func TestFoldSameDaemonReturnsTheStartingMembers(t *testing.T) {
	stamp := daemonStamp{PID: 4242, StartedAt: time.Unix(1_700_000_000, 0)}
	fold := func(memberOutcome outcome) map[string]bool {
		records := []clusterRecord{{Cluster: "a", Outcome: outcomeAnswered}, {Cluster: "b", Outcome: memberOutcome}}
		rowSets := [][]api.Session{{{Workspace: "w", Name: "s1"}}, {{Workspace: "w", Name: "s1"}}}
		return foldSameDaemon([]string{"d", "d"}, []daemonStamp{stamp, stamp}, []bool{true, true}, records, rowSets)
	}
	if got := fold(outcomeStarting); len(got) != 1 || !got["b"] {
		t.Errorf("a starting member should be named: %v", got)
	}
	if got := fold(outcomeAnswered); len(got) != 0 {
		t.Errorf("a member that was not starting should not be named: %v", got)
	}
}

// A second entry at the same address shares the first dial's fate, so when that
// dial was starting the entry is folded as a starting member.
func TestCollectFanOutNamesAStartingRepeatAddress(t *testing.T) {
	f := startFake(t, fakeStarting, []api.Session{row("w", "s1")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: f.sock})
	records, _, started, err := collectFanOutFolded([]string{"a", "b"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if records[1].Outcome != outcomeSameDaemon || !started["b"] {
		t.Errorf("b = %+v, started = %v, want a same-daemon repeat named as starting", records[1], started)
	}
}

// Two entries that reach one daemon by different paths are merged by the live
// fold, and a member that was starting is named through the whole read.
func TestCollectFanOutNamesAStartingMergedMember(t *testing.T) {
	f := startSD(t, sdStart, []api.Session{row("w", "s1")})
	f.starting = true
	link := filepath.Join(filepath.Dir(f.sock), "alias.sock")
	if err := os.Symlink(f.sock, link); err != nil {
		t.Fatal(err)
	}
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: link})
	records, _, started, err := collectFanOutFolded([]string{"a", "b"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if records[1].Outcome != outcomeSameDaemon || !started["b"] {
		t.Errorf("b = %+v, started = %v, want a merged member named as starting", records[1], started)
	}
}

// A starting folded member marks only the leader it was folded into, not every
// line in the block.
func TestFanOutTotalsStartingMemberMarksOnlyItsOwnLeader(t *testing.T) {
	n := 1
	records := []clusterRecord{
		answeredRecord("a"),
		{Cluster: "b", Outcome: outcomeSameDaemon, SameDaemonAs: "a", Sessions: &n},
		answeredRecord("c"),
		{Cluster: "d", Outcome: outcomeSameDaemon, SameDaemonAs: "c", Sessions: &n},
	}
	rows := []api.Session{
		clusterRow("a", "a1", 10), clusterRow("b", "b-only", 5),
		clusterRow("c", "c1", 20), clusterRow("d", "d-only", 5),
	}
	withRateClock(t)
	out := fanOutTotalsBlock(records, rows, columnsOrFatal(t, "name,cpu", nil), rateNow, 0, map[string]bool{"b": true})
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != 2 {
		t.Fatalf("want one line per leader, got %d:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.HasPrefix(got[0], "totals a (+ b, same daemon) (starting)   2 sessions") {
		t.Errorf("a's line should carry the mark from its starting member b:\n%s", got[0])
	}
	if strings.Contains(got[1], "(starting)") {
		t.Errorf("c's line must not carry a mark from a member of another leader:\n%s", got[1])
	}
}
