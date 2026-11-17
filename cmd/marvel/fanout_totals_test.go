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
	out := fanOutTotalsBlock(records, rows, columnsOrFatal(t, cols, nil), rateNow, 0)
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
