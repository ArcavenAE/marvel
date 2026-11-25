package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#1598 (I-791), ticket M1: one width function, the rune count, shared
// by the table fit, the totals wrap and the watch height budget; and a
// physical-row height budget that counts a line wider than the terminal as
// the rows it wraps onto.

// physicalRows is what a terminal of the given width spends on a frame: each
// line takes max(1, ceil(runes/width)) rows, and a line exactly width wide
// takes one, because terminals defer the wrap.
func physicalRows(frame string, width int) int {
	n := 0
	for _, l := range strings.Split(strings.TrimSuffix(frame, "\n"), "\n") {
		n += max(1, (utf8.RuneCountInString(l)+width-1)/width)
	}
	return n
}

// A totals line is measured in runes, as the table is. A label holding U+FFFD
// is one rune and three bytes, so a line of 66 runes and 106 bytes fits 80.
func TestWrapTotalsCountsRunesNotBytes(t *testing.T) {
	label := strings.Repeat("�", 20)
	line := "totals " + label + strings.Repeat("   field-0123", 3)
	if r := utf8.RuneCountInString(line); r > 80 {
		t.Fatalf("fixture is %d runes, want at most 80", r)
	}
	if got := wrapTotals(line, 80); got != line+"\n" {
		t.Errorf("wrapTotals broke a %d-rune line at width 80:\n%s", utf8.RuneCountInString(line), got)
	}
}

// A line exactly as wide as the terminal counts one row and is not broken,
// whatever its bytes: 80 runes, three fields, multibyte characters in the lead.
func TestExactWidthLineCountsOneRow(t *testing.T) {
	line := "totals " + strings.Repeat("é", 20) + "   " + strings.Repeat("a", 14) + "   " + strings.Repeat("b", 15) + "   " + strings.Repeat("c", 15)
	if r := utf8.RuneCountInString(line); r != 80 {
		t.Fatalf("fixture is %d runes, want exactly 80", r)
	}
	got := wrapTotals(line, 80)
	if rows := physicalRows(got, 80); rows != 1 {
		t.Errorf("a line exactly 80 wide counted %d rows:\n%s", rows, got)
	}
}

// The -w frame fits the terminal's height in physical rows. With 130-character
// names the table is wider than 120, so each row wraps onto a second row,
// and a budget that counts logical lines overruns the screen.
func TestFanWatchBudgetCountsPhysicalRows(t *testing.T) {
	withRateClock(t)
	setWidth(t, 120)
	// One named column and names wider than the terminal: no choice of what
	// the default set shows, and no fit that keeps a column, can stop the rows
	// wrapping (marvel#1598).
	cols, explicit, err := loadSessionColumnsSel("name")
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
	ws.explicit = explicit
	ws.height = 20
	cfg := fanWatchConfig{interval: 2 * time.Second, keyOf: func(c, fp string) string { return c }}
	var states []*fanState
	for _, c := range []string{"kinu", "corporate", "mokuzai"} {
		var rows []api.Session
		for i := 0; i < 12; i++ {
			s := sampled(fmt.Sprintf("builder-%02d-%s", i, strings.Repeat("n", 130-len("builder-00-"))), api.SessionRunning, 12, 300<<20)
			s.Cluster = c
			rows = append(rows, s)
		}
		states = append(states, &fanState{
			fanWatchCluster: fanWatchCluster{name: c},
			rec:             clusterRecord{Cluster: c, Outcome: outcomeAnswered},
			rows:            rows, asOf: rateNow, have: true, fetched: true,
		})
	}
	frame := renderFanFrame(ws, cfg, states, rateNow)
	if got := physicalRows(frame, 120); got > 20 {
		t.Errorf("the frame takes %d physical rows on a 120x20 terminal:\n%s", got, frame)
	}
}

// Guard, green before and after the fix: a frame whose table lines are exactly
// as wide as the terminal takes one row per line, so a height that holds the
// whole frame shows every row. It catches an off-by-one in the budget's row
// count, which TestExactWidthLineCountsOneRow (wrapTotals only) does not reach.
func TestFanWatchBudgetExactWidthLineIsOneRow(t *testing.T) {
	withRateClock(t)
	setWidth(t, 120)
	cols, explicit, err := loadSessionColumnsSel("name")
	if err != nil {
		t.Fatal(err)
	}
	build := func(height int) string {
		ws := newWatchState(cols)
		ws.explicit = explicit
		ws.height = height
		cfg := fanWatchConfig{interval: 2 * time.Second, keyOf: func(c, fp string) string { return c }}
		var rows []api.Session
		for i := 0; i < 8; i++ {
			s := sampled(fmt.Sprintf("%02d%s", i, strings.Repeat("n", 118)), api.SessionRunning, 12, 300<<20)
			s.Cluster = "kinu"
			rows = append(rows, s)
		}
		st := &fanState{
			fanWatchCluster: fanWatchCluster{name: "kinu"},
			rec:             clusterRecord{Cluster: "kinu", Outcome: outcomeAnswered},
			rows:            rows, asOf: rateNow, have: true, fetched: true,
		}
		return renderFanFrame(ws, cfg, []*fanState{st}, rateNow)
	}
	whole := build(0)
	n := physicalRows(whole, 120)
	if lines := strings.Count(whole, "\n"); n != lines {
		t.Fatalf("fixture has %d lines but %d physical rows; a line is not exactly 120 wide:\n%s", lines, n, whole)
	}
	if !strings.Contains(build(n-3), "showing rows") {
		t.Fatalf("fixture is too loose: a height of %d does not cut the frame", n-3)
	}
	if got := build(n); strings.Contains(got, "showing rows") {
		t.Errorf("a height of %d holds the whole frame, but the budget cut it:\n%s", n, got)
	}
}
