package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func runGooseWatch(t *testing.T, args ...string) (code int, stdout string) {
	t.Helper()
	cmd := newGooseWatchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	return exitCodeFor(cmd.Execute()), out.String()
}

func TestGooseWatchIsHidden(t *testing.T) {
	t.Parallel()
	if !newGooseWatchCmd().Hidden {
		t.Error("goose-watch is internal and must be hidden")
	}
}

// The flags reach the wrapper: a silent command is ended as idle with exit 124,
// the marker line on stdout and the watchdog line last in the stderr file.
func TestGooseWatchEndsASilentCommandWithExit124(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "stderr.log")
	code, out := runGooseWatch(t, "--wall", "10s", "--idle", "1s", "--grace", "1s", "--stderr-file", file, "--stderr-cap", "8192",
		"--", "/bin/sh", "-c", "sleep 30")
	if code != 124 {
		t.Fatalf("exit %d, want 124", code)
	}
	if !strings.Contains(out, `{"type":"marvel.watchdog","reason":"idle"}`) {
		t.Errorf("stdout %q lacks the marker", out)
	}
}

func TestGooseWatchPassesTheCommandsExitCodeThrough(t *testing.T) {
	t.Parallel()
	code, _ := runGooseWatch(t, "--wall", "10s", "--idle", "5s", "--grace", "1s", "--", "/bin/sh", "-c", "exit 7")
	if code != 7 {
		t.Errorf("exit %d, want 7", code)
	}
}

func TestGooseWatchNeedsTheCommandAfterTheDash(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--wall", "1s", "--idle", "1s", "--grace", "1s"},
		{"--wall", "1s", "--idle", "1s", "--grace", "1s", "/bin/true"},
	} {
		if code, _ := runGooseWatch(t, args...); code != 126 {
			t.Errorf("%v: exit %d, want 126", args, code)
		}
	}
}
