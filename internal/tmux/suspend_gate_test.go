package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeTmux is a driver whose tmux binary records each invocation in a file, so
// a test can tell whether a process was started.
func fakeTmux(t *testing.T) (*Driver, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Driver{binary: bin}, log
}

func started(log string) bool {
	_, err := os.Stat(log)
	return err == nil
}

// While the gate says suspended, no tmux process is started: the call fails with
// a sentinel that is both ErrClusterSuspended and, so every outage branch treats
// it as "tmux did not answer" and never as "the pane is gone", ErrTmuxTimeout.
// Its text says what to do and never says timeout.
func TestSuspendGateStopsEveryExecWithAnOutageShapedSentinel(t *testing.T) {
	d, log := fakeTmux(t)
	suspended := true
	d.SetSuspendGate(func() bool { return suspended })

	for name, run := range map[string]func() error{
		"Output":         func() error { _, err := d.cmd("list-sessions").Output(); return err },
		"CombinedOutput": func() error { _, err := d.cmd("list-sessions").CombinedOutput(); return err },
		"Run":            func() error { return d.cmd("list-sessions").Run() },
		"a public method": func() error {
			_, err := d.ListSessions()
			return err
		},
	} {
		err := run()
		if !errors.Is(err, ErrClusterSuspended) {
			t.Errorf("%s: %v, want ErrClusterSuspended", name, err)
		}
		if !errors.Is(err, ErrTmuxTimeout) {
			t.Errorf("%s: %v, want it to satisfy ErrTmuxTimeout too", name, err)
		}
		if name != "a public method" && err != nil && err.Error() != "cluster suspended; run marvel resume" {
			t.Errorf("%s: text %q, want the sentinel's own text", name, err.Error())
		}
	}
	if started(log) {
		t.Fatal("a tmux process was started while the gate was up")
	}
	if got := ErrClusterSuspended.Error(); got != "cluster suspended; run marvel resume" {
		t.Errorf("text = %q", got)
	}

	suspended = false
	if _, err := d.cmd("list-sessions").Output(); err != nil {
		t.Fatalf("with the gate down: %v", err)
	}
	if !started(log) {
		t.Error("the call did not run once the gate was down")
	}
}

// Unguarded is the one bypass: the same tmux, with the gate skipped. The suspend
// and resume procedure and the collector's reads use it, because the hold is up
// while they run.
func TestUnguardedSkipsTheGate(t *testing.T) {
	d, log := fakeTmux(t)
	d.SetSuspendGate(func() bool { return true })
	u := d.Unguarded()
	if _, err := u.cmd("display-message", "-p", "#{pid}").Output(); err != nil {
		t.Fatalf("unguarded call: %v", err)
	}
	if !started(log) {
		t.Error("the unguarded call did not start tmux")
	}
	// The original stays guarded, and a view of a view stays unguarded.
	if _, err := d.cmd("list-sessions").Output(); !errors.Is(err, ErrClusterSuspended) {
		t.Errorf("the guarded driver was opened by Unguarded: %v", err)
	}
	if _, err := u.Unguarded().cmd("list-sessions").Output(); err != nil {
		t.Errorf("a view of an unguarded view: %v", err)
	}
}

// No gate set is the old behaviour.
func TestNoGateRunsAsBefore(t *testing.T) {
	d, log := fakeTmux(t)
	if _, err := d.cmd("list-sessions").Output(); err != nil {
		t.Fatal(err)
	}
	if !started(log) {
		t.Error("no gate set, and tmux did not run")
	}
}
