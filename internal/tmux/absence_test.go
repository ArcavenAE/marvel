package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
		{"retry-only text is never absence, server exited", "server exited unexpectedly", absenceNone},
		{"retry-only text is never absence, no current target", "no current target", absenceNone},
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

// "no server running" is read by the same rule as a missing socket: it is
// absence only when no tmux server for this -L name is alive. A server stopped
// with a full listen backlog answers it too (measured by the #726 review on
// tmux 3.7b), and reading that as gone reaps every session the server runs.
const noServerText = "no server running on /private/tmp/tmux-501/unit"

func TestNoServerRunningIsAbsenceOnlyWhenNoServerProcessIsAlive(t *testing.T) {
	gone := absentDriver(t, noServerText, procLines(" 1 /opt/homebrew/bin/tmux -L another new-session -d"))
	if st, err := gone.PaneStatus("%999999"); err != nil || st.Exists {
		t.Errorf("no server alive: PaneStatus = %+v, %v; want gone", st, err)
	}
	alive := absentDriver(t, noServerText, procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d"))
	st, err := alive.PaneStatus("%999999")
	if err == nil || errors.Is(err, ErrPaneGone) {
		t.Errorf("server alive: PaneStatus = %+v, %v; want an outage error", st, err)
	}
}

func TestNoServerRunningWithAServerAliveIsAnOutageForEveryCaller(t *testing.T) {
	alive := procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d")
	d := absentDriver(t, noServerText, alive)
	if names, err := d.ListSessions(); err == nil {
		t.Errorf("ListSessions = %v, nil; want an error, not an empty list", names)
	}
	if err := d.KillPane("%1"); err == nil || errors.Is(err, ErrPaneGone) {
		t.Errorf("KillPane = %v; want an outage error, not ErrPaneGone", err)
	}
	if ok, err := d.sessionExists("s"); err == nil {
		t.Errorf("sessionExists = %v, nil; want an error", ok)
	}
	gone := absentDriver(t, noServerText, procLines())
	if names, err := gone.ListSessions(); err != nil || len(names) != 0 {
		t.Errorf("no server alive: ListSessions = %v, %v; want empty and no error", names, err)
	}
	if ok, err := gone.sessionExists("s"); err != nil || ok {
		t.Errorf("no server alive: sessionExists = %v, %v; want false and no error", ok, err)
	}
}

// create-if-absent must not create on an outage: a second server on the same
// name runs panes marvel cannot see.
func TestNewSessionRefusesToCreateOnAnOutage(t *testing.T) {
	for name, text := range map[string]string{"no server running": noServerText, "socket missing": noSocketText} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			script := filepath.Join(dir, "tmux")
			body := "#!/bin/sh\necho \"$*\" >> " + log + "\necho \"" + text + "\" >&2\nexit 1\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			d := &Driver{binary: script, socket: "unit", procs: procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d")}
			if err := d.NewSession("s"); err == nil {
				t.Error("NewSession returned nil with a server alive and unreachable; want an error")
			}
			calls, _ := os.ReadFile(log)
			if strings.Contains(string(calls), "new-session") {
				t.Errorf("NewSession ran new-session on an outage; calls:\n%s", calls)
			}
		})
	}
}

// With the server really absent, create-if-absent still creates.
func TestNewSessionCreatesWhenNoServerIsAlive(t *testing.T) {
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxabs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", "mxabs"+filepath.Base(dir))
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.KillServer() })
	if err := d.NewSession("probe"); err != nil {
		t.Fatalf("NewSession on a fresh socket: %v", err)
	}
	if !d.HasSession("probe") {
		t.Error("the session was not created")
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

// A live server whose socket file was removed (a /tmp sweep, a stray rm) is
// not an absent server: asking for its pane must be an outage, not "gone", or
// the caller reaps every session the server still runs. Real tmux, real ps.
func TestPaneStatusLiveServerWithItsSocketFileRemovedIsAnOutage(t *testing.T) {
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxabs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	name := "mxabs" + filepath.Base(dir)
	t.Setenv("MARVEL_TMUX_SOCKET", name)
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NewSession("probe"); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = d.KillServer() })
	pane, err := d.NewPane("probe", "sleep 60", "probe", nil, false)
	if err != nil {
		t.Fatalf("start a pane: %v", err)
	}
	out, err := d.cmd("display-message", "-p", "#{socket_path}").Output()
	if err != nil {
		t.Fatalf("ask the server for its socket: %v", err)
	}
	sock := strings.TrimSpace(string(out))
	// Removing the socket file leaves KillServer unable to reach the server,
	// so stop it by pid. The pid is read while the socket still answers.
	pidOut, err := d.cmd("display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("ask the server for its pid: %v", err)
	}
	serverPid, err := strconv.Atoi(strings.TrimSpace(string(pidOut)))
	if err != nil || serverPid <= 1 {
		t.Fatalf("server pid %q: %v", pidOut, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(serverPid, syscall.SIGTERM) })
	if st, err := d.PaneStatus(pane); err != nil || !st.Exists {
		t.Fatalf("setup: PaneStatus before the socket is removed = %+v, %v; want the pane", st, err)
	}
	if err := os.Remove(sock); err != nil {
		t.Fatalf("remove the socket file: %v", err)
	}
	if st, err := d.PaneStatus(pane); err == nil {
		t.Errorf("PaneStatus = %+v, nil with the server alive and its socket file gone; want an error", st)
	}
}

// MARVEL_TMUX_SOCKET=default reproduces the old shared server, which a user
// usually started as plain `tmux`, with no -L in its argv. That process must
// anchor the guard for the name "default", along with an explicit
// `-L default`; for any other name a plain tmux is somebody else's server.
func TestDefaultSocketNameAnchorsOnAPlainTmuxServer(t *testing.T) {
	for _, tc := range []struct {
		name, socket, line string
		want               bool
	}{
		{"plain tmux", "default", " 10 /opt/homebrew/bin/tmux new-session -d -s work", true},
		{"plain tmux with -f", "default", " 10 /opt/homebrew/bin/tmux -f /x/tmux.conf new-session -d", true},
		{"explicit -L default", "default", " 11 /opt/homebrew/bin/tmux -L default new-session -d", true},
		{"another -L name", "default", " 12 /opt/homebrew/bin/tmux -L marvel-1 new-session -d", false},
		{"-S names its own socket", "default", " 13 /opt/homebrew/bin/tmux -S /tmp/x.sock new-session -d", false},
		{"not tmux", "default", " 14 /usr/bin/vim new-session", false},
		{"plain tmux is not another name's server", "unit", " 15 /opt/homebrew/bin/tmux new-session -d", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := carriesSocket(strings.Fields(tc.line), tc.socket); got != tc.want {
				t.Errorf("carriesSocket(%q, %q) = %v, want %v", tc.line, tc.socket, got, tc.want)
			}
		})
	}
}

func TestDefaultSocketWithAPlainTmuxServerAliveIsAnOutage(t *testing.T) {
	d := absentDriver(t, noServerText, procLines(" 10 /opt/homebrew/bin/tmux new-session -d -s work"))
	d.socket = "default"
	if st, err := d.PaneStatus("%1"); err == nil || errors.Is(err, ErrPaneGone) {
		t.Errorf("PaneStatus = %+v, %v; want an outage error with a plain tmux server alive", st, err)
	}
	gone := absentDriver(t, noServerText, procLines())
	gone.socket = "default"
	if st, err := gone.PaneStatus("%1"); err != nil || st.Exists {
		t.Errorf("no server alive: PaneStatus = %+v, %v; want gone", st, err)
	}
}
