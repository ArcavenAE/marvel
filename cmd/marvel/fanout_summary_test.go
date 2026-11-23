package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The one-shot `get sessions --all-clusters` screen: a summary table with one
// aligned row per cluster and a total row, each caveat said once in words, and
// the status lines kept off the top of the screen. Cluster names are fixtures.

func summaryScreen(t *testing.T, records []clusterRecord, rows []api.Session, header bool) (stdout, stderr string) {
	t.Helper()
	withRateClock(t)
	cols := columnsOrFatal(t, "name,cpu,rss,tout,rate", nil)
	return renderFanOutScreen(records, rows, cols, false, false, 200, header, rateNow, nil)
}

// summaryParts splits stdout into the summary table, the notes under it and the
// sessions table, which are three blocks divided by a blank line.
func summaryParts(t *testing.T, stdout string) (table, notes []string, rest string) {
	t.Helper()
	blocks := strings.SplitN(stdout, "\n\n", 3)
	if len(blocks) != 3 {
		t.Fatalf("want three blocks (summary, notes, sessions), got %d:\n%s", len(blocks), stdout)
	}
	return strings.Split(blocks[0], "\n"), strings.Split(blocks[1], "\n"), blocks[2]
}

func summaryRows(partialOn string) (records []clusterRecord, rows []api.Session) {
	records = []clusterRecord{answeredRecord("alpha"), answeredRecord("beta")}
	a1, a2, b1 := clusterRow("alpha", "a1", 10), clusterRow("alpha", "a2", 15), clusterRow("beta", "b1", 40)
	a1.SpendOut, a2.SpendOut, b1.SpendOut = tokens(1_000_000), tokens(500_000), tokens(250_000)
	b1.SpendPartial = partialOn == "beta"
	return records, []api.Session{a1, a2, b1}
}

func TestFanOutSummaryIsOneAlignedRowPerClusterPlusTotal(t *testing.T) {
	records, rows := summaryRows("")
	out, _ := summaryScreen(t, records, rows, true)
	table, _, rest := summaryParts(t, out)
	if len(table) != 4 {
		t.Fatalf("want a header, two cluster rows and a total row, got %d lines:\n%s", len(table), strings.Join(table, "\n"))
	}
	for i, want := range []string{"CLUSTER", "alpha", "beta", "TOTAL"} {
		if !strings.HasPrefix(table[i], want) {
			t.Errorf("line %d = %q, want it to start with %q", i, table[i], want)
		}
	}
	for _, l := range table {
		if len(l) != len(table[0]) {
			t.Errorf("columns do not line up: %q is %d wide, the header is %d", l, len(l), len(table[0]))
		}
	}
	if !strings.Contains(table[0], "SESSIONS") {
		t.Errorf("no plain SESSIONS header: %q", table[0])
	}
	total := table[3]
	if !strings.Contains(total, "3") || !strings.Contains(total, "650") {
		t.Errorf("the total row does not carry 3 sessions and 650 mCPU: %q", total)
	}
	if !strings.Contains(rest, "alpha:ws/a1") || !strings.Contains(rest, "beta:ws/b1") {
		t.Errorf("the session rows are not printed after the summary:\n%s", rest)
	}
}

func TestFanOutSummaryDropsTheOldFreeFormTotalsLine(t *testing.T) {
	records, rows := summaryRows("beta")
	out, _ := summaryScreen(t, records, rows, true)
	for _, old := range []string{"totals alpha", "totals beta", " fresh)", " partial)"} {
		if strings.Contains(out, old) {
			t.Errorf("the old totals line text %q is still printed:\n%s", old, out)
		}
	}
}

func TestFanOutSummaryExplainsEachColumnOnce(t *testing.T) {
	records, rows := summaryRows("")
	out, _ := summaryScreen(t, records, rows, true)
	_, notes, _ := summaryParts(t, out)
	for _, col := range []string{"mCPU", "RSS", "TOUT", "RATE"} {
		n := 0
		for _, l := range notes {
			if strings.HasPrefix(l, col+": ") {
				n++
				if len(l) < len(col)+20 {
					t.Errorf("the %s legend is not a sentence: %q", col, l)
				}
			}
		}
		if n != 1 {
			t.Errorf("want one legend line for %s, got %d:\n%s", col, n, strings.Join(notes, "\n"))
		}
	}
}

func TestFanOutSummaryNamesTheClustersWhoseTotalIsALowerBound(t *testing.T) {
	records, rows := summaryRows("beta")
	out, _ := summaryScreen(t, records, rows, true)
	_, notes, _ := summaryParts(t, out)
	text := strings.Join(notes, "\n")
	if !strings.Contains(text, "lower bound") || !strings.Contains(text, "beta") {
		t.Errorf("no note that names beta's TOUT as a lower bound:\n%s", text)
	}
	records, rows = summaryRows("")
	out, _ = summaryScreen(t, records, rows, true)
	_, notes, _ = summaryParts(t, out)
	if strings.Contains(strings.Join(notes, "\n"), "lower bound") {
		t.Errorf("a lower-bound note with no lower bound:\n%s", strings.Join(notes, "\n"))
	}
}

func TestFanOutSummaryFoldsASameDaemonMemberIntoItsLeader(t *testing.T) {
	records, rows := summaryRows("")
	records[1] = clusterRecord{Cluster: "beta", Outcome: outcomeSameDaemon, SameDaemonAs: "alpha"}
	out, _ := summaryScreen(t, records, rows, true)
	table, notes, _ := summaryParts(t, out)
	for _, l := range table {
		if strings.HasPrefix(l, "beta") {
			t.Errorf("a same-daemon member has a row of its own: %q", l)
		}
	}
	if text := strings.Join(notes, "\n"); !strings.Contains(text, "beta") || !strings.Contains(text, "same daemon") {
		t.Errorf("no note that beta is the same daemon as alpha:\n%s", text)
	}
}

func TestFanOutSummaryNamesAClusterThatWasStarting(t *testing.T) {
	records, rows := summaryRows("")
	records[0] = clusterRecord{Cluster: "alpha", Outcome: outcomeStarting}
	out, _ := summaryScreen(t, records, rows, true)
	_, notes, _ := summaryParts(t, out)
	if text := strings.Join(notes, "\n"); !strings.Contains(text, "alpha") || !strings.Contains(text, "starting") {
		t.Errorf("no note that alpha was starting, so its figures may be low:\n%s", text)
	}
}

// With the summary on screen the answered clusters need no status line, since
// the summary has a row for each. A cluster that did not answer keeps its line,
// because the cause and the next step are only there. Off a terminal, with no
// summary, every line stays.
func TestFanOutStatusLinesDoNotCrowdTheSummary(t *testing.T) {
	records, rows := summaryRows("")
	records = append(records, clusterRecord{Cluster: "gamma", Outcome: outcomeUnreachable, Cause: "dial refused"})
	out, errOut := summaryScreen(t, records, rows, true)
	if !strings.HasPrefix(out, "CLUSTER") {
		t.Errorf("the summary is not the first thing on stdout:\n%s", out)
	}
	if strings.Contains(errOut, "cluster alpha") || strings.Contains(errOut, "cluster beta") {
		t.Errorf("an answered cluster still has a status line beside the summary:\n%s", errOut)
	}
	if !strings.Contains(errOut, "cluster gamma: unreachable") {
		t.Errorf("the failed cluster lost its status line:\n%s", errOut)
	}
	out, errOut = summaryScreen(t, records, rows, false)
	if strings.HasPrefix(out, "CLUSTER") {
		t.Errorf("a pipe got the summary:\n%s", out)
	}
	for _, c := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(errOut, "cluster "+c+":") {
			t.Errorf("with no summary, %s lost its status line:\n%s", c, errOut)
		}
	}
}

// A column can cover fewer sessions than its row: a session with no value adds
// nothing. The notes say so in words, so a partial sum is never silent, and say
// nothing when every session reports.
func TestFanOutSummarySaysWhenAColumnCoversFewerSessionsThanTheRow(t *testing.T) {
	records, rows := summaryRows("")
	rows[1].SpendOut = nil // alpha's second session reports no TOUT
	out, _ := summaryScreen(t, records, rows, true)
	_, notes, _ := summaryParts(t, out)
	text := strings.Join(notes, "\n")
	if !strings.Contains(text, "TOUT in alpha 1 of 2") || !strings.Contains(text, "fewer sessions than its row") {
		t.Errorf("no note that TOUT covers 1 of alpha's 2 sessions:\n%s", text)
	}
	if strings.Contains(text, "TOUT in beta") || strings.Contains(text, "mCPU in") {
		t.Errorf("a note for a column that covers every session:\n%s", text)
	}
	records, rows = summaryRows("")
	out, _ = summaryScreen(t, records, rows, true)
	_, notes, _ = summaryParts(t, out)
	if strings.Contains(strings.Join(notes, "\n"), "fewer sessions") {
		t.Errorf("a coverage note where every session reports:\n%s", strings.Join(notes, "\n"))
	}
}
