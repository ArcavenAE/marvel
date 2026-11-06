package goosewatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The freeze clock with fixed times: no process and no sleep.

var fcT0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

const fcInterval = 100 * time.Millisecond

// A late tick that no SIGCONT confirms is evaluated on the next tick, so a
// starved wrapper is still bounded and the stall counts as it always did.
func TestLateTickWithNoSIGCONTIsEvaluatedOnTheNext(t *testing.T) {
	f := newFreezeClock(fcInterval, fcT0)
	if !f.tick(fcT0.Add(time.Second)) {
		t.Fatal("a tick 900ms late was not held")
	}
	if f.pending != 900*time.Millisecond {
		t.Errorf("pending = %v, want 900ms", f.pending)
	}
	if f.tick(fcT0.Add(time.Second + fcInterval)) {
		t.Error("the next, on-time tick was held too")
	}
	if f.pending != 0 {
		t.Errorf("pending = %v after one unconfirmed tick, want it cleared", f.pending)
	}
}

// A late tick and then a SIGCONT excuse the pending gap.
func TestSIGCONTAfterALateTickExcusesThePendingGap(t *testing.T) {
	f := newFreezeClock(fcInterval, fcT0)
	f.tick(fcT0.Add(time.Second))
	if got := f.cont(fcT0.Add(time.Second + time.Millisecond)); got != 900*time.Millisecond {
		t.Errorf("excused %v, want the pending 900ms", got)
	}
	if f.pending != 0 || !f.lastTick.Equal(fcT0.Add(time.Second+time.Millisecond)) {
		t.Errorf("state after the SIGCONT: pending %v, lastTick %v", f.pending, f.lastTick)
	}
}

// A SIGCONT that comes before the stopped goroutine's first tick still excuses
// the gap since the last tick, less one interval.
func TestSIGCONTBeforeTheFirstLateTickStillExcuses(t *testing.T) {
	f := newFreezeClock(fcInterval, fcT0)
	if got := f.cont(fcT0.Add(time.Second)); got != 900*time.Millisecond {
		t.Errorf("excused %v, want 900ms", got)
	}
	// The tick that follows is on time against the SIGCONT, so it is evaluated.
	if f.tick(fcT0.Add(time.Second + fcInterval)) {
		t.Error("the tick after the SIGCONT was held")
	}
}

// A SIGCONT with no gap excuses nothing: a stray one does not forgive time.
func TestSIGCONTWithNoGapExcusesNothing(t *testing.T) {
	f := newFreezeClock(fcInterval, fcT0)
	f.tick(fcT0.Add(fcInterval))
	if got := f.cont(fcT0.Add(fcInterval + 50*time.Millisecond)); got != 0 {
		t.Errorf("excused %v, want nothing", got)
	}
}

// A tick that is late by no more than an interval is not a freeze.
func TestATickLateByOneIntervalIsLoadNotAFreeze(t *testing.T) {
	f := newFreezeClock(fcInterval, fcT0)
	if f.tick(fcT0.Add(2 * fcInterval)) {
		t.Error("a tick late by exactly one interval was held")
	}
}

// excuse moves the idle clock and the last-line time forward, never past now,
// and keeps a total; the longest gap then leaves the freeze out.
func TestExcuseMovesTheClocksAndLeavesTheFreezeOutOfTheGap(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(500*time.Millisecond), nil)
	now := fcT0.Add(4500 * time.Millisecond)
	tr.excuse(now, 4*time.Second)

	tr.mu.Lock()
	idle, last, total := tr.idleSince, tr.lastLine, tr.frozen
	tr.mu.Unlock()
	want := fcT0.Add(4500 * time.Millisecond)
	if !idle.Equal(want) || !last.Equal(want) {
		t.Errorf("idleSince %v, lastLine %v, want both %v", idle, last, want)
	}
	if total != 4*time.Second {
		t.Errorf("total = %v, want 4s", total)
	}
	tr.line(now.Add(200*time.Millisecond), nil)
	tr.mu.Lock()
	gap := tr.maxGap
	tr.mu.Unlock()
	if gap >= time.Second {
		t.Errorf("max-gap = %v, want the 4s freeze left out", gap)
	}

	// A span longer than the time since the clocks were last set is capped at now.
	tr2 := newTracker(fcT0)
	tr2.excuse(fcT0.Add(time.Second), 10*time.Second)
	tr2.mu.Lock()
	defer tr2.mu.Unlock()
	if !tr2.idleSince.Equal(fcT0.Add(time.Second)) {
		t.Errorf("idleSince = %v, want it capped at now", tr2.idleSince)
	}
}

// The frozen line appears only when something was frozen, sits just before the
// max-gap line, and leaves the last stderr line as it was.
func TestFrozenLineAppearsOnlyWhenSomethingWasFrozen(t *testing.T) {
	for _, frozen := range []time.Duration{0, 4 * time.Second} {
		path := filepath.Join(t.TempDir(), "stderr.log")
		s := newStderrSink(Config{StderrFile: path})
		if err := s.open(); err != nil {
			t.Fatal(err)
		}
		tr := newTracker(fcT0)
		tr.line(fcT0.Add(time.Second), nil)
		if frozen > 0 {
			tr.excuse(fcT0.Add(5*time.Second), frozen)
		}
		s.finish(tr, "watchdog: idle")
		s.close()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if got := lines[len(lines)-1]; got != "watchdog: idle" {
			t.Errorf("frozen %v: last line = %q, want it unchanged", frozen, got)
		}
		has := strings.Contains(string(b), "watchdog: frozen ")
		if has != (frozen > 0) {
			t.Errorf("frozen %v: frozen line present = %v in %q", frozen, has, string(b))
		}
		if frozen > 0 {
			fi, mi := -1, -1
			for i, l := range lines {
				if strings.HasPrefix(l, "watchdog: frozen ") {
					fi = i
				}
				if strings.HasPrefix(l, "watchdog: max-gap ") {
					mi = i
				}
			}
			if fi < 0 || mi != fi+1 || lines[fi] != "watchdog: frozen 4.0s" {
				t.Errorf("lines = %q, want %q immediately before the max-gap line", lines, "watchdog: frozen 4.0s")
			}
		}
	}
}

// A tick received after a stop, before the SIGCONT: it is late by the whole
// stop less one interval, so its expiry check is skipped, and the SIGCONT then
// excuses that gap, which runs from the last tick before the stop. This is the
// order a real resume produces, because the ticker's first tick is processed in
// the same instant as the signal.
func TestATickReceivedAfterAStopHoldsTheExpiryUntilTheSIGCONT(t *testing.T) {
	f := newFreezeClock(fcInterval, fcT0)
	// The last tick before the stop.
	f.tick(fcT0)
	received := fcT0.Add(4 * time.Second)
	if !f.tick(received) {
		t.Fatal("a tick received 3.9s late was not held, so its expiry check would run before the SIGCONT")
	}
	want := 4*time.Second - fcInterval
	if f.pending != want {
		t.Errorf("pending = %v, want %v from the last tick before the stop, less one interval", f.pending, want)
	}
	if got := f.cont(received.Add(500 * time.Microsecond)); got != want {
		t.Errorf("excused %v, want %v", got, want)
	}
}

// The longest gap with a stop in it. A stop of 3.9s begins just after the tick
// at T0 and ends at T0+4s; the wrapper reads nothing in it, so the first line
// read after the resume carries the whole stop as its gap, and the SIGCONT is
// handled a moment later. interval is 100ms, so s, the last tick before the
// stop, is now less frozen less one interval.
func maxGapOfTracker(tr *tracker) time.Duration {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.maxGap
}

// One line read after the resume, before the SIGCONT: its gap holds the stop,
// and the SIGCONT takes the stop back off it.
func TestExcuseCorrectsTheGapOfALineReadBeforeTheSIGCONT(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(4*time.Second), nil)
	if got := maxGapOfTracker(tr); got != 4*time.Second {
		t.Fatalf("max-gap = %v before the SIGCONT, want the 4s the line read", got)
	}
	tr.excuse(fcT0.Add(4*time.Second+time.Millisecond), 3900*time.Millisecond)
	if got := maxGapOfTracker(tr); got != 100*time.Millisecond {
		t.Errorf("max-gap = %v, want 100ms: the 3.9s stop taken off the 4s gap", got)
	}
}

// Two lines read after the resume: the first carries the stop and the second a
// small gap. The max is the first's, and the SIGCONT takes the stop off it.
func TestExcuseCorrectsTheMaxWhenItIsTheFirstOfTwoLinesReadBeforeTheSIGCONT(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(4*time.Second), nil)
	tr.line(fcT0.Add(4200*time.Millisecond), nil)
	tr.excuse(fcT0.Add(4300*time.Millisecond), 4100*time.Millisecond)
	if got := maxGapOfTracker(tr); got != 200*time.Millisecond {
		t.Errorf("max-gap = %v, want 200ms: the second line's gap, the first's stop taken off", got)
	}
}

// A max that does not overlap the stop is left alone.
func TestExcuseLeavesAMaxOutsideTheStop(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(10*time.Second), nil) // a real 10s gap, before the stop
	tr.line(fcT0.Add(14*time.Second), nil) // the first line after a 3.9s stop
	tr.excuse(fcT0.Add(14*time.Second+time.Millisecond), 3900*time.Millisecond)
	if got := maxGapOfTracker(tr); got != 10*time.Second {
		t.Errorf("max-gap = %v, want the 10s gap that held no stop", got)
	}
}

// A real gap after the stop's own, and smaller than the stop but larger than
// what is left of it, is kept as the max.
func TestExcuseKeepsALargerRealGapReadAfterTheStop(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(4*time.Second), nil) // holds the stop
	tr.line(fcT0.Add(7*time.Second), nil) // a real 3s gap
	tr.excuse(fcT0.Add(7100*time.Millisecond), 3900*time.Millisecond)
	if got := maxGapOfTracker(tr); got != 3*time.Second {
		t.Errorf("max-gap = %v, want the real 3s gap", got)
	}
}

// The SIGCONT handled first, then the line: the line's time is read against a
// last-line time the stop was already taken off, so no correction is needed.
func TestAGapReadAfterTheSIGCONTLeavesTheStopOut(t *testing.T) {
	tr := newTracker(fcT0)
	tr.excuse(fcT0.Add(4*time.Second), 3900*time.Millisecond)
	tr.line(fcT0.Add(4100*time.Millisecond), nil)
	if got := maxGapOfTracker(tr); got != 200*time.Millisecond {
		t.Errorf("max-gap = %v, want 200ms (4.1s less the 3.9s shifted start)", got)
	}
}

// A new max starts maxAfter over, so a gap from before it cannot outlive it.
func TestANewMaxResetsTheLongestGapSince(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(300*time.Millisecond), nil) // gap 300ms, the max
	tr.line(fcT0.Add(500*time.Millisecond), nil) // gap 200ms, since the max
	tr.mu.Lock()
	if tr.maxAfter != 200*time.Millisecond {
		t.Errorf("maxAfter = %v, want 200ms", tr.maxAfter)
	}
	tr.mu.Unlock()
	tr.line(fcT0.Add(1000*time.Millisecond), nil) // gap 500ms, a new max
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.maxBefore != 300*time.Millisecond || tr.maxAfter != 0 || tr.maxGap != 500*time.Millisecond {
		t.Errorf("maxBefore %v maxAfter %v maxGap %v, want 300ms, 0, 500ms", tr.maxBefore, tr.maxAfter, tr.maxGap)
	}
}

// A line read in the last interval before the stop leaves the idle shift in
// place: the stop is still taken off the idle clock, so a seat that wrote just
// before it is not killed idle after the resume.
func TestALineJustBeforeTheStopStillGetsTheIdleShift(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(30*time.Millisecond), nil) // read after the last tick, before the stop
	now := fcT0.Add(4 * time.Second)
	tr.excuse(now, 3900*time.Millisecond)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if got := now.Sub(tr.idleSince); got > 200*time.Millisecond {
		t.Errorf("idle reads %v at the SIGCONT, want the 3.9s stop taken off it", got)
	}
}

// A 10s gap that ended 30ms before the stop held none of it, and stays 10s.
func TestAGapEndingJustBeforeTheStopIsNotCorrected(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(10*time.Second+30*time.Millisecond), nil) // a real 10s gap, then the stop
	tr.excuse(fcT0.Add(14*time.Second+30*time.Millisecond), 3900*time.Millisecond)
	if got := maxGapOfTracker(tr); got != 10*time.Second+30*time.Millisecond {
		t.Errorf("max-gap = %v, want the real 10.03s gap left as it was", got)
	}
}

// An earlier line read just before the stop and a later one read after the
// resume: the gap between them holds the stop and is corrected, as the steady
// writer's is about half the time, when its last line before the stop falls
// inside the last interval.
func TestExcuseCorrectsAGapWhoseEarlierLineWasReadJustBeforeTheStop(t *testing.T) {
	tr := newTracker(fcT0)
	tr.line(fcT0.Add(70*time.Millisecond), nil) // read 30ms before the stop at T0+100ms
	tr.line(fcT0.Add(4*time.Second), nil)       // the first line read after the resume
	tr.excuse(fcT0.Add(4001*time.Millisecond), 3901*time.Millisecond)
	if got := maxGapOfTracker(tr); got != 70*time.Millisecond {
		t.Errorf("max-gap = %v, want 70ms: the stop taken off the 3.93s gap, leaving the earlier 70ms", got)
	}
}
