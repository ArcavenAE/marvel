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
