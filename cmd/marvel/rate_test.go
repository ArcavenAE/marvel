package main

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

var rateNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// rateSession is a session with a rate cell observed age ago and a context
// reading (the activity channel's own stamp) contextAge ago. The cell's
// ValidUntil is set: a zero one means never stale, and a fixture that left it
// out would show a number forever.
func rateSession(name string, value float64, age, contextAge time.Duration) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.OutRate = asof.Cell[float64]{
		Value:      value,
		ObservedAt: rateNow.Add(-age),
		ValidUntil: rateNow.Add(-age).Add(api.DefaultQuietWindow),
	}
	s.ContextAt = rateNow.Add(-contextAge)
	return s
}

func withRateClock(t *testing.T) {
	t.Helper()
	old := rateClock
	rateClock = func() time.Time { return rateNow }
	t.Cleanup(func() { rateClock = old })
}

func rateCells(t *testing.T, sessions ...api.Session) map[string]string {
	t.Helper()
	withRateClock(t)
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,rate", nil))
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n")[1:] {
		cells := splitColumns(line)
		out[cells[0]] = cells[1]
	}
	return out
}

// RATE draws the rate through the as-of cell: a fresh reading prints the
// value decayed to now, a seat never sampled prints a dash, and a seat that
// went quiet prints exactly 0.
func TestRateCellRendersThroughTheAsOfCell(t *testing.T) {
	never := api.Session{Name: "c-never", Workspace: "ws", Team: "squad", Role: "worker", PaneID: "%4"}
	got := rateCells(t,
		// 40 tokens a second observed 30s ago, 20s half-life: 40 * 2^-1.5 = 14.1.
		rateSession("a-fresh", 40, 30*time.Second, 30*time.Second),
		rateSession("b-quiet", 40, 11*time.Minute, 11*time.Minute),
		never,
	)
	if got["a-fresh"] != "14.1/s" {
		t.Errorf("fresh rate = %q, want 14.1/s", got["a-fresh"])
	}
	if got["b-quiet"] != "0/s" {
		t.Errorf("a quiet seat = %q, want exactly 0/s", got["b-quiet"])
	}
	if got["c-never"] != "-" {
		t.Errorf("a seat never sampled = %q, want a dash, not a zero", got["c-never"])
	}
}

// A reading that expired while the seat is still reporting (its context
// stamp is recent, so it is not quiet) prints "?", never the old number.
func TestRateCellStaleRendersQuestion(t *testing.T) {
	s := rateSession("a-stale", 40, 11*time.Minute, time.Minute)
	if s.OutRate.ValidUntil.IsZero() {
		t.Fatal("setup: the fixture must set ValidUntil, or the cell never goes stale")
	}
	got := rateCells(t, s)
	if got["a-stale"] != "?" {
		t.Errorf("an expired reading on a seat that is not quiet = %q, want ?", got["a-stale"])
	}
	if strings.Contains(got["a-stale"], "/s") {
		t.Errorf("an expired number was shown: %q", got["a-stale"])
	}
}

// Sorting by RATE orders by the value the cell shows, not by its text:
// 9.0 is below 40 though "9" sorts after "4", a seat with no number
// (never sampled or expired) sorts below every number, a quiet 0 included,
// and the name breaks a tie.
func TestRateCellSortsByValue(t *testing.T) {
	withRateClock(t)
	sessions := []api.Session{
		rateSession("d-nine", 9, 0, 0),
		rateSession("b-forty", 40, 0, 0),
		{Name: "a-never", Workspace: "ws"},
		rateSession("e-expired", 40, 11*time.Minute, time.Minute),
		rateSession("c-forty", 40, 0, 0),
		rateSession("0-quiet", 40, 11*time.Minute, 11*time.Minute), // exactly 0/s: a number, above no number
	}
	sortSessions(sessions, &watchSort{column: "rate"})
	var asc []string
	for _, s := range sessions {
		asc = append(asc, s.Name)
	}
	if want := "a-never,e-expired,0-quiet,d-nine,b-forty,c-forty"; strings.Join(asc, ",") != want {
		t.Errorf("ascending = %s, want %s", strings.Join(asc, ","), want)
	}
	sortSessions(sessions, &watchSort{column: "rate", desc: true})
	var desc []string
	for _, s := range sessions {
		desc = append(desc, s.Name)
	}
	if want := "c-forty,b-forty,d-nine,0-quiet,e-expired,a-never"; strings.Join(desc, ",") != want {
		t.Errorf("descending = %s, want %s", strings.Join(desc, ","), want)
	}
}

// RATE is selectable by name and in the wide set, and not in the default
// table a pipe prints.
func TestRateColumnInWideNotInDefault(t *testing.T) {
	has := func(cols []sessionColumn) bool {
		for _, c := range cols {
			if c.name == "rate" {
				return true
			}
		}
		return false
	}
	if !has(columnsOrFatal(t, "wide", nil)) || !has(columnsOrFatal(t, "rate", nil)) {
		t.Error("rate should be selectable and in the wide set")
	}
	if has(defaultSessionColumns()) {
		t.Error("the default table should not include rate")
	}
}

// withWindow stamps the quiet window the daemon judged the seat under, as the
// daemon does on every read (ActiveTicks.Window).
func withWindow(s api.Session, w time.Duration) api.Session {
	s.ActiveTicks.Window = w
	return s
}

// RATE reads a seat quiet by the window the daemon stamped, so a role with a
// short activity_timeout shows 0 at the point the daemon calls it quiet, not
// ten minutes later (aae-orc-b49a7).
func TestRateUsesAShortRoleWindow(t *testing.T) {
	// Last sign of work 3m ago; the role's window is 2m, so the seat is quiet.
	// Under the default 10m window the cell would still show a decaying number.
	got := rateCells(t, withWindow(rateSession("a-short", 40, 30*time.Second, 3*time.Minute), 2*time.Minute))
	if got["a-short"] != "0/s" {
		t.Errorf("a seat quiet under its role's 2m window = %q, want 0/s", got["a-short"])
	}
}

// A role with a long window keeps its number past the default ten minutes: the
// seat is not quiet to the daemon, so the cell must not zero it.
func TestRateUsesALongRoleWindow(t *testing.T) {
	got := rateCells(t, withWindow(rateSession("a-long", 40, 30*time.Second, 15*time.Minute), 30*time.Minute))
	if got["a-long"] != "14.1/s" {
		t.Errorf("a seat not quiet under its role's 30m window = %q, want 14.1/s", got["a-long"])
	}
}

// A session with no stamped window (a held role's synthetic row, or a daemon
// that does not stamp one) falls back to the default window.
func TestRateFallsBackToTheDefaultWindow(t *testing.T) {
	got := rateCells(t,
		withWindow(rateSession("a-unstamped-quiet", 40, 30*time.Second, 11*time.Minute), 0),
		withWindow(rateSession("b-unstamped-live", 40, 30*time.Second, 9*time.Minute), 0),
	)
	if got["a-unstamped-quiet"] != "0/s" {
		t.Errorf("quiet past the default window = %q, want 0/s", got["a-unstamped-quiet"])
	}
	if got["b-unstamped-live"] != "14.1/s" {
		t.Errorf("not quiet inside the default window = %q, want 14.1/s", got["b-unstamped-live"])
	}
}
