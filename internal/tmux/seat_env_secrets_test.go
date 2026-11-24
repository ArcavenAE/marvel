package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fakeSecretName = "BEADS_DOLT_PASSWORD"

// envDumpTmux is a tmux that logs its arguments and the NAMES of the variables
// it was started with, never a value.
func envDumpTmux(t *testing.T, reply string) (*Driver, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\necho \"ARGS $@\" >> '" + log + "'\nenv | cut -d= -f1 | sed 's/^/ENV /' >> '" + log + "'\n" + reply
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Driver{binary: bin}, log
}

func readLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b) // a tmux that was never called left no log
}

// Control: with no secret names set, a tmux client inherits the daemon's
// environment, which is how a server comes to hold a variable.
func TestAClientInheritsTheVariableWhenNoSecretNameIsSet(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")
	d, log := envDumpTmux(t, "")
	if _, err := d.NewPaneAt("sess", "claude", "t", "", nil, false); err != nil {
		t.Fatalf("NewPaneAt: %v", err)
	}
	if !strings.Contains(readLog(t, log), "ENV "+fakeSecretName+"\n") {
		t.Error("the control client did not inherit the variable, so the next test proves nothing")
	}
}

// A declared secret name is kept out of every tmux client's environment, so the
// server a client starts never holds it, and out of every pane through env -u.
func TestASecretNameIsKeptOutOfTheClientAndThePane(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")
	d, log := envDumpTmux(t, "")
	d.SetSeatSecretNames([]string{fakeSecretName})
	if _, err := d.NewPaneAt("sess", "claude", "t", "", nil, false); err != nil {
		t.Fatalf("NewPaneAt: %v", err)
	}
	got := readLog(t, log)
	if strings.Contains(got, "ENV "+fakeSecretName+"\n") {
		t.Errorf("the tmux client still carries %s", fakeSecretName)
	}
	if !strings.Contains(got, " -u "+fakeSecretName+" ") {
		t.Errorf("the pane command does not unset %s: %s", fakeSecretName, got)
	}
	if strings.Contains(got, "mxfake-0042") {
		t.Error("the value reached the tmux log")
	}
}

// A server that is already running, started by a process that carried the name,
// has it removed from its global environment by name.
func TestStripServerEnvRemovesTheNameByName(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")
	d, log := envDumpTmux(t, "")
	d.SetSeatSecretNames([]string{fakeSecretName})
	if err := d.StripServerEnv(); err != nil {
		t.Fatalf("StripServerEnv: %v", err)
	}
	got := readLog(t, log)
	if !strings.Contains(got, "ARGS -u set-environment -g -u "+fakeSecretName+"\n") {
		t.Errorf("no set-environment -g -u %s call: %s", fakeSecretName, got)
	}
	if strings.Contains(got, "mxfake-0042") {
		t.Error("the value reached the tmux log")
	}
}

// With no server running there is nothing to strip, and that is not an error.
func TestStripServerEnvWithNoServerIsNotAnError(t *testing.T) {
	d, _ := envDumpTmux(t, "echo 'no server running on /tmp/x' >&2\nexit 1\n")
	d.SetSeatSecretNames([]string{fakeSecretName})
	if err := d.StripServerEnv(); err != nil {
		t.Errorf("StripServerEnv with no server: %v", err)
	}
}

func serverEnvNames(t *testing.T, pid int) map[string]bool {
	t.Helper()
	out, err := exec.Command("ps", "-E", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Skipf("ps -E cannot read the server here: %v", err)
	}
	names := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		if k, _, ok := strings.Cut(f, "="); ok && k != "" {
			names[k] = true
		}
	}
	return names
}

func realDriver(t *testing.T, tag string) *Driver {
	t.Helper()
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxsec")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", tag+filepath.Base(dir))
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.KillServer() })
	return d
}

// The measurement behind the strip, by key name only: a server started by a
// daemon that carries the variable holds it (control), and a server started once
// the secret name is set does not, while the same daemon still carries it.
func TestAServerStartedAfterTheNameIsSetHoldsNoSecret(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")

	control := realDriver(t, "mxsecc")
	if err := control.NewSession("probe"); err != nil {
		t.Fatalf("control server: %v", err)
	}
	cpid, err := control.ServerPID()
	if err != nil {
		t.Fatal(err)
	}
	if !serverEnvNames(t, cpid)[fakeSecretName] {
		t.Fatal("the control server does not hold the variable, so the proof below would pass without the change")
	}

	d := realDriver(t, "mxsecs")
	d.SetSeatSecretNames([]string{fakeSecretName})
	if err := d.NewSession("probe"); err != nil {
		t.Fatalf("server: %v", err)
	}
	pid, err := d.ServerPID()
	if err != nil {
		t.Fatal(err)
	}
	if serverEnvNames(t, pid)[fakeSecretName] {
		t.Errorf("the tmux server holds %s", fakeSecretName)
	}
}

// A pane of a running server, started by a dirty process, loses the name from the
// server's global environment through StripServerEnv, and a pane started after it
// does not carry it either.
func TestStripServerEnvLeavesNoNameForTheNextPane(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")
	d := realDriver(t, "mxsecd")
	if err := d.NewSession("probe"); err != nil { // a dirty start: no name set yet
		t.Fatalf("server: %v", err)
	}
	if out, _ := d.cmd("show-environment", "-g", fakeSecretName).CombinedOutput(); !strings.Contains(string(out), fakeSecretName+"=") {
		t.Fatalf("the dirty server does not hold the name in its global environment: %q", out)
	}
	d.SetSeatSecretNames([]string{fakeSecretName})
	if err := d.StripServerEnv(); err != nil {
		t.Fatalf("StripServerEnv: %v", err)
	}
	out, _ := d.cmd("show-environment", "-g", fakeSecretName).CombinedOutput()
	if strings.Contains(string(out), fakeSecretName+"=") {
		t.Errorf("the global environment still holds the name after the strip")
	}
}

// The first window of a session a cold server creates (what the session manager's
// NewSession does before any seat pane) holds no secret either. A pane starts
// with the server's global environment plus its session's, and tmux adds only the
// update-environment names (display and ssh variables) from the client. The
// panes' own shell is a platform binary that ps -E cannot read, so the proof is
// those two tables, by name. The control shows the probe sees the name when it
// is there.
func TestTheFirstWindowOfAColdServerHoldsNoSecret(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")
	holds := func(d *Driver) (global, session bool) {
		t.Helper()
		g, _ := d.cmd("show-environment", "-g", fakeSecretName).CombinedOutput()
		s, _ := d.cmd("show-environment", "-t", "probe", fakeSecretName).CombinedOutput()
		return strings.Contains(string(g), fakeSecretName+"="), strings.Contains(string(s), fakeSecretName+"=")
	}
	control := realDriver(t, "mxfwc")
	if err := control.NewSession("probe"); err != nil {
		t.Fatal(err)
	}
	if g, _ := holds(control); !g {
		t.Fatal("the control server does not hold the variable globally, so the proof below would pass without the change")
	}
	d := realDriver(t, "mxfws")
	d.SetSeatSecretNames([]string{fakeSecretName})
	if err := d.NewSession("probe"); err != nil {
		t.Fatal(err)
	}
	if g, s := holds(d); g || s {
		t.Errorf("a cold server's first window would inherit %s (global %v, session %v)", fakeSecretName, g, s)
	}
}
