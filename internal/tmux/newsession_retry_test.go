package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seqFake is a tmux whose has-session and new-session answers follow a script
// by call number. has and mk are shell fragments run with n set to the call
// number (1 for the first); each ends in an exit. Every call is logged.
type seqFake struct {
	log string
}

func newSeqFake(t *testing.T, has, mk string, procs procList) (*seqFake, *Driver) {
	t.Helper()
	dir := t.TempDir()
	f := &seqFake{log: filepath.Join(dir, "calls")}
	body := "#!/bin/sh\n" +
		"echo \"$*\" >> " + f.log + "\n" +
		"case \"$*\" in\n" +
		"*has-session*) n=$(grep -c has-session " + f.log + ")\n" + has + "\n;;\n" +
		"*new-session*) n=$(grep -c new-session " + f.log + ")\n" + mk + "\n;;\n" +
		"*) exit 0 ;;\n" +
		"esac\n"
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return f, &Driver{binary: script, socket: "unit", procs: procs}
}

func (f *seqFake) calls(word string) int {
	b, _ := os.ReadFile(f.log)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, word) {
			n++
		}
	}
	return n
}

func shortBound(t *testing.T, d time.Duration) {
	t.Helper()
	old := startupBound
	startupBound = d
	t.Cleanup(func() { startupBound = old })
}

const (
	noSession = `echo "can't find session: s" >&2; exit 1`
	exitedMsg = `echo "server exited unexpectedly" >&2; exit 1`
)

// new-session reached a server that was shutting down: the whole body runs
// again, check first, and the second pass creates the session.
func TestNewSessionRetriesTheWholeBodyAfterAShutdownRace(t *testing.T) {
	// Room for four process spawns on a loaded host; the bound's value is
	// pinned elsewhere.
	shortBound(t, 30*time.Second)
	f, d := newSeqFake(t, noSession, `if [ "$n" = 1 ]; then `+exitedMsg+`; fi; exit 0`, procLines())
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if h, m := f.calls("has-session"), f.calls("new-session"); h != 2 || m != 2 {
		t.Errorf("has-session ran %d times and new-session %d times, want 2 and 2: the check runs again before the second create", h, m)
	}
}

// The retry re-runs the check, and the check can refuse: a live server that
// no longer answers is an outage, and a bare second create would start a
// second server beside it.
func TestNewSessionRetryRefusesWhenTheCheckFindsAnOutage(t *testing.T) {
	shortBound(t, 300*time.Millisecond)
	f, d := newSeqFake(t,
		`if [ "$n" = 1 ]; then `+noSession+`; fi; echo "no server running on /x" >&2; exit 1`,
		exitedMsg,
		procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d"))
	start := time.Now()
	err := d.NewSession("s")
	if err == nil {
		t.Fatal("NewSession returned nil with a live server that does not answer")
	}
	if m := f.calls("new-session"); m != 1 {
		t.Errorf("new-session ran %d times, want 1: no second create beside a live server", m)
	}
	if took := time.Since(start); took > 2*startupBound+5*time.Second {
		t.Errorf("took %v, want the wait bounded", took)
	}
}

// Any other text from new-session is an error at once.
func TestNewSessionDoesNotRetryOtherTexts(t *testing.T) {
	f, d := newSeqFake(t, noSession, `echo "create session failed: something else" >&2; exit 1`, procLines())
	if err := d.NewSession("s"); err == nil {
		t.Fatal("NewSession returned nil on an unrecognised new-session failure")
	}
	if h, m := f.calls("has-session"), f.calls("new-session"); h != 1 || m != 1 {
		t.Errorf("has-session ran %d times and new-session %d times, want 1 and 1", h, m)
	}
}

// An absence text that never clears stops at the bound.
func TestNewSessionRetryIsBounded(t *testing.T) {
	shortBound(t, 3*time.Second)
	f, d := newSeqFake(t, noSession, exitedMsg, procLines())
	start := time.Now()
	err := d.NewSession("s")
	if err == nil {
		t.Fatal("NewSession returned nil while new-session kept failing")
	}
	if m := f.calls("new-session"); m < 2 {
		t.Errorf("new-session ran %d times, want a retry before the bound", m)
	}
	if took := time.Since(start); took > startupBound+5*time.Second {
		t.Errorf("took %v, want the retry stopped at the bound", took)
	}
}

// A timeout from new-session is never retried.
func TestNewSessionTimeoutIsNeverRetried(t *testing.T) {
	f, d := newSeqFake(t, noSession, `echo "server exited unexpectedly" >&2; exec sleep 5`, procLines())
	// Plenty of bound left after the timeout, so a retry would be visible.
	shortBound(t, 30*time.Second)
	d.SetExecTimeout(time.Second)
	err := d.NewSession("s")
	if !errors.Is(err, ErrTmuxTimeout) {
		t.Errorf("NewSession error = %v, want ErrTmuxTimeout", err)
	}
	if m := f.calls("new-session"); m != 1 {
		t.Errorf("new-session ran %d times, want 1", m)
	}
}

// A retry pass that finds the session a peer created still sets the options:
// the peer's set-options may not have landed.
func TestNewSessionRetryPassThatFindsTheSessionStillSetsItsOptions(t *testing.T) {
	shortBound(t, 30*time.Second)
	f, d := newSeqFake(t,
		`if [ "$n" = 1 ]; then `+noSession+`; fi; exit 0`,
		exitedMsg,
		procLines())
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if n := f.calls("new-session"); n != 1 {
		t.Fatalf("new-session ran %d times, want 1: the second pass found the session", n)
	}
	if f.calls("history-limit") != 1 || f.calls("remain-on-exit") != 1 {
		t.Errorf("history-limit set %d times and remain-on-exit %d times, want once each after a retry pass that found the session",
			f.calls("history-limit"), f.calls("remain-on-exit"))
	}
}

// A duplicate session on a retry pass is the session existing: created, with
// the options set, not an error.
func TestNewSessionDuplicateOnARetryPassIsCreated(t *testing.T) {
	shortBound(t, 30*time.Second)
	f, d := newSeqFake(t, noSession,
		`if [ "$n" = 1 ]; then `+exitedMsg+`; fi; echo "duplicate session: s" >&2; exit 1`,
		procLines())
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if f.calls("history-limit") != 1 || f.calls("remain-on-exit") != 1 {
		t.Errorf("history-limit set %d times and remain-on-exit %d times, want once each",
			f.calls("history-limit"), f.calls("remain-on-exit"))
	}
}

// The retry shares one deadline with the check: a pass that starts late
// waits only for what is left of the bound, not a fresh one. The first create
// fails after 1.5 s of a 3 s bound; the second pass's check then sees a live
// server that does not answer. Total about 3 s, where a fresh wait gave 4.5 s.
func TestNewSessionPassesShareOneBound(t *testing.T) {
	shortBound(t, 3*time.Second)
	f, d := newSeqFake(t,
		`if [ "$n" = 1 ]; then `+noSession+`; fi; echo "no server running on /x" >&2; exit 1`,
		`sleep 1.5; `+exitedMsg,
		procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d"))
	start := time.Now()
	err := d.NewSession("s")
	took := time.Since(start)
	if err == nil {
		t.Fatal("NewSession returned nil with a live server that does not answer")
	}
	if m := f.calls("new-session"); m != 1 {
		t.Errorf("new-session ran %d times, want 1", m)
	}
	if took > 3900*time.Millisecond {
		t.Errorf("NewSession took %v for a 3s bound, want about 3s: the second pass must wait only for what is left", took)
	}
}
