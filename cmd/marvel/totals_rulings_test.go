package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/config"
)

// Tests for the ruled shape of the totals line: tokens and dollars apart, a
// plain CPU sum, the fleet line and its two preferences, and the fan-out,
// watch and team-load cases. The fixtures read the clock the cells read.

func withCostClock(t *testing.T) {
	t.Helper()
	old := rateClock
	rateClock = func() time.Time { return costNow }
	t.Cleanup(func() { rateClock = old })
}

// tokRate is a token-metered row with an output rate of 40 a second when
// observed, as rateSession makes one, but against the cost clock.
func tokRate(name string, age, ctxAge time.Duration) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.OutRate = asof.Cell[float64]{
		Value:      40,
		ObservedAt: costNow.Add(-age),
		ValidUntil: costNow.Add(-age).Add(api.DefaultQuietWindow),
	}
	s.ContextAt = costNow.Add(-ctxAge)
	return s
}

func risingCost(name string) api.Session {
	return costSession(name, usd(4.12), 15*time.Second, at(12*time.Minute, 3.0), at(5*time.Minute, 3.6), at(time.Minute, 4.12))
}

func costLine(t *testing.T, rows []api.Session) string {
	t.Helper()
	withCostClock(t)
	return renderTotalsLine(rows, rateClock(), "totals", 0)
}

// kindCounts reads the token and dollar counts out of a field's text.
func kindCounts(text string) (tok, usd int) {
	for _, seg := range strings.Split(text, "; ") {
		c := totalsCounts(seg)
		if len(c) != 1 {
			continue
		}
		var n int
		_, _ = fmt.Sscanf(c[0], "%d", &n)
		if strings.Contains(seg, "~$") {
			usd = n
		} else {
			tok = n
		}
	}
	return tok, usd
}

// Token rows sum to a token figure and statusline rows to a dollar figure,
// each with its own count, and nothing adds the two.
func TestTotalsTokensAndDollarsNeverSum(t *testing.T) {
	tok1 := withTout(tokRate("t1", 30*time.Second, 30*time.Second), 1000)
	tok2 := withTout(tokRate("t2", 30*time.Second, 30*time.Second), 2000)
	line := costLine(t, []api.Session{tok1, tok2, risingCost("s1")})

	tout := totalsFieldText(line, "tout")
	if !strings.Contains(tout, formatTokenCount(3000)) || !strings.Contains(tout, "~$4.12") {
		t.Errorf("tout = %q, want %s and ~$4.12", tout, formatTokenCount(3000))
	}
	if got := totalsCounts(tout); len(got) != 2 || got[0] != "2" || got[1] != "1" {
		t.Errorf("tout counts = %v, want [2 1]", got)
	}
	if strings.Contains(line, "3004") {
		t.Errorf("tokens and dollars were added: %q", line)
	}

	v1, _ := sessionRate(tok1, costNow)
	v2, _ := sessionRate(tok2, costNow)
	rate := totalsFieldText(line, "rate")
	if want := formatTokenRate(v1+v2) + " tok/s"; !strings.Contains(rate, want) {
		t.Errorf("rate = %q, want the token sum %q", rate, want)
	}
	if !strings.Contains(rate, "~$0.11/m") {
		t.Errorf("rate = %q, want the statusline row's ~$0.11/m", rate)
	}
	if got := totalsCounts(rate); len(got) != 2 || got[0] != "2" || got[1] != "1" {
		t.Errorf("rate counts = %v, want [2 1]", got)
	}
}

// A field with one kind of row prints only that kind.
func TestTotalsOneKindPrintsOnlyThatKind(t *testing.T) {
	tokens := costLine(t, []api.Session{withTout(tokRate("t", 30*time.Second, 30*time.Second), 1000)})
	if strings.Contains(tokens, "~$") {
		t.Errorf("token rows only, but the line has a dollar figure: %q", tokens)
	}
	dollars := costLine(t, []api.Session{risingCost("s")})
	for _, f := range []string{"tout", "rate"} {
		if text := totalsFieldText(dollars, f); strings.Contains(text, "tok") {
			t.Errorf("statusline rows only, but %s has a token figure: %q", f, text)
		}
	}
}

// Every dollar figure carries the estimate mark.
func TestTotalsDollarFigureCarriesTilde(t *testing.T) {
	line := costLine(t, []api.Session{risingCost("a"), risingCost("b")})
	if n := strings.Count(line, "$"); n == 0 || n != strings.Count(line, "~$") {
		t.Errorf("%d dollar signs but %d ~$ in %q", n, strings.Count(line, "~$"), line)
	}
}

// The CPU figure is the plain sum of the cells, not a host share.
func TestTotalsCPUIsThePlainSum(t *testing.T) {
	const gib = int64(1) << 30
	base := api.Session{Name: "a", Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning}
	rows := []api.Session{withMetrics(base, 10, gib), withMetrics(base, 140.5, gib), inState(base, api.SessionRunning)}
	line := renderTotals(t, rows)
	if got := totalsFieldText(line, "cpu"); !strings.HasPrefix(got, "150.5%") || got != "150.5% (2)" {
		t.Errorf("cpu = %q, want the sum 150.5%% over 2 rows", got)
	}
}

// For each field and each kind, the count in the line is the number of rows
// whose rendered cell is a value of that kind.
func TestTotalsAgreeWithRenderedCellsByKind(t *testing.T) {
	const gib = int64(1) << 30
	rows := []api.Session{
		withTout(withMetrics(tokRate("a", 30*time.Second, 30*time.Second), 10, gib), 1000),
		withTout(tokRate("b", 11*time.Minute, time.Minute), 2000), // stale rate
		risingCost("c"),
		costSession("silent", usd(4.12), 11*time.Minute, at(30*time.Minute, 3)),
		costSession("reset", usd(0.9), 15*time.Second, at(3*time.Minute, 0.5), at(time.Minute, 0.9)),
		{Name: "bare", Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning},
	}
	withCostClock(t)
	var toutTok, toutUSD, rateTok, rateUSD int
	for _, r := range rows {
		c := newSessionRow(r)
		switch {
		case strings.HasPrefix(c.tout, "~$"):
			toutUSD++
		case c.tout != "-":
			toutTok++
		}
		switch {
		case strings.HasPrefix(c.rate, "~$"):
			rateUSD++
		case c.rate != "-" && c.rate != "?":
			rateTok++
		}
	}
	line := renderTotalsLine(rows, rateClock(), "totals", 0)
	if tok, usd := kindCounts(totalsFieldText(line, "tout")); tok != toutTok || usd != toutUSD {
		t.Errorf("tout counts tokens %d dollars %d, cells say %d and %d (line %q)", tok, usd, toutTok, toutUSD, line)
	}
	if tok, usd := kindCounts(totalsFieldText(line, "rate")); tok != rateTok || usd != rateUSD {
		t.Errorf("rate counts tokens %d dollars %d, cells say %d and %d (line %q)", tok, usd, rateTok, rateUSD, line)
	}
	if toutTok == 0 || toutUSD == 0 || rateTok == 0 || rateUSD == 0 {
		t.Fatalf("fixture lacks a kind: %d %d %d %d", toutTok, toutUSD, rateTok, rateUSD)
	}
}

// The totals line is the last header line's neighbour: after the daemon header,
// before the source legend, before the table's column row.
func TestSessionsViewPrintsTotalsWithHeader(t *testing.T) {
	withLastActiveClock(t)
	rows := []api.Session{ticked(360, 450, api.ContextSourceHeartbeat, time.Second)}
	cols := columnsOrFatal(t, "name,active", nil)
	out := renderSessionsView(rows, cols, "cluster alpha   rung default\n", fitOptions{width: 220})
	header := strings.Index(out, "cluster alpha")
	totals := strings.Index(out, "totals")
	legend := strings.Index(out, sourceLegend)
	column := strings.Index(out, "AGENT NAME")
	if header < 0 || totals < 0 || legend < 0 || column < 0 {
		t.Fatalf("missing a part (header %d totals %d legend %d columns %d):\n%s", header, totals, legend, column, out)
	}
	if header >= totals || totals >= legend || legend >= column {
		t.Errorf("order is wrong, want header, totals, legend, columns:\n%s", out)
	}
	if bare := renderSessionsView(rows, cols, "", fitOptions{width: 220}); strings.Contains(bare, "totals") {
		t.Errorf("a view with no header printed totals:\n%s", bare)
	}
}

// clusterConfigShowing is clusterConfig with the totals preferences set.
func clusterConfigShowing(t *testing.T, display config.Display, entries ...config.Cluster) {
	t.Helper()
	resolveFixture(t, false)
	t.Setenv("MARVEL_SESSION", "")
	if err := config.Save(&config.Config{Clusters: entries, Display: display}); err != nil {
		t.Fatal(err)
	}
}

func off() *bool { v := false; return &v }

func twoClusterLines(t *testing.T, display config.Display) []string {
	t.Helper()
	a := startFake(t, fakeAnswers, fakeRows("wa", "a1", "a2"))
	b := startFake(t, fakeAnswers, fakeRows("wb", "b1", "b2", "b3"))
	clusterConfigShowing(t, display, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--header")
	return totalsLineRE.FindAllString(stdout, -1)
}

// Both lines print by default, and the fleet line sums the answered clusters.
func TestFanOutFleetLineSumsAnsweredClusters(t *testing.T) {
	lines := twoClusterLines(t, config.Display{})
	if len(lines) != 3 {
		t.Fatalf("got %d totals lines, want 3 (a, b, fleet): %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[2], "totals fleet ") || sessionsCount(lines[2]) != "5" {
		t.Errorf("fleet line = %q, want 5 sessions", lines[2])
	}
}

func TestFanOutTotalsPerClusterPreferenceHidesItsLines(t *testing.T) {
	lines := twoClusterLines(t, config.Display{TotalsPerCluster: off()})
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "totals fleet ") {
		t.Errorf("totals lines = %q, want only the fleet line", lines)
	}
}

func TestFanOutTotalsFleetPreferenceHidesItsLine(t *testing.T) {
	lines := twoClusterLines(t, config.Display{TotalsFleet: off()})
	if len(lines) != 2 || strings.Contains(strings.Join(lines, "\n"), "fleet") {
		t.Errorf("totals lines = %q, want the two cluster lines only", lines)
	}
}

// The preferences are read from the client config's display block, and an
// absent key shows its line.
func TestDisplayTotalsPreferencesRoundTrip(t *testing.T) {
	clusterConfigShowing(t, config.Display{TotalsPerCluster: off()})
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Display.ShowTotalsPerCluster() || !cfg.Display.ShowTotalsFleet() {
		t.Errorf("display = %+v, want per-cluster off and fleet on", cfg.Display)
	}
	if !(config.Display{}).ShowTotalsPerCluster() || !(config.Display{}).ShowTotalsFleet() {
		t.Error("an absent key should show its line")
	}
}

// Two entries on one socket are one daemon: one line, the first's, counting
// every row.
func TestFanOutTotalsSkipRepeatedAddress(t *testing.T) {
	f := startSD(t, sdStart, []api.Session{row("w", "s1"), row("w", "s2")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: f.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--header")
	lines := totalsLineRE.FindAllString(stdout, -1)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "totals a ") || sessionsCount(lines[0]) != "2" {
		t.Errorf("totals lines = %q, want one, for a, over 2 sessions:\n%s", lines, stdout)
	}
}

// A member merged into another's daemon gets no line, and the rows it kept
// count in the leader's line. Whichever of the two reads sees the extra row,
// the leader's line covers both rows.
func TestFanOutTotalsMergedMemberCountsInLeader(t *testing.T) {
	f := startSD(t, sdStart, []api.Session{row("w", "s1")}, []api.Session{row("w", "s1"), row("w", "x")})
	link := filepath.Join(filepath.Dir(f.sock), "alias.sock")
	if err := os.Symlink(f.sock, link); err != nil {
		t.Fatal(err)
	}
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: link})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--header")
	lines := totalsLineRE.FindAllString(stdout, -1)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "totals a ") || sessionsCount(lines[0]) != "2" {
		t.Errorf("totals lines = %q, want one, for a, over 2 sessions:\n%s", lines, stdout)
	}
}

func fanTestStates(rows map[string][]api.Session, order ...string) []*fanState {
	var states []*fanState
	for _, n := range order {
		s := &fanState{fanWatchCluster: fanWatchCluster{name: n}, fetched: true, have: true, rows: rows[n]}
		s.rec = clusterRecord{Cluster: n, Outcome: outcomeAnswered}
		for i := range s.rows {
			s.rows[i].Cluster = n
		}
		states = append(states, s)
	}
	return states
}

func fanFrame(t *testing.T, states []*fanState, display config.Display) string {
	t.Helper()
	clusterConfigShowing(t, display)
	ws := newWatchState(columnsOrFatal(t, "name", nil))
	cfg := fanWatchConfig{interval: time.Second, keyOf: func(string, string) string { return "" }}
	return renderFanFrame(ws, cfg, states, time.Now())
}

// A cluster whose rows are dropped as a repeat gets no line; each other
// answered cluster gets one.
func TestFanWatchFrameTotalsSkipRepeat(t *testing.T) {
	states := fanTestStates(map[string][]api.Session{
		"a": fakeRows("wa", "a1", "a2"), "c": fakeRows("wc", "c1"),
	}, "a", "b", "c")
	states[1].repeatOf = "a"
	frame := fanFrame(t, states, config.Display{TotalsFleet: off()})
	lines := totalsLineRE.FindAllString(frame, -1)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "totals a ") || !strings.HasPrefix(lines[1], "totals c ") {
		t.Errorf("totals lines = %q, want a and c only:\n%s", lines, frame)
	}
}

// A cluster that failed after a good read, or is late, gets no line in the
// watch frame, though its last rows stay in the table.
func TestFanWatchFrameTotalsSkipFailedAndLateCluster(t *testing.T) {
	states := fanTestStates(map[string][]api.Session{
		"a": fakeRows("wa", "a1"), "b": fakeRows("wb", "b1"), "c": fakeRows("wc", "c1"),
	}, "a", "b", "c")
	states[1].failed = true
	states[1].rec = clusterRecord{Cluster: "b", Outcome: outcomeUnreachable}
	states[2].late = true
	frame := fanFrame(t, states, config.Display{})
	lines := totalsLineRE.FindAllString(frame, -1)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "totals a ") {
		t.Errorf("totals lines = %q, want only a's:\n%s", lines, frame)
	}
	if !strings.Contains(frame, "b1") || !strings.Contains(frame, "c1") {
		t.Errorf("the last rows of b and c left the table:\n%s", frame)
	}
}

// A disconnected frame prints no totals, even with last known rows.
func TestWatchFrameDisconnectedHasNoTotals(t *testing.T) {
	withRateClock(t)
	ws := watchFrameState([]api.Session{rateSession("a", 40, 30*time.Second, 30*time.Second)}, "name", t)
	ws.disconnected = true
	frame := renderWatchFrame(ws, time.Second, rateNow)
	if !strings.Contains(frame, "last known state") {
		t.Fatalf("setup: no last known rows in the frame:\n%s", frame)
	}
	if totalsLineRE.MatchString(frame) {
		t.Errorf("a disconnected frame printed totals:\n%s", frame)
	}
}

var allTeamsRE = regexp.MustCompile(`(?m)^all teams.*$`)

func allTeamsFields(t *testing.T, out string) []string {
	t.Helper()
	line := allTeamsRE.FindString(out)
	if line == "" {
		t.Fatalf("no all teams row:\n%s", out)
	}
	return fieldGap.Split(strings.TrimSpace(line), -1)
}

func teamPair(a, b func(*api.TeamLoad)) []api.TeamLoad {
	return []api.TeamLoad{loadRow(a), loadRow(b)}
}

// The all teams row sums seats and limited, and its ACTIVE% is the mean over
// seats: 50 over 2 and 80 over 4 is 70, not 65.
func TestTeamLoadAllTeamsRow(t *testing.T) {
	rows := teamPair(
		func(r *api.TeamLoad) {
			r.Team, r.Declared, r.Running, r.ActiveMeanPct, r.ActiveMeasured, r.Limited = "one", 3, 2, 50, 2, 1
		},
		func(r *api.TeamLoad) {
			r.Team, r.Declared, r.Running, r.ActiveMeanPct, r.ActiveMeasured, r.Limited = "two", 5, 4, 80, 4, 2
		})
	got := allTeamsFields(t, renderRows(t, loadAt, rows...))
	want := []string{"all teams", "6/8", "70% (6 of 6)", "3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("all teams row = %q, want %q", got, want)
	}
}

func TestTeamLoadAllTeamsNoMeasuredSeat(t *testing.T) {
	rows := teamPair(
		func(r *api.TeamLoad) { r.Team, r.ActiveMeasured, r.ActiveMeanPct = "one", 0, 0 },
		func(r *api.TeamLoad) { r.Team, r.ActiveMeasured, r.ActiveMeanPct = "two", 0, 0 })
	got := allTeamsFields(t, renderRows(t, loadAt, rows...))
	if len(got) != 4 || got[2] != "-" {
		t.Errorf("all teams row = %q, want a dash for ACTIVE%%", got)
	}
}

// An expired team row adds nothing and the row says how many teams it covers.
func TestTeamLoadAllTeamsFreshOnlyMarked(t *testing.T) {
	fresh := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeanPct, r.ActiveMeasured, r.Limited = "one", 3, 2, 50, 2, 1
	})
	expired := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeanPct, r.ActiveMeasured, r.Limited = "two", 5, 4, 80, 4, 2
		r.ObservedAt = loadAt.Add(-time.Hour)
		r.ValidUntil = loadAt.Add(-time.Hour + api.TeamLoadValidity)
	})
	out := renderRows(t, loadAt, fresh, expired)
	got := allTeamsFields(t, out)
	want := []string{"all teams", "(1 of 2 teams)", "2/3", "50% (2 of 2)", "1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("all teams row = %q, want %q", got, want)
	}
	if strings.Contains(allTeamsRE.FindString(out), "?") {
		t.Errorf("the all teams row printed a question mark: %q", allTeamsRE.FindString(out))
	}
}
