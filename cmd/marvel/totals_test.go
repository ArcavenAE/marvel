package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The totals line of docs/design/cluster-rollup-header.md, section 2. These
// tests read the line's fields, which the design separates by three spaces:
// "totals   37 sessions   mCPU 14600 (35)   RSS 24.1G (35)   ...".

func totalsLine(t *testing.T, rows []api.Session, cols string) string {
	t.Helper()
	withRateClock(t)
	return strings.TrimRight(renderTotals(rows, columnsOrFatal(t, cols, nil), rateNow, totalsOpts{}), "\n")
}

// totalsFields splits a totals line into its fields, without the leading label.
func totalsFields(line string) []string {
	parts := strings.Split(line, "   ")
	if len(parts) > 0 && strings.HasPrefix(parts[0], "totals") {
		parts = parts[1:]
	}
	return parts
}

// totalsField returns the field that starts with the column label, or "".
func totalsField(line, label string) string {
	for _, f := range totalsFields(line) {
		if strings.HasPrefix(f, label+" ") {
			return f
		}
	}
	return ""
}

func needField(t *testing.T, line, label, want string) {
	t.Helper()
	if got := totalsField(line, label); got != want {
		t.Errorf("field %s = %q, want %q\nline: %q", label, got, want, line)
	}
}

func sampled(name string, state api.SessionState, cpuPercent float64, rss int64) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: state, PaneID: "%3"}
	s.CPUPercent, s.RSSBytes, s.MetricsAt = cpuPercent, rss, rateNow
	return s
}

func tokens(n int) *int { return &n }

func plain(name string) api.Session {
	return api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
}

// A total appears only for a column that is shown. A column the fit dropped
// has no field, and naming it with --columns brings it back.
func TestTotalsOnlyForShownColumns(t *testing.T) {
	a := sampled("a", api.SessionRunning, 12.5, 1<<20)
	a.SpendOut = tokens(1_000_000)
	b := sampled("b", api.SessionRunning, 30.2, 2<<20)
	b.SpendOut = tokens(500_000)
	rows := []api.Session{a, b}

	narrow := totalsLine(t, rows, "name,cpu")
	if !strings.Contains(narrow, "2 sessions") {
		t.Errorf("the line should always carry the session count:\n%q", narrow)
	}
	for _, hidden := range []string{"RSS", "TOUT", "RATE", "COST"} {
		if strings.Contains(narrow, hidden) {
			t.Errorf("column %s is not shown but has a field:\n%q", hidden, narrow)
		}
	}
	if totalsField(narrow, "mCPU") == "" {
		t.Errorf("mCPU is shown but has no field:\n%q", narrow)
	}
	named := totalsLine(t, rows, "name,cpu,tout")
	if totalsField(named, "TOUT") == "" {
		t.Errorf("--columns tout should bring the TOUT field back:\n%q", named)
	}
}

// mCPU is the integer sum of the cells that print: 12.5% is 125, 30.2% is 302.
func TestTotalsMillicpuIntegerSum(t *testing.T) {
	rows := []api.Session{
		sampled("a", api.SessionRunning, 12.5, 1<<20),
		sampled("b", api.SessionRunning, 30.2, 2<<20),
	}
	line := totalsLine(t, rows, "name,cpu,rss")
	needField(t, line, "mCPU", "mCPU 427 (2)")
	needField(t, line, "RSS", "RSS 3.0M (2)")
}

// A crashed row keeps a frozen reading that its cell hides, and a row never
// sampled has none. Neither adds to the sum or to the count.
func TestTotalsGoneRowsAddNothing(t *testing.T) {
	rows := []api.Session{
		sampled("a", api.SessionRunning, 12.5, 1<<20),
		sampled("b", api.SessionCrashed, 99.0, 500<<20),
		sampled("c", api.SessionFailed, 99.0, 500<<20),
		sampled("d", api.SessionSucceeded, 99.0, 500<<20),
		plain("e"),
	}
	// The premise: the cells of those rows are dashes.
	cells := renderSessionTableCols(rows, columnsOrFatal(t, "name,cpu,rss", nil))
	if strings.Count(cells, " -") < 8 {
		t.Fatalf("fixture: the gone and unsampled rows should print dashes:\n%s", cells)
	}
	line := totalsLine(t, rows, "name,cpu,rss")
	if !strings.Contains(line, "5 sessions") {
		t.Errorf("sessions should count every row:\n%q", line)
	}
	needField(t, line, "mCPU", "mCPU 125 (1)")
	needField(t, line, "RSS", "RSS 1.0M (1)")
}

// A token total that summed a + cell carries + and names the partial rows.
func TestTotalsPartialCarriesPlusAndCount(t *testing.T) {
	a, b, c := plain("a"), plain("b"), plain("c")
	a.SpendOut = tokens(1_000_000)
	b.SpendOut, b.SpendPartial = tokens(500_000), true
	rows := []api.Session{a, b, c}
	line := totalsLine(t, rows, "name,tout")
	needField(t, line, "TOUT", "TOUT 1.5M+ (2, 1 partial)")
	clean := totalsLine(t, []api.Session{a, c}, "name,tout")
	needField(t, clean, "TOUT", "TOUT 1.0M (1)")
}

// TIN, CR, CW and PROMPT use the TOUT cell and its + rule.
func TestTotalsTokenFieldsFollowToutRule(t *testing.T) {
	a, b := plain("a"), plain("b")
	for _, s := range []*api.Session{&a, &b} {
		n := 1_000_000
		if s == &b {
			n = 500_000
			s.SpendPartial = true
		}
		s.SpendFresh, s.SpendCacheRead, s.SpendCacheWrite, s.SpendPromptTokens = tokens(n), tokens(n), tokens(n), tokens(n)
	}
	line := totalsLine(t, []api.Session{a, b}, "name,tin,cr,cw,prompt")
	for _, f := range []string{"TIN", "CR", "CW", "PROMPT"} {
		needField(t, line, f, f+" 1.5M+ (2, 1 partial)")
	}
}

// HIT% is a ratio and is never summed, even when it is shown.
func TestTotalsHitNeverSummed(t *testing.T) {
	a := plain("a")
	a.SpendOut, a.SpendCacheRead, a.SpendPromptTokens = tokens(10), tokens(50), tokens(100)
	line := totalsLine(t, []api.Session{a, a}, "name,hit,tout")
	if !strings.Contains(line, "2 sessions") || totalsField(line, "TOUT") == "" {
		t.Errorf("the line should exist and carry TOUT:\n%q", line)
	}
	if strings.Contains(line, "HIT") {
		t.Errorf("HIT%% is a ratio and has no total:\n%q", line)
	}
}

// RATE sums fresh cells only. A ? (expired) and a - (never sampled) add
// nothing and are not counted.
func TestTotalsRateFreshOnly(t *testing.T) {
	rows := []api.Session{
		rateSession("a", 10, 0, 0),
		rateSession("b", 5.5, 0, 0),
		rateSession("c", 40, 11*time.Minute, 5*time.Second),
		plain("d"),
	}
	cells := rateCells(t, rows...)
	if cells["c"] != "?" || cells["d"] != "-" || cells["a"] != "10.0/s" {
		t.Fatalf("fixture: cells = %v, want a 10.0/s, c ?, d -", cells)
	}
	line := totalsLine(t, rows, "name,rate")
	needField(t, line, "RATE", "RATE 15.5/s (2 fresh)")
}

// With the clock the cells read pinned, the fresh count equals the number of
// RATE cells that show a value, across readings on both sides of the window.
func TestTotalsRateClockSeam(t *testing.T) {
	window := api.DefaultQuietWindow
	var rows []api.Session
	for i, age := range []time.Duration{0, time.Minute, window - time.Second, window, window + time.Second, 2 * window} {
		rows = append(rows, rateSession(string(rune('a'+i)), 8, age, 5*time.Second))
	}
	withRateClock(t)
	shown := 0
	for _, s := range rows {
		if c := rateCell(s, rateNow); c != "?" && c != "-" {
			shown++
		}
	}
	if shown == 0 || shown == len(rows) {
		t.Fatalf("fixture: %d of %d cells show a value, want some and not all", shown, len(rows))
	}
	line := totalsLine(t, rows, "name,rate")
	want := rateFreshCount(shown)
	if f := totalsField(line, "RATE"); !strings.HasSuffix(f, want) {
		t.Errorf("RATE field = %q, want its count to end %q", f, want)
	}
}

// rateFreshCount renders the count text of a RATE field.
func rateFreshCount(n int) string { return "(" + strconv.Itoa(n) + " fresh)" }

// A row with no cost total is left out and not counted. When no row has one
// the field is a dash, never ~$0.00.
func TestTotalsCostNilLeftOut(t *testing.T) {
	rows := []api.Session{
		costSession("a", usd(4.12), time.Second),
		costSession("b", nil, time.Second),
	}
	old := rateClock
	rateClock = func() time.Time { return costNow }
	t.Cleanup(func() { rateClock = old })
	line := strings.TrimRight(renderTotals(rows, columnsOrFatal(t, "name,cost", nil), costNow, totalsOpts{}), "\n")
	needField(t, line, "COST", "COST ~$4.12 (1)")
	none := strings.TrimRight(renderTotals(rows[1:], columnsOrFatal(t, "name,cost", nil), costNow, totalsOpts{}), "\n")
	needField(t, none, "COST", "COST -")
	if strings.Contains(none, "$0.00") {
		t.Errorf("no row has a total, so the field must not print a zero:\n%q", none)
	}
}

// TOUT and COST are separate fields: neither changes when the other's input
// does, and no total converts one to the other.
func TestTotalsToutAndCostSeparate(t *testing.T) {
	mk := func(out int, cost float64) string {
		a := costSession("a", usd(cost), time.Second)
		a.SpendOut = tokens(out)
		old := rateClock
		rateClock = func() time.Time { return costNow }
		defer func() { rateClock = old }()
		return strings.TrimRight(renderTotals([]api.Session{a}, columnsOrFatal(t, "name,tout,cost", nil), costNow, totalsOpts{}), "\n")
	}
	base, moreCost, moreTokens := mk(1_000_000, 4.00), mk(1_000_000, 9.00), mk(3_000_000, 4.00)
	needField(t, base, "TOUT", "TOUT 1.0M (1)")
	needField(t, base, "COST", "COST ~$4.00 (1)")
	if totalsField(moreCost, "TOUT") != totalsField(base, "TOUT") || totalsField(moreCost, "TOUT") == "" {
		t.Errorf("TOUT moved when only the cost changed:\n%q\n%q", base, moreCost)
	}
	if totalsField(moreTokens, "COST") != totalsField(base, "COST") || totalsField(moreTokens, "COST") == "" {
		t.Errorf("COST moved when only the tokens changed:\n%q\n%q", base, moreTokens)
	}
}

// With no value in any row, each shown field is a dash and never a zero.
func TestTotalsDashWhenNoValue(t *testing.T) {
	line := totalsLine(t, []api.Session{plain("a"), plain("b")}, "name,cpu,rss,tout,rate,cost")
	for _, f := range []string{"mCPU", "RSS", "TOUT", "RATE", "COST"} {
		needField(t, line, f, f+" -")
	}
}

// The line is header chrome: it prints under the daemon header on a terminal
// or with --header, and a plain table into a pipe is byte for byte as before.
func TestTotalsNotIntoPipe(t *testing.T) {
	withRateClock(t)
	a := sampled("a", api.SessionRunning, 12.5, 1<<20)
	rows := []api.Session{a, sampled("b", api.SessionRunning, 30.2, 2<<20)}
	cols := columnsOrFatal(t, "name,cpu", nil)

	withHeader := renderSessionsOneShot(rows, cols, "cluster  alpha\n\n", fitOptions{})
	if !strings.HasPrefix(withHeader, "cluster  alpha\ntotals   2 sessions") {
		t.Errorf("with a header the totals line should sit directly under the header line:\n%s", withHeader)
	}
	pre, table, post := renderSessionsParts(rows, cols, "", fitOptions{})
	if got := renderSessionsOneShot(rows, cols, "", fitOptions{}); got != pre+table+post || strings.Contains(got, "totals") {
		t.Errorf("with no header the output must be the plain table:\n%s", got)
	}
}

// A line wider than the terminal breaks between fields onto a second totals
// line, so the table keeps its own width budget.
func TestTotalsBreaksBetweenFields(t *testing.T) {
	withRateClock(t)
	a := sampled("a", api.SessionRunning, 12.5, 1<<20)
	a.SpendOut, a.SpendFresh, a.SpendCacheRead, a.SpendCacheWrite, a.SpendPromptTokens = tokens(1_000_000), tokens(1_000_000), tokens(1_000_000), tokens(1_000_000), tokens(1_000_000)
	b := a
	b.Name, b.SpendPartial = "b", true
	rows := []api.Session{a, b}
	cols := columnsOrFatal(t, "name,cpu,rss,tout,tin,cr,cw,prompt", nil)
	const width = 100
	out := renderSessionsOneShot(rows, cols, "cluster  alpha\n\n", fitOptions{width: width, explicit: true})
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "totals") {
			lines = append(lines, l)
			if len(l) > width {
				t.Errorf("a totals line is %d wide, past the terminal's %d:\n%q", len(l), width, l)
			}
		}
	}
	if len(lines) < 2 {
		t.Errorf("the line should break onto a second totals line at width %d, got %d:\n%s", width, len(lines), out)
	}
	joined := strings.Join(lines, "   ")
	for _, want := range []string{"mCPU 250 (2)", "TOUT 2.0M+ (2, 1 partial)", "PROMPT 2.0M+ (2, 1 partial)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("field %q should print whole, never cut:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

// A RATE total at 1000/s or more prints as the cell does, %.0f/s, so 2100/s
// and not 2.1k/s (design section 2.7).
func TestTotalsRateAtAThousand(t *testing.T) {
	rows := []api.Session{rateSession("a", 1500, 0, 0), rateSession("b", 600, 0, 0)}
	cells := rateCells(t, rows...)
	if cells["a"] != "1500/s" {
		t.Fatalf("fixture: the cell = %q, want 1500/s", cells["a"])
	}
	needField(t, totalsLine(t, rows, "name,rate"), "RATE", "RATE 2100/s (2 fresh)")
}

// A COST total at $1000 or more prints as the cell does, with no thousands
// separator: ~$4893.10 (design section 2.7).
func TestTotalsCostAtAThousand(t *testing.T) {
	rows := []api.Session{costSession("a", usd(4000), time.Second), costSession("b", usd(893.10), time.Second)}
	old := rateClock
	rateClock = func() time.Time { return costNow }
	t.Cleanup(func() { rateClock = old })
	line := strings.TrimRight(renderTotals(rows, columnsOrFatal(t, "name,cost", nil), costNow, totalsOpts{}), "\n")
	needField(t, line, "COST", "COST ~$4893.10 (2)")
}

// The session count agrees in number: 0 sessions, 1 session, 2 sessions.
func TestTotalsSessionCountAgreesInNumber(t *testing.T) {
	for _, c := range []struct {
		rows []api.Session
		want string
	}{
		{nil, "0 sessions"},
		{[]api.Session{plain("a")}, "1 session"},
		{[]api.Session{plain("a"), plain("b")}, "2 sessions"},
	} {
		line := totalsLine(t, c.rows, "name,cpu")
		if f := totalsFields(line); len(f) == 0 || f[0] != c.want {
			t.Errorf("%d rows: first field = %v, want %q\nline: %q", len(c.rows), f, c.want, line)
		}
	}
}

// Through the command: --header prints the line even into a pipe, a plain
// pipe does not, and -o json is byte for byte the same with or without
// --header, with no totals in it (design section 2.7).
func TestTotalsThroughTheGetCommand(t *testing.T) {
	withHeader := getSessionsAtWidth(t, 0, "--header", "--columns", "name,cpu")
	if !strings.Contains(withHeader, "\ntotals   1 session   mCPU -\n") {
		t.Errorf("--header should print the totals line under the header:\n%s", withHeader)
	}
	if piped := getSessionsAtWidth(t, 0, "--columns", "name,cpu"); strings.Contains(piped, "totals") {
		t.Errorf("a plain pipe must not carry the line:\n%s", piped)
	}
	plainJSON := getSessionsAtWidth(t, 0, "-o", "json")
	headerJSON := getSessionsAtWidth(t, 0, "--header", "-o", "json")
	if plainJSON == "" || plainJSON != headerJSON {
		t.Errorf("-o json must be byte-identical with --header:\nwithout: %q\nwith:    %q", plainJSON, headerJSON)
	}
	if strings.Contains(headerJSON, "totals   ") {
		t.Errorf("-o json must not carry the line:\n%s", headerJSON)
	}
}

// The -w frame carries the totals line directly under the daemon header, as
// the one-shot output does (design section 2.4, aae-orc-g5gil).
func TestTotalsInTheWatchFrame(t *testing.T) {
	parityDaemon(t, newCluster())
	setWidth(t, 200)
	cols, explicit, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	frame := renderWatch(newWatchScreen(cols, explicit, false))
	if !strings.Contains(frame, "mrvl://") || !strings.Contains(frame, "workspace") && !strings.Contains(frame, "WORKSPACE") {
		t.Fatalf("fixture: the frame should carry the header and the table:\n%s", frame)
	}
	if !strings.Contains(frame, "\ntotals   3 sessions") {
		t.Errorf("the watch frame should carry the totals line under the header:\n%s", frame)
	}
}

// The one-shot line follows the columns the fit drew, not the columns asked
// for: at 120 columns the wide set loses mCPU and RSS, so the table has no such
// column and the line has no such field (design section 2.1).
func TestTotalsFollowTheDrawnColumnsThroughTheFit(t *testing.T) {
	withRateClock(t)
	a := sampled("a", api.SessionRunning, 12.5, 1<<20)
	a.SpendOut = tokens(1000)
	cols := columnsOrFatal(t, "wide", nil)
	out := renderSessionsOneShot([]api.Session{a}, cols, "cluster  alpha\n\n", fitOptions{width: 120})

	var table, totals string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "totals"):
			totals += l + "\n"
		case strings.Contains(l, "AGENT NAME"):
			table = l
		}
	}
	if table == "" {
		t.Fatalf("fixture: the table header is missing:\n%s", out)
	}
	dropped := 0
	for _, c := range cols {
		if c.name != "cpu" && c.name != "rss" && c.name != "tout" && c.name != "rate" {
			continue
		}
		drawn := strings.Contains(table, c.header)
		if !drawn {
			dropped++
		}
		if has := strings.Contains(totals, c.header+" "); has != drawn {
			t.Errorf("column %s: drawn in the table = %v, field in the totals = %v\ntable: %q\ntotals:\n%s", c.header, drawn, has, table, totals)
		}
	}
	if dropped == 0 {
		t.Fatalf("fixture: at width 120 the fit should drop mCPU and RSS from the wide set:\n%s", out)
	}
}
