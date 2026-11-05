package goosewatch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A fleet suspend stops the wrapper and goose's group with SIGSTOP and resumes
// them with SIGCONT. These tests do the same to a wrapper they started: the
// test binary re-executes itself as the wrapper (reexec_test.go), with a fake
// goose as `sh -c`. They signal only the two process groups they started.

// freezeIdle is the idle limit every re-exec freeze test runs under.
const freezeIdle = 2 * time.Second

// freezeRun is what one frozen run looked like from outside.
type freezeRun struct {
	code       int
	stderrLast string
	stderrAll  string
	markerLast string
	start      time.Time
	contAt     time.Time
	end        time.Time
	stoppedAt  time.Time
	readyAt    time.Time
}

// runFrozen starts the wrapper over script with the given limits, waits for the
// ready file, sleeps freezeAfter, stops both groups for freezeFor, then resumes
// them and waits for the wrapper to end. freezeFor of zero is the control.
func runFrozen(t *testing.T, script string, wall, freezeAfter, freezeFor time.Duration) freezeRun {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "goose.pid")
	ready := filepath.Join(dir, "ready")
	errFile := filepath.Join(dir, "stderr.log")
	outFile := filepath.Join(dir, "stdout.log")
	full := `echo $$ > ` + pidFile + `; : > ` + ready + `; ` + script

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tok := newToken()
	out, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	cmd := exec.Command("/usr/bin/env", reexecEnv+"="+tok, exe, reexecFlag, tok, "wrapperlim", errFile, "1s", full, freezeIdle.String(), wall.String())
	cmd.Stdout = out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var r freezeRun
	r.start = time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wrapperPgid := cmd.Process.Pid
	kill := func() {
		_ = syscall.Kill(-wrapperPgid, syscall.SIGKILL)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(ready); err != nil {
		kill()
		t.Fatal("the fake goose never reported ready")
	}
	r.readyAt = time.Now()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		kill()
		t.Fatalf("read the fake goose's pid: %v", err)
	}
	goosePid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || goosePid <= 0 {
		kill()
		t.Fatalf("the fake goose's pid file reads %q, want a pid", b)
	}

	if freezeFor > 0 {
		time.Sleep(freezeAfter)
		if goosePid > 0 {
			_ = syscall.Kill(-goosePid, syscall.SIGSTOP)
		}
		_ = syscall.Kill(-wrapperPgid, syscall.SIGSTOP)
		r.stoppedAt = time.Now()
		time.Sleep(freezeFor)
		r.contAt = time.Now()
		_ = syscall.Kill(-wrapperPgid, syscall.SIGCONT)
		if goosePid > 0 {
			_ = syscall.Kill(-goosePid, syscall.SIGCONT)
		}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(60 * time.Second):
		kill()
		if goosePid > 0 {
			_ = syscall.Kill(-goosePid, syscall.SIGKILL)
		}
		t.Fatal("the wrapper did not end within 60s")
	}
	r.end = time.Now()
	if ee, ok := waitErr.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	}
	eb, _ := os.ReadFile(errFile)
	ob, _ := os.ReadFile(outFile)
	r.stderrAll = string(eb)
	r.stderrLast = lastLine(string(eb))
	r.markerLast = lastLine(string(ob))
	t.Logf("exit %d, stderr last %q, stdout last %q; ready to end %v; frozen %v; SIGCONT to end %v",
		r.code, r.stderrLast, r.markerLast, r.end.Sub(r.readyAt).Round(time.Millisecond),
		r.contAt.Sub(r.stoppedAt).Round(time.Millisecond), r.end.Sub(r.contAt).Round(time.Millisecond))
	return r
}

// Test 1. A run frozen for longer than Idle, with about half a second of idle
// behind it, is not ended at resume: the freeze is not idle time. It ends idle
// at least 1.2s after SIGCONT, when its own remaining idle has run.
func TestFreezeIsNotIdleTime(t *testing.T) {
	script := `printf '%s\n' '` + textLine + `'; sleep 60`
	r := runFrozen(t, script, 30*time.Second, 500*time.Millisecond, 4*time.Second)
	if r.code != ExitWatchdog || r.stderrLast != "watchdog: idle" {
		t.Fatalf("exit %d, stderr last %q, want an idle kill", r.code, r.stderrLast)
	}
	if got := r.end.Sub(r.contAt); got < 1200*time.Millisecond {
		t.Errorf("ended %v after SIGCONT, want at least 1.2s: the frozen 4s counted as idle", got)
	}
}

// Test 2. A goose that writes a line every 0.2s is not idle, and a freeze does
// not make it so. It is still running 1.5s after SIGCONT and ends with its own
// exit 0, the frozen span is named and is at least half the freeze, and the
// longest gap between lines is under half the freeze. At base this run survives
// by a race (its first write after SIGCONT lands before the monitor's first
// tick) and its max-gap counts the freeze (4.1 to 4.2s), so the max-gap bound is
// what fails there. The bounds are fractions of the freeze, not literals, and
// the frozen figure has no upper bound: a load stall beside the stop is excused
// with it.
func TestFreezeDoesNotKillASteadyWriter(t *testing.T) {
	const freeze = 4 * time.Second
	script := `i=0; while [ $i -lt 40 ]; do printf '%s\n' '` + textLine + `'; sleep 0.2; i=$((i+1)); done`
	r := runFrozen(t, script, 30*time.Second, time.Second, freeze)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("wrapper stderr = %q", r.stderrAll)
		}
	})
	if got := r.end.Sub(r.contAt); got < 1500*time.Millisecond {
		t.Errorf("ended %v after SIGCONT (exit %d, stderr last %q), want it still running at 1.5s: the freeze counted as idle",
			got, r.code, r.stderrLast)
	}
	if r.code != 0 {
		t.Errorf("exit = %d, want goose's own 0", r.code)
	}
	if strings.Contains(r.stderrAll, "watchdog: idle") || strings.Contains(r.stderrAll, "watchdog: wall-clock") {
		t.Errorf("stderr = %q, want no watchdog kill line", r.stderrAll)
	}
	frozen, ok := frozenOf(r.stderrAll)
	switch {
	case !ok:
		t.Errorf("stderr = %q, want a frozen line", r.stderrAll)
	case frozen < freeze/2:
		t.Errorf("frozen = %v, want at least %v: the freeze path did not take off most of the stop", frozen, freeze/2)
	}
	if gap := maxGapOf(t, r.stderrAll); gap >= freeze/2 {
		t.Errorf("max-gap = %v, want under %v: the freeze counted as a gap between lines", gap, freeze/2)
	}
}

// frozenOf reads the figure from the "watchdog: frozen 3.9s" line.
func frozenOf(stderr string) (time.Duration, bool) {
	for _, l := range strings.Split(stderr, "\n") {
		var secs float64
		if n, _ := fmt.Sscanf(l, "watchdog: frozen %fs", &secs); n == 1 {
			return time.Duration(secs * float64(time.Second)), true
		}
	}
	return 0, false
}

// maxGapOf reads the figure from the "watchdog: max-gap 0.2s lines 40" line.
func maxGapOf(t *testing.T, stderr string) time.Duration {
	t.Helper()
	for _, l := range strings.Split(stderr, "\n") {
		var gap float64
		var lines int
		if n, _ := fmt.Sscanf(l, "watchdog: max-gap %fs lines %d", &gap, &lines); n == 2 {
			return time.Duration(gap * float64(time.Second))
		}
	}
	t.Fatalf("no max-gap line in %q", stderr)
	return 0
}

// Test 3. A tool call is open, so idle is paused, and Wall is 5s. A 6s freeze
// is not wall time: the run ends by wall about 5s of running time from its
// start, not at SIGCONT.
func TestFreezeIsNotWallTime(t *testing.T) {
	script := `printf '%s\n' '` + reqLine + `'; sleep 60`
	r := runFrozen(t, script, 5*time.Second, time.Second, 6*time.Second)
	if r.code != ExitWatchdog || r.stderrLast != "watchdog: wall-clock" {
		t.Fatalf("exit %d, stderr last %q, want a wall-clock kill", r.code, r.stderrLast)
	}
	running := r.end.Sub(r.start) - r.contAt.Sub(r.stoppedAt)
	if running < 4500*time.Millisecond || running > 6500*time.Millisecond {
		t.Errorf("ended after %v of running time, want about 5s: the frozen 6s counted toward wall", running.Round(time.Millisecond))
	}
	if got := r.end.Sub(r.contAt); got < 1500*time.Millisecond {
		t.Errorf("ended %v after SIGCONT, want it still running at 1.5s", got)
	}
}

// stalledRun runs a silent fake goose in this process with the monitor's ticks
// and SIGCONTs under the test's control, so the order of a resume is fixed and
// needs no real stop. After firstTickAt it delivers one tick, which is on time
// when firstTickAt is under two intervals and late, and held, when it is more
// (a held tick excuses nothing: the next tick replaces what it left pending).
// It then holds the monitor for stall (nothing ticks, as in a stop), then
// delivers the first tick after the stop and a SIGCONT. That tick carries a value dueOffset
// after the tick before the stop, which is what a real ticker sends: the time
// the tick fell due inside the stop, not the time it is received.
func stalledRun(t *testing.T, script string, idle, firstTickAt, stall, dueOffset time.Duration) result {
	t.Helper()
	ticks := make(chan time.Time)
	conts := make(chan os.Signal)
	done := make(chan struct{})
	defer close(done)
	go func() {
		time.Sleep(firstTickAt)
		before := time.Now()
		select {
		case ticks <- before:
		case <-done:
			return
		}
		time.Sleep(stall)
		select {
		case ticks <- before.Add(dueOffset):
		case <-done:
			return
		}
		select {
		case conts <- syscall.SIGCONT:
		case <-done:
		}
	}()
	return runSh(t, script, time.Minute, idle, func(c *Config) {
		c.Ticks = ticks
		c.Conts = conts
	})
}

// A tick received after a stop is late whatever value it carries, so its expiry
// check is held until the SIGCONT, which excuses the stop from the last tick
// before it, less one interval. The tick here carries a due time 0.9s after the
// last tick, inside the 3s stop. Read as the clock, that value reads as on
// schedule: nothing is held and the excuse runs from the stale value, 1.1s of
// the 3s. The excuse here is 2.0s. The figure is printed to 0.1s.
func TestATickReceivedAfterAStopIsLateWhateverItsValueSays(t *testing.T) {
	t.Parallel()
	const (
		stall = 3 * time.Second
		idle  = 20 * time.Second // its tick is 1s, the cap
		// The bound sits between the two figures this feed produces: 2.0s when
		// lateness is read at receipt (stall less one tick), and 1.1s when the
		// tick's value is read as the clock (that less the 0.9s due offset),
		// measured 5 of 5 runs each. The midpoint leaves 0.5s on the passing side
		// for load on the first tick's receipt and 0.4s against the old reading.
		// Change the feed's timing and the bound has to be derived again.
		bound = 1500 * time.Millisecond
	)
	r := stalledRun(t, "sleep 5", idle, 300*time.Millisecond, stall, 900*time.Millisecond)
	frozen, ok := frozenOf(r.stderr)
	if !ok {
		t.Fatalf("stderr = %q, want a frozen line", r.stderr)
	}
	if frozen < bound {
		t.Errorf("frozen = %v, want at least %v: the excuse ran from the tick's due time, not from the last tick before the stop (stderr %q)",
			frozen, bound, r.stderr)
	}
}

// A seat with most of its idle spent when it was stopped survives a resume
// whose first tick is processed before the SIGCONT: the tick is late, so its
// expiry check is held, and the SIGCONT then takes the stop off the idle clock.
// At receipt time the idle clock reads 4.2s of a 2s limit, so a check that ran
// before the SIGCONT would kill it. The seat's own first tick, at 1.2s, is late
// against the 100ms interval and is held too; the post-stop tick replaces what
// it left pending, so the SIGCONT excuses the 3s stop less one interval.
func TestASeatWithMostOfItsIdleSpentSurvivesATickFirstResume(t *testing.T) {
	t.Parallel()
	r := stalledRun(t, "sleep 5", 2*time.Second, 1200*time.Millisecond, 3*time.Second, 90*time.Millisecond)
	if r.code != 0 {
		t.Errorf("exit = %d, want goose's own 0 (stderr %q)", r.code, r.stderr)
	}
	if strings.Contains(r.stderr, "watchdog: idle") {
		t.Errorf("stderr = %q, want no idle kill: the stop was counted as idle", r.stderr)
	}
	if _, ok := frozenOf(r.stderr); !ok {
		t.Errorf("stderr = %q, want a frozen line", r.stderr)
	}
}
