package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// ticked is a seat whose tick ring says active of total ticks were not quiet,
// under a ten minute window, last counted at ago before lastActiveNow.
func ticked(active, total int, source api.ContextSourceKind, ago time.Duration) api.Session {
	s := activeSession("a", time.Minute, source)
	s.ActiveTicks = api.ActiveTicks{
		Active: active, Total: total, Window: 10 * time.Minute,
		Observable: true, Full: true, At: lastActiveNow.Add(-ago),
	}
	return s
}

// ACTIVE% is the share of the ring's ticks that found the seat not quiet,
// divided when printed.
func TestActivePctFromTickRing(t *testing.T) {
	for _, tc := range []struct {
		active, total int
		want          string
	}{
		{360, 450, "80%"},
		{0, 450, "0%"},
		{450, 450, "100%"},
		{150, 450, "33%"},
		{226, 450, "50%"},
	} {
		s := ticked(tc.active, tc.total, api.ContextSourceAccountant, 2*time.Second)
		if got := activePctCell(s, lastActiveNow); got != tc.want {
			t.Errorf("%d of %d: cell = %q, want %q", tc.active, tc.total, got, tc.want)
		}
	}
}

// Until the ring has watched the whole window the cell is a dash, whatever the
// counts so far say.
func TestActivePctDashBeforeWindowFills(t *testing.T) {
	s := ticked(300, 301, api.ContextSourceAccountant, time.Second)
	s.ActiveTicks.Full = false
	if got := activePctCell(s, lastActiveNow); got != "-" {
		t.Errorf("cell = %q before the window filled, want -", got)
	}
}

// A daemon that has just restarted has an empty ring, which the daemon reports as
// a reading with nothing in it. It is a dash, and so is a session the daemon did
// not stamp at all.
func TestActivePctDashAfterDaemonRestart(t *testing.T) {
	empty := activeSession("a", time.Minute, api.ContextSourceAccountant)
	empty.ActiveTicks = api.ActiveTicks{Observable: true, Window: 10 * time.Minute}
	if got := activePctCell(empty, lastActiveNow); got != "-" {
		t.Errorf("an empty ring: cell = %q, want -", got)
	}
	if got := activePctCell(activeSession("a", time.Minute, api.ContextSourceAccountant), lastActiveNow); got != "-" {
		t.Errorf("an unstamped session: cell = %q, want -", got)
	}
}

// A seat marvel has no activity channel for reads a dash, not a zero.
func TestActivePctDashWithoutActivityChannel(t *testing.T) {
	s := ticked(0, 450, api.ContextSourceNone, time.Second)
	s.ActiveTicks.Observable = false
	if got := activePctCell(s, lastActiveNow); got != "-" {
		t.Errorf("cell = %q for a seat with no activity channel, want -", got)
	}
}

// The same share reads differently by source: a statusline or heartbeat
// reporting is weaker than token flow, and the cell says so with the mark.
func TestActivePctStatuslineAndStreamRenderDifferently(t *testing.T) {
	hb := ticked(360, 450, api.ContextSourceHeartbeat, time.Second)
	tok := ticked(360, 450, api.ContextSourceAccountant, time.Second)
	if got := activePctCell(hb, lastActiveNow); got != "80%*" {
		t.Errorf("heartbeat source: cell = %q, want 80%%*", got)
	}
	if got := activePctCell(tok, lastActiveNow); got != "80%" {
		t.Errorf("stream source: cell = %q, want 80%%", got)
	}
}

// The reading is as fresh as its newest tick: it carries a ValidUntil, and a
// reading older than that prints a question mark, never the old number.
func TestActivePctExpiresWhenTheCountsAreOld(t *testing.T) {
	fresh := ticked(360, 450, api.ContextSourceAccountant, 2*time.Second)
	c := activePctAsOf(fresh, lastActiveNow)
	if c.ValidUntil.IsZero() {
		t.Fatal("ValidUntil is zero: the cell would show a number forever")
	}
	if c.Source != string(api.ContextSourceAccountant) || !c.ObservedAt.Equal(fresh.ActiveTicks.At) {
		t.Errorf("cell = %+v, want the context source and the newest tick's time", c)
	}
	old := ticked(360, 450, api.ContextSourceAccountant, 10*time.Minute)
	if got := activePctCell(old, lastActiveNow); got != asof.MarkStale {
		t.Errorf("cell = %q for counts ten minutes old, want %q", got, asof.MarkStale)
	}
	oldMarked := ticked(360, 450, api.ContextSourceHeartbeat, 10*time.Minute)
	if got := activePctCell(oldMarked, lastActiveNow); got != asof.MarkStale {
		t.Errorf("cell = %q for stale heartbeat counts, want a bare %q with no mark", got, asof.MarkStale)
	}
}

func TestActivePctColumnInWideNotInDefault(t *testing.T) {
	has := func(cols []sessionColumn) bool {
		for _, c := range cols {
			if c.name == "active" {
				return true
			}
		}
		return false
	}
	if !has(columnsOrFatal(t, "wide", nil)) {
		t.Error("the wide set should include active")
	}
	if has(defaultSessionColumns()) {
		t.Error("the default table should not include active")
	}
	if !has(columnsOrFatal(t, "active", nil)) {
		t.Error("active should be selectable by name")
	}
}

// A marked ACTIVE% earns the legend line like a marked LAST-ACTIVE does, on a
// terminal wide enough to show it; a plain one earns none; a pipe keeps the
// mark and drops the legend. The width fit shows LAST-ACTIVE at any width, so
// the mark test looks at the ACTIVE% column alone.
func TestSourceLegendOnTTYWhenAMarkIsShown(t *testing.T) {
	marked := []api.Session{ticked(360, 450, api.ContextSourceHeartbeat, time.Second)}
	if !sourceMarkOnScreen(marked, columnsOrFatal(t, "active", nil)) {
		t.Error("a marked ACTIVE% cell is not counted as a mark on screen")
	}
	out := sessionsView(t, marked, "name,active", fitOptions{width: 220})
	if !strings.Contains(out, "80%*") || strings.Count(out, legendLine) != 1 {
		t.Errorf("a wide terminal with a marked ACTIVE%% should show it and print the legend once:\n%s", out)
	}
}

func TestSourceLegendAbsentWhenNoMarkOrPiped(t *testing.T) {
	plain := []api.Session{ticked(360, 450, api.ContextSourceAccountant, time.Second)}
	if sourceMarkOnScreen(plain, columnsOrFatal(t, "active", nil)) {
		t.Error("a plain ACTIVE% cell is counted as a mark on screen")
	}
	if out := sessionsView(t, plain, "name,active", fitOptions{width: 220}); strings.Contains(out, legendLine) {
		t.Errorf("no cell carries a mark, so no legend:\n%s", out)
	}
	marked := []api.Session{ticked(360, 450, api.ContextSourceHeartbeat, time.Second)}
	out := sessionsView(t, marked, "name,active", fitOptions{})
	if strings.Contains(out, legendLine) {
		t.Errorf("a pipe should print no legend:\n%s", out)
	}
	if !strings.Contains(out, "80%*") {
		t.Errorf("a pipe should keep the mark in the value:\n%s", out)
	}
}

// get --help says what ACTIVE% claims, so the mark is not the only place the
// caveat lives.
func TestHelpDocumentsSourceMark(t *testing.T) {
	long := getCmd().Long
	for _, want := range []string{"ACTIVE%", "not quiet", "not busy"} {
		if !strings.Contains(long, want) {
			t.Errorf("get --help should mention %q for ACTIVE%%:\n%s", want, long)
		}
	}
}

// JSON keeps the source and the counts as their own fields. The star is part
// of the printed cell and never appears in the data.
func TestJSONKeepsSourceSeparateFromMark(t *testing.T) {
	s := ticked(360, 450, api.ContextSourceHeartbeat, time.Second)
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["ContextSource"] != string(api.ContextSourceHeartbeat) {
		t.Errorf("context source field = %v, want %q", got["ContextSource"], api.ContextSourceHeartbeat)
	}
	ticks, ok := got["active_ticks"].(map[string]any)
	if !ok || ticks["active"] != float64(360) || ticks["total"] != float64(450) {
		t.Errorf("active_ticks = %v, want the counts as numbers", got["active_ticks"])
	}
	if strings.Contains(string(raw), "*") {
		t.Errorf("a mark leaked into the JSON: %s", raw)
	}
}
