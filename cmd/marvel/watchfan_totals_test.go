package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The totals block of the watch across clusters: docs/design/cluster-rollup-header.md,
// section 2.5, 2.8, 2.9 and ticket T4 (aae-orc-onecx).

func anyFrame(string) bool { return true }

// blockLines is the totals lines of one cluster in a frame: the lines whose
// lead is "totals <label>", continuation lines of a wrapped line included.
func blockLines(frame, label string) []string {
	var out []string
	for _, l := range totalsLines(frame) {
		if l == "totals "+label || strings.HasPrefix(l, "totals "+label+" ") {
			out = append(out, l)
		}
	}
	return out
}

func answeredBoth(r *fanRig) string {
	r.t.Helper()
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.answer("a", 1, "a-one")
	r.answer("b", 1, "b-one")
	return r.frame("both clusters' rows", func(f string) bool {
		return strings.Contains(f, "a-one") && strings.Contains(f, "b-one")
	})
}

// The frame carries one totals line per cluster, under the status lines and
// above the merged table. (It replaces the hold-out of T2, which pinned that
// the fan-out frame carried none.)
func TestTotalsInTheFanOutFrame(t *testing.T) {
	r := newFanRig(t, time.Hour, time.Hour, "a", "b")
	f := answeredBoth(r)
	a, b := blockLines(f, "a"), blockLines(f, "b")
	if len(a) != 1 || !strings.HasPrefix(a[0], "totals a   1 session") {
		t.Errorf("cluster a's line = %q, want one starting %q:\n%s", a, "totals a   1 session", f)
	}
	if len(b) != 1 || !strings.HasPrefix(b[0], "totals b   1 session") {
		t.Errorf("cluster b's line = %q, want one starting %q:\n%s", b, "totals b   1 session", f)
	}
	status, line, table := strings.Index(f, "cluster b: answered"), strings.Index(f, "totals a"), strings.Index(f, "AGENT NAME")
	if status < 0 || table < 0 || status >= line || line >= table {
		t.Errorf("the block should sit under the status lines and above the table (at %d, %d, %d):\n%s", status, line, table, f)
	}
}

// The block counts against the screen height, so the frame never grows past
// the terminal and the last row of the table still fits when the cursor is on
// it.
func TestWatchFanTotalsCountedAsUsed(t *testing.T) {
	const height = 14
	r := newFanRigWith(t, 200, height, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b"}})
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	var as, bs []string
	for i := 0; i < 10; i++ {
		as = append(as, fmt.Sprintf("a%02d", i))
		bs = append(bs, fmt.Sprintf("b%02d", i))
	}
	r.answer("a", 1, as...)
	r.answer("b", 1, bs...)
	r.frame("both clusters' rows", func(f string) bool {
		return strings.Contains(f, "cluster a: answered: 10") && strings.Contains(f, "cluster b: answered: 10")
	})
	var frame string
	for i := 0; i < 20; i++ { // the first press selects the first row
		r.keys <- 'j'
		frame = r.frame("a frame after a key", anyFrame)
	}
	if len(blockLines(frame, "a")) == 0 || len(blockLines(frame, "b")) == 0 {
		t.Errorf("the frame should carry both clusters' lines:\n%s", frame)
	}
	if n := frameHeight(frame); n > height {
		t.Errorf("frame is %d lines, want at most %d:\n%s", n, height, frame)
	}
	if !strings.Contains(frame, "b09") {
		t.Errorf("the last row, under the cursor, should be on screen:\n%s", frame)
	}
	if !strings.Contains(frame, "showing rows") {
		t.Errorf("fixture: the table should be cut at height %d:\n%s", height, frame)
	}
}

// A repeat address is not polled and its rows are skipped, so it gets no line.
func TestWatchFanTotalsRepeatHasNoLine(t *testing.T) {
	r := newFanRigWith(t, 200, 0, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b", repeatOf: "a"}})
	r.waitStarted("a", 1)
	r.answer("a", 1, "a-one")
	f := r.frame("a's rows", contains("a-one"))
	if got := totalsLines(f); len(got) != 1 || !strings.HasPrefix(got[0], "totals a   1 session") {
		t.Errorf("want exactly cluster a's line and none for the repeat address b, got %q:\n%s", got, f)
	}
}

// A cluster that failed after a good read shows its held rows, so its line
// carries the time of that read; a cluster that answered does not; the next
// good poll drops the stamp.
func TestWatchFanTotalsHeldClusterCarriesAsOf(t *testing.T) {
	r := newFanRig(t, time.Hour, time.Hour, "a", "b")
	r.setNow(watchT0.Add(time.Second))
	answeredBoth(r)

	r.setNow(watchT0.Add(5 * time.Second))
	r.tick()
	r.waitStarted("a", 2)
	r.waitStarted("b", 2)
	r.gate(fanKey{"a", 2}) <- fanFetch{rec: clusterRecord{Cluster: "a", Outcome: outcomeUnreachable, Cause: "connection refused"}}
	r.answer("b", 2, "b-one")
	f := r.frame("a held and b fresh", func(f string) bool {
		return strings.Contains(f, "cluster a: unreachable") && strings.Contains(f, "cluster b: answered: 1 sessions, as of 12:00:05")
	})
	a, b := blockLines(f, "a"), blockLines(f, "b")
	if len(a) != 1 || !strings.HasPrefix(a[0], "totals a (as of 12:00:01)   1 session") {
		t.Errorf("held cluster a's line = %q, want one starting %q:\n%s", a, "totals a (as of 12:00:01)   1 session", f)
	}
	if len(b) != 1 || !strings.HasPrefix(b[0], "totals b   1 session") || strings.Contains(b[0], "as of") {
		t.Errorf("fresh cluster b's line = %q, want one starting %q and no stamp:\n%s", b, "totals b   1 session", f)
	}

	r.setNow(watchT0.Add(9 * time.Second))
	r.tick()
	r.waitStarted("a", 3)
	r.waitStarted("b", 3)
	r.answer("a", 3, "a-one")
	r.answer("b", 3, "b-one")
	f = r.frame("a answering again", func(f string) bool {
		return strings.Contains(f, "cluster a: answered: 1 sessions, as of 12:00:09")
	})
	if a := blockLines(f, "a"); len(a) != 1 || !strings.HasPrefix(a[0], "totals a   1 session") || strings.Contains(a[0], "as of") {
		t.Errorf("a good poll should drop the stamp, got %q:\n%s", a, f)
	}
}

// On the default view the fit draws columns the operator never listed, and each
// cluster's line carries a field for each summable one it drew and none for a
// column the width dropped (design section 2.1; the T1 defect).
func TestWatchFanTotalsCarryTheDrawnColumns(t *testing.T) {
	for _, c := range []struct {
		width int
		drawn []string
		not   []string
	}{
		{120, []string{"TOUT", "RATE"}, []string{"mCPU", "RSS"}},
		{200, []string{"mCPU", "RSS", "TOUT", "RATE"}, nil},
	} {
		r := newFanRigWith(t, c.width, 0, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b"}})
		f := answeredBoth(r)
		table := frameLineHolding(f, "AGENT NAME")
		for _, label := range []string{"a", "b"} {
			line := strings.Join(blockLines(f, label), " ")
			if line == "" || table == "" {
				t.Errorf("width %d: cluster %s's line and the table should both print:\n%s", c.width, label, f)
				continue
			}
			for _, h := range c.drawn {
				if !strings.Contains(table, h) {
					t.Errorf("width %d: fixture: the table should draw %s:\n%s", c.width, h, table)
				}
				if !strings.Contains(line, h+" ") {
					t.Errorf("width %d: the table draws %s but cluster %s's line has no field for it:\n%s", c.width, h, label, line)
				}
			}
			for _, h := range c.not {
				if strings.Contains(table, h) || strings.Contains(line, h+" ") {
					t.Errorf("width %d: %s is not drawn, so cluster %s's line must not carry it:\ntable: %s\nline: %s", c.width, h, label, table, line)
				}
			}
		}
	}
}

// sameDaemon makes every cluster of the rig read as one daemon.
func sameDaemon(cfg *fanWatchConfig) {
	cfg.keyOf = func(string, string) string { return "one-daemon" }
}

// answerOf releases a cluster's first fetch with the rows named, the outcome given and the one
// daemon stamp the same-daemon fold needs.
func (r *fanRig) answerOf(c string, outcome outcome, names ...string) {
	var rows []api.Session
	for _, n := range names {
		rows = append(rows, api.Session{Name: n, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning})
	}
	cnt := len(rows)
	r.gate(fanKey{c, 1}) <- fanFetch{
		rec:         clusterRecord{Cluster: c, Outcome: outcome, Sessions: &cnt},
		rows:        rows,
		fingerprint: "fp",
		stamp:       daemonStamp{PID: 4242, StartedAt: watchT0},
		stampOK:     true,
	}
}

// Two names that reach one daemon are one load: the member gets no line, its
// kept rows count on the leader's line, which names it (design section 2.5; the
// grammar of the one-shot block).
func TestWatchFanTotalsFoldSameDaemon(t *testing.T) {
	r := newFanRigWith(t, 200, 0, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b"}}, sameDaemon)
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.answerOf("a", outcomeAnswered, "a-one", "shared")
	r.answerOf("b", outcomeAnswered, "shared", "b-only")
	f := r.frame("both clusters answered and folded", func(f string) bool {
		return strings.Contains(f, "cluster b: same_daemon") && strings.Contains(f, "b-only")
	})
	a := blockLines(f, "a")
	if len(a) != 1 || !strings.HasPrefix(a[0], "totals a (+ b, same daemon)   3 sessions") {
		t.Errorf("the leader's line = %q, want one starting %q:\n%s", a, "totals a (+ b, same daemon)   3 sessions", f)
	}
	if b := blockLines(f, "b"); len(b) != 0 {
		t.Errorf("the folded member gets no line, got %q:\n%s", b, f)
	}
}

// A starting cluster may not have returned every row, so its line says so; and
// a starting member folded into an answered leader marks the leader's line, the
// mark taken from the fold's own marker because the fold overwrites the
// member's outcome.
func TestWatchFanTotalsStartingMarked(t *testing.T) {
	r := newFanRigWith(t, 200, 0, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b"}, {name: "c"}}, func(cfg *fanWatchConfig) {
		cfg.keyOf = func(c, _ string) string {
			if c == "c" {
				return ""
			}
			return "one-daemon"
		}
	})
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.waitStarted("c", 1)
	r.answerOf("a", outcomeAnswered, "a-one")
	r.answerOf("b", outcomeStarting, "b-only")
	r.answerOf("c", outcomeStarting, "c-one")
	f := r.frame("all three read", func(f string) bool {
		return strings.Contains(f, "cluster b: same_daemon") && strings.Contains(f, "c-one")
	})
	if a := blockLines(f, "a"); len(a) != 1 || !strings.HasPrefix(a[0], "totals a (+ b, same daemon) (starting)   2 sessions") {
		t.Errorf("the leader's line = %q, want one starting %q:\n%s", a, "totals a (+ b, same daemon) (starting)   2 sessions", f)
	}
	if c := blockLines(f, "c"); len(c) != 1 || !strings.HasPrefix(c[0], "totals c (starting)   1 session") {
		t.Errorf("a starting cluster's line = %q, want one starting %q:\n%s", c, "totals c (starting)   1 session", f)
	}
}

// When the table is cut, each line ends with the count of its own cluster's
// rows, and the line that is counted in the height is the line drawn. A table
// that is not cut carries no suffix.
func TestWatchFanTotalsCutSuffixOwnCount(t *testing.T) {
	const height = 14
	fill := func(r *fanRig) {
		r.waitStarted("a", 1)
		r.waitStarted("b", 1)
		var as, bs []string
		for i := 0; i < 10; i++ {
			as = append(as, fmt.Sprintf("a%02d", i))
		}
		for i := 0; i < 6; i++ {
			bs = append(bs, fmt.Sprintf("b%02d", i))
		}
		r.answer("a", 1, as...)
		r.answer("b", 1, bs...)
	}
	both := func(f string) bool {
		return strings.Contains(f, "cluster a: answered: 10") && strings.Contains(f, "cluster b: answered: 6")
	}

	cut := newFanRigWith(t, 200, height, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b"}})
	fill(cut)
	f := cut.frame("both clusters' rows", both)
	if a := blockLines(f, "a"); len(a) == 0 || !strings.HasSuffix(a[len(a)-1], "   covers all 10 rows") {
		t.Errorf("cluster a's line should end %q, got %q:\n%s", "   covers all 10 rows", a, f)
	}
	if b := blockLines(f, "b"); len(b) == 0 || !strings.HasSuffix(b[len(b)-1], "   covers all 6 rows") {
		t.Errorf("cluster b's line should end %q, got %q:\n%s", "   covers all 6 rows", b, f)
	}
	if n := frameHeight(f); n > height {
		t.Errorf("frame is %d lines, want at most %d:\n%s", n, height, f)
	}
	if !strings.Contains(f, "showing rows") {
		t.Errorf("fixture: the table should be cut at height %d:\n%s", height, f)
	}

	whole := newFanRigWith(t, 200, 0, time.Hour, time.Hour, []fanWatchCluster{{name: "a"}, {name: "b"}})
	fill(whole)
	f = whole.frame("both clusters' rows", both)
	if strings.Contains(f, "covers all") {
		t.Errorf("a table that is not cut carries no suffix:\n%s", f)
	}
}

// A cluster whose fetch is past the stale interval holds its last good rows, so
// its line carries that read's time, as a failed cluster's does; its answer
// drops the stamp.
func TestWatchFanTotalsLateClusterCarriesAsOf(t *testing.T) {
	r := newFanRig(t, 10*time.Millisecond, time.Hour, "a", "b")
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)
	r.setNow(watchT0.Add(time.Second))
	r.answer("a", 1, "a-one")
	r.answer("b", 1, "b-one")
	r.frame("both clusters' rows", func(f string) bool {
		return strings.Contains(f, "a-one") && strings.Contains(f, "b-one")
	})

	r.setNow(watchT0.Add(11 * time.Second))
	r.tick()
	r.waitStarted("a", 2)
	r.waitStarted("b", 2)
	r.answer("b", 2, "b-one")
	var sawStale, sawResult bool
	r.waitEvent("a's stale mark and b's second result", func(e fanEvent) bool {
		sawStale = sawStale || (e.Kind == "stale" && e.Cluster == "a" && e.Applied)
		sawResult = sawResult || (e.Kind == "result" && e.Cluster == "b" && e.Seq == 2 && e.Applied)
		return sawStale && sawResult
	})
	f := r.redraw()
	if a := blockLines(f, "a"); len(a) != 1 || !strings.HasPrefix(a[0], "totals a (as of 12:00:01)   1 session") {
		t.Errorf("late cluster a's line = %q, want one starting %q:\n%s", a, "totals a (as of 12:00:01)   1 session", f)
	}
	if b := blockLines(f, "b"); len(b) != 1 || strings.Contains(b[0], "as of") {
		t.Errorf("answering cluster b's line = %q, want one with no stamp:\n%s", b, f)
	}

	r.answer("a", 2, "a-one")
	f = r.frame("a answering again", contains("cluster a: answered: 1 sessions, as of 12:00:11"))
	if a := blockLines(f, "a"); len(a) != 1 || strings.Contains(a[0], "as of") {
		t.Errorf("an answer should drop the stamp, got %q:\n%s", a, f)
	}
}
