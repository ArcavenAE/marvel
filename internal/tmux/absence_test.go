package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The strings are what tmux 3.7b printed on macOS, measured on the day this
// was written, except the Connection refused one, which is the wording the
// design ruling names and was not produced here: a SIGKILLed server's stale
// socket answered "no server running" on 3.7b. The wording on Linux is
// unmeasured.
func TestClassifyAbsence(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       absence
	}{
		{"stale socket after kill-server, measured", "no server running on /private/tmp/tmux-501/probe-argv-10100", absenceNoServer},
		{"socket file missing, measured", "error connecting to /private/tmp/tmux-501/marvel-probe-nonexistent-63397 (No such file or directory)", absenceNoSocket},
		{"connection refused, wording from the ruling, unmeasured", "error connecting to /tmp/tmux-501/x (Connection refused)", absenceNoSocket},
		{"permission denied is never absence", "error connecting to /tmp/tmux-501/x (Permission denied)", absenceNone},
		{"tmux cannot create its directory, measured", "couldn't create directory /private/tmp/noperm/tmux-501 (Permission denied)", absenceNone},
		{"a bare errno without the prefix is not absence", "No such file or directory", absenceNone},
		{"a bare refusal without the prefix is not absence", "Connection refused", absenceNone},
		{"unknown pane is not server absence", "can't find pane: %9", absenceNone},
		{"empty", "", absenceNone},
		{"unrecognised", "tmux: something new", absenceNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAbsence(tc.text); got != tc.want {
				t.Errorf("classifyAbsence(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

const noSocketText = "error connecting to /private/tmp/tmux-501/unit (No such file or directory)"

// absentDriver returns a driver whose tmux prints text on stderr and exits 1,
// as a tmux that cannot reach its server does, with a process list the test
// controls.
func absentDriver(t *testing.T, text string, procs procList) *Driver {
	t.Helper()
	script := filepath.Join(t.TempDir(), "tmux")
	body := "#!/bin/sh\necho \"" + text + "\" >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Driver{binary: script, socket: "unit", procs: procs}
}

func procLines(lines ...string) procList {
	return func(context.Context) ([]byte, error) {
		out := ""
		for _, l := range lines {
			out += l + "\n"
		}
		return []byte(out), nil
	}
}

func TestPaneStatusSocketMissingNoServerProcessIsGone(t *testing.T) {
	d := absentDriver(t, noSocketText, procLines("  401 /usr/sbin/cfprefsd agent", " 1234 /opt/homebrew/bin/tmux -L another-socket new-session -d"))
	st, err := d.PaneStatus("%999999")
	if err != nil || st.Exists {
		t.Errorf("PaneStatus = %+v, %v; want gone with no error, since no tmux -L unit process is alive", st, err)
	}
}

// A socket that cannot be reached while a server for it is alive is an outage,
// not a vanished pane: reaping on it would crash every session the live server
// still runs.
func TestPaneStatusSocketMissingServerAliveIsAnOutage(t *testing.T) {
	d := absentDriver(t, noSocketText, procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d -s marvel"))
	st, err := d.PaneStatus("%999999")
	if err == nil {
		t.Fatalf("PaneStatus = %+v, nil; want an error while a tmux -L unit server is alive", st)
	}
	if errors.Is(err, ErrPaneGone) {
		t.Errorf("error %v reads as gone", err)
	}
}

func TestPaneStatusProcessListFailureIsAnOutage(t *testing.T) {
	d := absentDriver(t, noSocketText, func(context.Context) ([]byte, error) { return nil, errors.New("ps: not permitted") })
	if st, err := d.PaneStatus("%999999"); err == nil {
		t.Errorf("PaneStatus = %+v, nil; want an error when the guard cannot read the process list", st)
	}
}

// The anchor is the program and its -L argument: another socket's server, a
// non-tmux process that happens to carry -L unit, and a tmux client for a
// different option do not count.
func TestServerGuardAnchorsOnTheTmuxProgramAndItsSocketName(t *testing.T) {
	for name, line := range map[string]string{
		"another socket":      " 10 /opt/homebrew/bin/tmux -L unit-2 new-session -d",
		"not tmux":            " 11 /usr/bin/vim -L unit file.txt",
		"-L as a value":       " 12 /opt/homebrew/bin/tmux new-session -d -s -L unit",
		"name as a substring": " 13 /opt/homebrew/bin/tmux -L unit-extra new-session -d",
	} {
		t.Run(name, func(t *testing.T) {
			d := absentDriver(t, noSocketText, procLines(line))
			if st, err := d.PaneStatus("%999999"); err != nil || st.Exists {
				t.Errorf("PaneStatus = %+v, %v; want gone: %q is not a tmux -L unit server", st, err, line)
			}
		})
	}
}

// "no server running" is the server's own answer from a socket nothing
// serves, so a process-list match for the name does not turn it into an outage.
func TestNoServerRunningIsAbsenceWhateverTheProcessList(t *testing.T) {
	d := absentDriver(t, "no server running on /private/tmp/tmux-501/unit", procLines(" 1234 tmux -L unit new-session -d"))
	if st, err := d.PaneStatus("%999999"); err != nil || st.Exists {
		t.Errorf("PaneStatus = %+v, %v; want gone", st, err)
	}
}

func TestListSessionsSocketMissing(t *testing.T) {
	gone := absentDriver(t, noSocketText, procLines())
	if names, err := gone.ListSessions(); err != nil || len(names) != 0 {
		t.Errorf("no server alive: ListSessions = %v, %v; want empty and no error", names, err)
	}
	alive := absentDriver(t, noSocketText, procLines(" 1234 tmux -L unit new-session -d"))
	if names, err := alive.ListSessions(); err == nil {
		t.Errorf("server alive: ListSessions = %v, nil; want an error, not an empty list", names)
	}
}

func TestListSessionsBareErrnoIsNotAbsence(t *testing.T) {
	d := absentDriver(t, "open: No such file or directory", procLines())
	if names, err := d.ListSessions(); err == nil {
		t.Errorf("ListSessions = %v, nil; want an error for a bare errno without the connecting prefix", names)
	}
}
