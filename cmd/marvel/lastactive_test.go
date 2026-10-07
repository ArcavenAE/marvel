package main

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

var lastActiveNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func activeSession(name string, ago time.Duration, source api.ContextSourceKind) api.Session {
	s := api.Session{
		Name: name, Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, HealthState: api.HealthHealthy, PaneID: "%3",
	}
	s.ContextSource = source
	if ago >= 0 {
		s.ContextAt = lastActiveNow.Add(-ago)
	}
	return s
}

// LAST-ACTIVE is the time since ContextAt, in the age words the header uses.
func TestLastActiveFromContextAt(t *testing.T) {
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{3 * time.Minute, "3m"},
		{2*time.Hour + 9*time.Minute, "2h9m"},
	} {
		s := activeSession("a", tc.ago, api.ContextSourceAccountant)
		if got := lastActiveCell(s, lastActiveNow); got != tc.want {
			t.Errorf("ago %v: cell = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

// A seat with no reading shows a dash, never an age it did not measure, and
// a dash carries no source mark.
func TestLastActiveDashWhenUnmeasured(t *testing.T) {
	for _, src := range []api.ContextSourceKind{api.ContextSourceNone, api.ContextSourceAccountant, api.ContextSourceHeartbeat} {
		s := activeSession("a", -1, src)
		if got := lastActiveCell(s, lastActiveNow); got != "-" {
			t.Errorf("source %q with no ContextAt: cell = %q, want -", src, got)
		}
	}
}

// A reading a statusline or a heartbeat wrote carries a trailing star; one
// the token stream wrote carries none. The mark is part of the value.
func TestLastActiveMarksStatuslineSource(t *testing.T) {
	hb := activeSession("a", 3*time.Minute, api.ContextSourceHeartbeat)
	if got := lastActiveCell(hb, lastActiveNow); got != "3m*" {
		t.Errorf("heartbeat source: cell = %q, want 3m*", got)
	}
	for _, src := range []api.ContextSourceKind{api.ContextSourceAccountant, api.ContextSourceNone} {
		tok := activeSession("b", 3*time.Minute, src)
		if got := lastActiveCell(tok, lastActiveNow); got != "3m" {
			t.Errorf("source %q: cell = %q, want 3m with no mark", src, got)
		}
	}
}

// The cell is an age, so it never expires: a reading from days ago still
// prints its age and never turns into "?". ValidUntil is zero on purpose
// (a deviation from the ticket's note, which asks tests to assert it is
// set); any expiry would hide the one number the cell exists to show.
func TestLastActiveNeverExpires(t *testing.T) {
	s := activeSession("a", 72*time.Hour, api.ContextSourceAccountant)
	c := lastActiveAsOf(s, lastActiveNow)
	if !c.ValidUntil.IsZero() {
		t.Errorf("ValidUntil = %v, want zero: an age does not expire", c.ValidUntil)
	}
	if got := c.State(lastActiveNow); got != asof.Fresh {
		t.Errorf("state = %q, want fresh", got)
	}
	if got := lastActiveCell(s, lastActiveNow); got != "72h0m" {
		t.Errorf("cell = %q, want 72h0m", got)
	}
	if got := lastActiveAsOf(s, lastActiveNow).Source; got != string(api.ContextSourceAccountant) {
		t.Errorf("source = %q, want the context source", got)
	}
}

func withLastActiveClock(t *testing.T) {
	t.Helper()
	old := lastActiveClock
	lastActiveClock = func() time.Time { return lastActiveNow }
	t.Cleanup(func() { lastActiveClock = old })
}

func sessionsView(t *testing.T, sessions []api.Session, cols string, fit fitOptions) string {
	t.Helper()
	withLastActiveClock(t)
	columns := columnsOrFatal(t, cols, nil)
	return captureStdout(t, func() {
		if err := printSessionsFrom(sessions, columns, "cluster  alpha\n\n", fit); err != nil {
			t.Fatal(err)
		}
	})
}

// LAST-ACTIVE is in the wide set and not in the default table, so a pipe
// prints what it always printed.
func TestLastActiveColumnInWideNotInDefault(t *testing.T) {
	has := func(cols []sessionColumn) bool {
		for _, c := range cols {
			if c.name == "last-active" {
				return true
			}
		}
		return false
	}
	if !has(columnsOrFatal(t, "wide", nil)) {
		t.Error("the wide set should include last-active")
	}
	if has(defaultSessionColumns()) {
		t.Error("the default table should not include last-active")
	}
	if !has(columnsOrFatal(t, "last-active", nil)) {
		t.Error("last-active should be selectable by name")
	}
}

// The fit counts the printed cell, mark included: a table at exactly its
// printed width keeps every column, and one column under it drops the last.
// The mark can never widen the LAST-ACTIVE column past its header, so this
// pins the boundary and cannot tell a fit that ignores the mark.
func TestFitCountsSourceMark(t *testing.T) {
	withLastActiveClock(t)
	sessions := []api.Session{activeSession("agent-0", 13*time.Hour+30*time.Minute, api.ContextSourceHeartbeat)}
	cols := columnsOrFatal(t, "name,state,health,context,last-active", nil)
	full := renderSessionTableCols(sessions, cols)
	if !strings.Contains(full, "13h30m*") {
		t.Fatalf("fixture lost its mark:\n%s", full)
	}
	edge := widest(full)
	at := fitSessionTable(sessions, namedColumns([]string{"name", "state", "health", "context", "last-active"}), fitOptions{width: edge})
	if got := fitHeaders(at.table); len(got) != 5 {
		t.Errorf("at the printed width %d: columns = %v, want all five", edge, got)
	}
	under := fitSessionTable(sessions, namedColumns([]string{"name", "state", "health", "context", "last-active"}), fitOptions{width: edge - 1})
	if got := fitHeaders(under.table); len(got) != 4 {
		t.Errorf("one under %d: columns = %v, want four", edge, got)
	}
}

const legendLine = "* reported by statusline or heartbeat, not token flow"

// When any visible cell carries the mark, one legend line prints with the
// header, on a terminal only.
func TestLegendPrintsWhenAMarkIsOnScreen(t *testing.T) {
	marked := []api.Session{
		activeSession("a", time.Minute, api.ContextSourceHeartbeat),
		activeSession("b", time.Minute, api.ContextSourceHeartbeat),
	}
	out := sessionsView(t, marked, "name,last-active", fitOptions{width: 100})
	if strings.Count(out, legendLine) != 1 {
		t.Errorf("a terminal with marked cells should print the legend once:\n%s", out)
	}
	if strings.Index(out, legendLine) > strings.Index(out, "AGENT NAME") {
		t.Errorf("the legend belongs with the header, above the table:\n%s", out)
	}
}

func TestLegendAbsentWithoutAMark(t *testing.T) {
	tok := []api.Session{activeSession("a", time.Minute, api.ContextSourceAccountant)}
	if out := sessionsView(t, tok, "name,last-active", fitOptions{width: 100}); strings.Contains(out, legendLine) {
		t.Errorf("no cell carries a mark, so no legend:\n%s", out)
	}
}

// Piped output keeps the mark in the value and drops the legend, even when
// --header forces the header on.
func TestLegendDroppedWhenPipedButMarkKept(t *testing.T) {
	marked := []api.Session{activeSession("a", time.Minute, api.ContextSourceHeartbeat)}
	out := sessionsView(t, marked, "name,last-active", fitOptions{})
	if strings.Contains(out, legendLine) {
		t.Errorf("a pipe should print no legend:\n%s", out)
	}
	if !strings.Contains(out, "1m*") {
		t.Errorf("a pipe should keep the mark in the value:\n%s", out)
	}
}

// A mark that the width fit hid is not on screen, so it earns no legend. A
// guard: it passes against the stub because no column exists yet.
func TestLegendAbsentWhenTheMarkedColumnIsHidden(t *testing.T) {
	marked := []api.Session{activeSession("a", time.Minute, api.ContextSourceHeartbeat)}
	out := sessionsView(t, marked, "wide", fitOptions{width: 30})
	if strings.Contains(out, "1m*") || strings.Contains(out, legendLine) {
		t.Errorf("the width hid LAST-ACTIVE, so neither the mark nor the legend should show:\n%s", out)
	}
}

// --help documents the mark.
func TestHelpDocumentsTheSourceMark(t *testing.T) {
	long := getCmd().Long
	if !strings.Contains(long, "reported by statusline or heartbeat") {
		t.Errorf("get --help should document the source mark:\n%s", long)
	}
}
