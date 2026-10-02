package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A program that redraws only when it is told the terminal changed, like a TUI
// that paints on input and on resize. It prints "painted-N" on each SIGWINCH.
const winchScript = `trap 'n=$((n+1)); echo painted-$n' WINCH
n=0
echo ready
while :; do sleep 0.1; done
`

func waitFor(t *testing.T, d *Driver, pane, want string) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		got, err := d.CapturePane(pane)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if strings.Contains(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed %q; last capture:\n%s", want, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func repaintPane(t *testing.T, command string) (*Driver, string) {
	t.Helper()
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf("marvel-test-repaint-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = d.KillSession(session) })
	if err := d.NewSession(session); err != nil {
		t.Fatal(err)
	}
	pane, err := d.NewPane(session, command, "repaint-test", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return d, pane
}

// Repaint reaches a program that only redraws on a resize signal, with no input
// sent, and reports who the foreground program is.
func TestRepaintSignalsAProgramThatRedrawsOnWinch(t *testing.T) {
	script := filepath.Join(t.TempDir(), "winch.sh")
	if err := os.WriteFile(script, []byte(winchScript), 0o700); err != nil {
		t.Fatal(err)
	}
	d, pane := repaintPane(t, "sh "+script)
	waitFor(t, d, pane, "ready")
	if got, _ := d.CapturePane(pane); strings.Contains(got, "painted-") {
		t.Fatalf("pane painted before any repaint:\n%s", got)
	}
	res, err := d.Repaint(pane, testHold)
	if err != nil {
		t.Fatalf("Repaint: %v", err)
	}
	target := res.Target
	waitFor(t, d, pane, "painted-1")
	if target != "sh" {
		t.Errorf("target = %q, want the foreground leader's command name, sh", target)
	}
}

// Under a wrapper that runs the program as a child, the signal still reaches
// it: the child shares the foreground process group.
func TestRepaintReachesAProgramUnderAWrapper(t *testing.T) {
	script := filepath.Join(t.TempDir(), "winch.sh")
	if err := os.WriteFile(script, []byte(winchScript), 0o700); err != nil {
		t.Fatal(err)
	}
	d, pane := repaintPane(t, "sh -c 'sh "+script+"; echo wrapper-done'")
	waitFor(t, d, pane, "ready")
	if _, err := d.Repaint(pane, testHold); err != nil {
		t.Fatalf("Repaint: %v", err)
	}
	waitFor(t, d, pane, "painted-1")
}

// When the foreground program is a shell (a seat between commands), the signal
// goes to the shell, which survives it; the result names the shell so the
// caller does not mistake that for a harness repaint.
func TestRepaintOfAShellIsHarmlessAndNamesTheShell(t *testing.T) {
	d, pane := repaintPane(t, "sh")
	if err := d.SendKeys(pane, "echo shell-alive-1", true, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, pane, "shell-alive-1")
	res, err := d.Repaint(pane, testHold)
	if err != nil {
		t.Fatalf("Repaint of a shell: %v", err)
	}
	if target := res.Target; target != "sh" {
		t.Errorf("target = %q, want sh", target)
	}
	if err := d.SendKeys(pane, "echo shell-alive-2", true, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, d, pane, "shell-alive-2")
}

// A pane that is gone is an error, so a caller can report the repaint as
// unavailable rather than assume it happened.
func TestRepaintOfAMissingPaneIsAnError(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Repaint("%99999", testHold); err == nil {
		t.Fatal("Repaint of a pane that does not exist returned no error")
	}
}

// A pane kept after its program exited keeps reporting the old pid. Pids are
// reused, so signalling "its" foreground group could reach a process outside the
// pane, and the repaint refuses a dead pane by name.
func TestRepaintRefusesAPaneThatHasExited(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf("marvel-test-repaint-dead-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = d.KillSession(session) })
	if err := d.NewSession(session); err != nil {
		t.Fatal(err)
	}
	pane, err := d.NewPane(session, "sh -c 'exit 0'", "repaint-dead", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		out, _ := d.cmd("display-message", "-p", "-t", pane, "#{pane_dead}").CombinedOutput()
		if strings.TrimSpace(string(out)) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pane never reported dead")
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, err = d.Repaint(pane, testHold)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("Repaint of a dead pane: error = %v, want a refusal that says the pane has exited", err)
	}
}

const testHold = 150 * time.Millisecond

// A program like the harnesses research measured: it repaints when the terminal
// SIZE changes, and a bare SIGWINCH with the same size does nothing.
const sizeOnlyScript = `prev=""
echo ready
while :; do
  s=$(stty size)
  if [ "$s" != "$prev" ]; then
    [ -n "$prev" ] && echo "painted-$s"
    prev=$s
  fi
  sleep 0.05
done
`

func paneTTYPath(t *testing.T, d *Driver, pane string) string {
	t.Helper()
	out, err := d.cmd("display-message", "-p", "-t", pane, "#{pane_tty}").CombinedOutput()
	if err != nil {
		t.Fatalf("pane_tty: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// Repaint changes the pane's size and puts it back, so a program that repaints
// on a size change repaints, and no tmux option is touched.
func TestRepaintNudgesTheSizeOfAProgramThatIgnoresASignal(t *testing.T) {
	script := filepath.Join(t.TempDir(), "size.sh")
	if err := os.WriteFile(script, []byte(sizeOnlyScript), 0o700); err != nil {
		t.Fatal(err)
	}
	d, pane := repaintPane(t, "sh "+script)
	waitFor(t, d, pane, "ready")
	tty := paneTTYPath(t, d, pane)
	before, err := ttyWinsize(tty)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Repaint(pane, testHold); err != nil {
		t.Fatalf("Repaint: %v", err)
	}
	// The nudge paints at the widened size and the restore paints at the
	// original one; wait for the second, which lands after Repaint returns.
	got := waitFor(t, d, pane, fmt.Sprintf("painted-%d %d", before.Rows, before.Cols))
	if n := strings.Count(got, "painted-"); n < 2 {
		t.Errorf("painted %d time(s), want one for the nudge and one for the restore:\n%s", n, got)
	}
	after, err := ttyWinsize(tty)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("size after = %+v, want the original %+v", after, before)
	}
}

// The restore is compare-and-restore: it only writes the original size back if
// the size is still the one the nudge set. A real resize during the hold wins.
func TestNudgeWinsizeRestoresTheOriginal(t *testing.T) {
	d, pane := repaintPane(t, "sleep 60")
	tty := paneTTYPath(t, d, pane)
	orig, err := ttyWinsize(tty)
	if err != nil {
		t.Fatal(err)
	}
	var during winsize
	skipped, err := nudgeWinsize(tty, 10*time.Millisecond, func() error {
		during, err = ttyWinsize(tty)
		return err
	})
	if err != nil {
		t.Fatalf("nudge: %v", err)
	}
	if skipped {
		t.Error("restore skipped with nothing else changing the size")
	}
	if during.Cols != orig.Cols+1 || during.Rows != orig.Rows {
		t.Errorf("size during the hold = %+v, want one column wider than %+v", during, orig)
	}
	if after, _ := ttyWinsize(tty); after != orig {
		t.Errorf("size after = %+v, want %+v", after, orig)
	}
}

func TestNudgeWinsizeLeavesAResizeThatHappenedUnderneath(t *testing.T) {
	d, pane := repaintPane(t, "sleep 60")
	tty := paneTTYPath(t, d, pane)
	orig, err := ttyWinsize(tty)
	if err != nil {
		t.Fatal(err)
	}
	other := winsize{Rows: orig.Rows, Cols: orig.Cols + 7}
	skipped, err := nudgeWinsize(tty, 10*time.Millisecond, func() error { return setTTYWinsize(tty, other) })
	if err != nil {
		t.Fatalf("nudge: %v", err)
	}
	if !skipped {
		t.Error("restore not reported as skipped after the size changed underneath")
	}
	if after, _ := ttyWinsize(tty); after != other {
		t.Errorf("size after = %+v, want the other writer's %+v left alone", after, other)
	}
}

func TestNudgeWinsizeRestoresWhenAStepFails(t *testing.T) {
	d, pane := repaintPane(t, "sleep 60")
	tty := paneTTYPath(t, d, pane)
	orig, err := ttyWinsize(tty)
	if err != nil {
		t.Fatal(err)
	}
	_, err = nudgeWinsize(tty, 10*time.Millisecond, func() error { return fmt.Errorf("injected failure") })
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("error = %v, want the injected failure returned", err)
	}
	if after, _ := ttyWinsize(tty); after != orig {
		t.Errorf("size after a failed step = %+v, want the original %+v restored", after, orig)
	}
}

func TestNudgeWinsizeOfAMissingTTYIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := nudgeWinsize("/dev/does-not-exist", time.Millisecond, nil); err == nil {
		t.Fatal("nudge of a tty that does not exist returned no error")
	}
}

// Repaint changes no tmux state: the window-size option stays unset and the
// pane keeps its size.
func TestRepaintLeavesTmuxOptionsAlone(t *testing.T) {
	d, pane := repaintPane(t, "sleep 60")
	read := func() string {
		out, _ := d.cmd("display-message", "-p", "-t", pane, "#{pane_width}x#{pane_height} [#{window-size}]").CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	opt := func() string {
		out, _ := d.cmd("show-options", "-w", "-t", pane, "window-size").CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	beforeSize, beforeOpt := read(), opt()
	if _, err := d.Repaint(pane, testHold); err != nil {
		t.Fatalf("Repaint: %v", err)
	}
	if got := read(); got != beforeSize {
		t.Errorf("pane = %q, want %q unchanged", got, beforeSize)
	}
	if got := opt(); got != beforeOpt {
		t.Errorf("window-size option = %q, want %q unchanged", got, beforeOpt)
	}
}

func TestValidateRepaintPane(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		dead    bool
		tty     string
		wantErr string
	}{
		{"live pane with a terminal", false, "/dev/ttys003", ""},
		{"dead pane", true, "/dev/ttys003", "exited"},
		{"no terminal reported", false, "", "no terminal"},
	}
	for _, tc := range cases {
		err := validateRepaintPane(tc.dead, tc.tty)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: error %v, want none", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.wantErr)
		}
	}
}
