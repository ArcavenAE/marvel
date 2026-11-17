package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The totals line in the single-cluster -w frame: docs/design/cluster-rollup-header.md,
// sections 2.4 and 2.8.

// meteredRows is n rows that each print an mCPU and an RSS value, so the totals
// line has fields to carry.
func meteredRows(n int) []api.Session {
	rows := selRows(n)
	for i := range rows {
		rows[i].State = api.SessionRunning
		rows[i].CPUPercent, rows[i].RSSBytes, rows[i].MetricsAt = 10, 1<<20, watchT0
	}
	return rows
}

// totalsRig is a watch screen with the daemon header on, the columns named
// explicitly (so the fit never drops one) and the rate clock pinned.
func totalsRig(t *testing.T, rows []api.Session, columns string) *watchSort {
	t.Helper()
	withRateClock(t)
	ws := selRig(t, rows)
	ws.columns = columnsOrFatal(t, columns, nil)
	ws.explicit, ws.showHeader = true, true
	ws.header = headerInfo{Address: "alpha"}
	return ws
}

// totalsLines returns the frame's lines that belong to the totals line: the
// first starts with "totals", and a continuation starts the same way.
func totalsLines(frame string) []string {
	var out []string
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "totals") {
			out = append(out, l)
		}
	}
	return out
}

func frameHeight(frame string) int {
	return len(strings.Split(strings.TrimRight(frame, "\n"), "\n"))
}

// The totals line counts against the screen height, in the frame the cursor
// moves through and in the frame of held rows: the frame never grows past the
// terminal and the selected row stays on screen.
func TestWatchTotalsFitHeightBudget(t *testing.T) {
	const height = 12
	ws := totalsRig(t, meteredRows(30), "name,cpu,rss")
	ws.height = height
	for _, at := range []int{0, 14, 29} {
		for selectedIndex(ws) < at {
			press(t, ws, 'j')
		}
		frame := frameOf(ws)
		if len(totalsLines(frame)) == 0 {
			t.Errorf("selected row %d: the frame has no totals line:\n%s", at, frame)
		}
		if n := frameHeight(frame); n > height {
			t.Errorf("selected row %d: frame is %d lines, want at most %d:\n%s", at, n, height, frame)
		}
		if name := fmt.Sprintf("seat%02d", at); !strings.Contains(frame, name) {
			t.Errorf("selected row %s is not on screen:\n%s", name, frame)
		}
	}

	// The frame of held rows after a failed fetch counts the line too. It is
	// taller than the live frame (a warning, the header and a heading above the
	// table), so it gets a taller screen.
	const heldHeight = 16
	ws.height = heldHeight
	ws.apply(watchData{err: fmt.Errorf("daemon gone"), at: watchT0.Add(time.Second)})
	frame := frameOf(ws)
	if !strings.Contains(frame, "last known state") {
		t.Fatalf("fixture: the frame is not the disconnected one:\n%s", frame)
	}
	if len(totalsLines(frame)) == 0 {
		t.Errorf("the disconnected frame has no totals line:\n%s", frame)
	}
	if n := frameHeight(frame); n > heldHeight {
		t.Errorf("disconnected frame is %d lines, want at most %d:\n%s", n, heldHeight, frame)
	}
	if !strings.Contains(frame, "seat29") {
		t.Errorf("the selected row is not on the disconnected screen:\n%s", frame)
	}
}

// selectedIndex is the position of the selected row in the sorted rows.
func selectedIndex(ws *watchSort) int {
	for i, s := range ws.lastSessions {
		if ws.sel.is(s) {
			return i
		}
	}
	return -1
}

// A line wider than the terminal breaks onto a second line, and the suffix of
// a cut table can be what tips it over. The line counted in the height budget
// is the line drawn (design section 2.8).
func TestWatchTotalsSuffixWrapStaysInHeight(t *testing.T) {
	const height = 12
	ws := totalsRig(t, meteredRows(30), "name,cpu,rss")
	// Measure the line with no cut, then size the terminal to it exactly.
	frame := frameOf(ws)
	base := totalsLines(frame)
	if len(base) != 1 {
		t.Fatalf("fixture: want one totals line without a cut, got %d:\n%s", len(base), frame)
	}
	setWidth(t, len(base[0]))
	ws.height = height
	frame = frameOf(ws)
	lines := totalsLines(frame)
	if len(lines) < 2 {
		t.Fatalf("the cut suffix should push the line onto a second line at width %d, got %d lines:\n%s", len(base[0]), len(lines), frame)
	}
	if n := frameHeight(frame); n > height {
		t.Errorf("frame is %d lines, want at most %d:\n%s", n, height, frame)
	}
	if !strings.Contains(frame, "seat00") {
		t.Errorf("the selected row is not on screen:\n%s", frame)
	}
}

// The totals cover every row the daemon returned, so moving the cursor never
// changes them, and a cut table says so on the line itself.
func TestWatchTotalsCursorStable(t *testing.T) {
	ws := totalsRig(t, meteredRows(30), "name,cpu,rss")
	ws.height = 12
	first := totalsLines(frameOf(ws))
	if len(first) == 0 {
		t.Fatalf("the frame has no totals line:\n%s", frameOf(ws))
	}
	if !strings.Contains(first[0], "30 sessions") || !strings.Contains(first[0], "mCPU 3000 (30)") {
		t.Errorf("the totals should cover all 30 rows, not the rows drawn:\n%s", first[0])
	}
	if got := first[len(first)-1]; !strings.HasSuffix(got, "   covers all 30 rows") {
		t.Errorf("a cut table's line should end %q:\n%s", "   covers all 30 rows", got)
	}
	for i := 0; i < 20; i++ {
		press(t, ws, 'j')
		got := totalsLines(frameOf(ws))
		if strings.Join(got, "\n") != strings.Join(first, "\n") {
			t.Fatalf("the totals moved with the cursor after %d presses:\n%s\nwas:\n%s", i+1, strings.Join(got, "\n"), strings.Join(first, "\n"))
		}
	}

	// No cut, no suffix.
	ws.height = 0
	for _, l := range totalsLines(frameOf(ws)) {
		if strings.Contains(l, "covers all") {
			t.Errorf("a table that is not cut carries no suffix:\n%s", l)
		}
	}
}

// Rows from an earlier read carry that read's time on the line; a fresh read
// drops it. The late frame and the disconnected frame both hold rows.
func TestWatchTotalsHeldRowsCarryAsOf(t *testing.T) {
	ws := totalsRig(t, meteredRows(3), "name,cpu,rss")
	const stamped = "totals (as of 12:00:00)   3 sessions"

	fresh := totalsLines(frameOf(ws))
	if len(fresh) == 0 || !strings.HasPrefix(fresh[0], "totals   3 sessions") {
		t.Fatalf("a fresh frame should carry an unstamped totals line, got %q", fresh)
	}

	ws.late = true
	late := totalsLines(frameOf(ws))
	if len(late) == 0 || !strings.HasPrefix(late[0], stamped) {
		t.Errorf("the late frame should carry %q, got %q", stamped, late)
	}
	ws.late = false

	ws.apply(watchData{err: fmt.Errorf("daemon gone"), at: watchT0.Add(time.Minute)})
	frame := frameOf(ws)
	i := strings.Index(frame, "last known state:")
	if i < 0 {
		t.Fatalf("fixture: the frame is not the disconnected one:\n%s", frame)
	}
	held := totalsLines(frame[i:])
	if len(held) == 0 || !strings.HasPrefix(held[0], stamped) {
		t.Errorf("the disconnected frame should carry %q under last known state, with the last good read's time, got %q:\n%s", stamped, held, frame)
	}

	ws.apply(watchData{sessions: meteredRows(3), header: ws.header, at: watchT0.Add(2 * time.Minute)})
	after := totalsLines(frameOf(ws))
	if len(after) == 0 || !strings.HasPrefix(after[0], "totals   3 sessions") || strings.Contains(after[0], "as of") {
		t.Errorf("a fresh read should drop the stamp, got %q", after)
	}
}

// rowLine is the table line of the row with that name.
func rowLine(frame, name string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, name+" ") {
			return l
		}
	}
	return ""
}

// With the clock the row cells read moved past a held row's reading, the
// total's fresh count falls with the table's ? cell. The seats are not quiet
// (a long quiet window), so a reading that expires reads ?, not 0.
func TestWatchTotalsHeldRateExpires(t *testing.T) {
	a := rateSession("a", 10, 0, 0)
	b := rateSession("b", 5.5, 9*time.Minute, 9*time.Minute)
	a.ActiveTicks.Window, b.ActiveTicks.Window = 30*time.Minute, 30*time.Minute
	ws := totalsRig(t, []api.Session{a, b}, "name,rate")
	ws.late = true // the rows are held

	line := func() string {
		got := totalsLines(frameOf(ws))
		if len(got) == 0 {
			t.Fatalf("the frame has no totals line:\n%s", frameOf(ws))
		}
		return got[0]
	}
	if frame := frameOf(ws); strings.Contains(rowLine(frame, "a"), "?") || strings.Contains(rowLine(frame, "b"), "?") {
		t.Fatalf("fixture: no cell should read ? yet:\n%s", frame)
	}
	if got := line(); !strings.Contains(got, "(2 fresh)") {
		t.Fatalf("both cells are fresh, so the count should be 2:\n%s\nframe:\n%s", got, frameOf(ws))
	}

	rateClock = func() time.Time { return rateNow.Add(2 * time.Minute) }
	frame := frameOf(ws)
	if !strings.Contains(rowLine(frame, "b"), "?") || strings.Contains(rowLine(frame, "a"), "?") {
		t.Fatalf("fixture: only row b's cell should read ? two minutes on:\n%s", frame)
	}
	if got := line(); !strings.Contains(got, "(1 fresh)") {
		t.Errorf("the count should fall with the cell that turned ?:\n%s", got)
	}
}

// The renderer's options carry the as-of time and the covered-row count, and
// the slots come in the architect's order: label, as-of, then the fields; the
// suffix last (design section 2.8).
func TestTotalsOptionSlots(t *testing.T) {
	rows := meteredRows(3)
	cols := columnsOrFatal(t, "name,cpu", nil)
	withRateClock(t)
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	got := strings.TrimRight(renderTotals(rows, cols, rateNow, totalsOpts{label: "alpha", asOf: at}), "\n")
	if want := "totals alpha (as of 12:00:00)   3 sessions   mCPU 300 (3)"; got != want {
		t.Errorf("label and as-of slots:\n got %q\nwant %q", got, want)
	}
	got = strings.TrimRight(renderTotals(rows, cols, rateNow, totalsOpts{covered: 3}), "\n")
	if want := "totals   3 sessions   mCPU 300 (3)   covers all 3 rows"; got != want {
		t.Errorf("covered suffix:\n got %q\nwant %q", got, want)
	}
	got = strings.TrimRight(renderTotals(rows, cols, rateNow, totalsOpts{}), "\n")
	if want := "totals   3 sessions   mCPU 300 (3)"; got != want {
		t.Errorf("no options:\n got %q\nwant %q", got, want)
	}
}

// The fan-out frame prints no totals line until its own ticket lands
// (aae-orc-onecx), which inverts this assertion.
func TestTotalsNotInTheFanOutFrameYet(t *testing.T) {
	r := newFanRig(t, time.Hour, time.Hour, "a", "b")
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.answer("a", 1, "a-one")
	r.answer("b", 1, "b-one")
	f := r.frame("both clusters' rows", func(f string) bool {
		return strings.Contains(f, "a-one") && strings.Contains(f, "b-one")
	})
	if len(totalsLines(f)) != 0 {
		t.Errorf("the fan-out frame must not carry a totals line yet:\n%s", f)
	}
}
