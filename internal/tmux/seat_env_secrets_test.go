package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// rawPaneEnvNames starts a pane with a bare new-window, not NewPane, because
// NewPane puts the secret names in the pane's own `env -u` prefix and would hide a
// strip that did nothing. It returns the NAMES the pane holds, never a value.
func rawPaneEnvNames(t *testing.T, d *Driver, tag string) map[string]bool {
	t.Helper()
	out := filepath.Join(t.TempDir(), tag)
	script := "env | cut -d= -f1 > '" + out + ".tmp' && mv '" + out + ".tmp' '" + out + "'"
	if b, err := d.cmd("new-window", "-d", "-t", "probe", script).CombinedOutput(); err != nil {
		t.Fatalf("new-window: %v: %s", err, b)
	}
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(out); err == nil {
			names := map[string]bool{}
			for _, n := range strings.Fields(string(b)) {
				names[n] = true
			}
			return names
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the %s pane never wrote its environment names", tag)
	return nil
}

// A running server started by a dirty process loses the name from its global
// environment through StripServerEnv, and a pane started after the strip does not
// carry it. A pane started before the strip is the control: it does carry it.
func TestStripServerEnvLeavesNoNameForTheNextPane(t *testing.T) {
	t.Setenv(fakeSecretName, "mxfake-0042")
	d := realDriver(t, "mxsecd")
	if err := d.NewSession("probe"); err != nil { // a dirty start: no name set yet
		t.Fatalf("server: %v", err)
	}
	if out, _ := d.cmd("show-environment", "-g", fakeSecretName).CombinedOutput(); !strings.Contains(string(out), fakeSecretName+"=") {
		t.Fatalf("the dirty server does not hold the name in its global environment: %q", out)
	}
	if !rawPaneEnvNames(t, d, "before")[fakeSecretName] {
		t.Fatal("a pane started before the strip does not carry the name, so the check below would pass without the change")
	}
	d.SetSeatSecretNames([]string{fakeSecretName})
	if err := d.StripServerEnv(); err != nil {
		t.Fatalf("StripServerEnv: %v", err)
	}
	out, _ := d.cmd("show-environment", "-g", fakeSecretName).CombinedOutput()
	if strings.Contains(string(out), fakeSecretName+"=") {
		t.Errorf("the global environment still holds the name after the strip")
	}
	if rawPaneEnvNames(t, d, "after")[fakeSecretName] {
		t.Errorf("a pane started after the strip still carries %s", fakeSecretName)
	}
}
