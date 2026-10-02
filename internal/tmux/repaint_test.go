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
	target, err := d.Repaint(pane)
	if err != nil {
		t.Fatalf("Repaint: %v", err)
	}
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
	if _, err := d.Repaint(pane); err != nil {
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
	target, err := d.Repaint(pane)
	if err != nil {
		t.Fatalf("Repaint of a shell: %v", err)
	}
	if target != "sh" {
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
	if _, err := d.Repaint("%99999"); err == nil {
		t.Fatal("Repaint of a pane that does not exist returned no error")
	}
}
