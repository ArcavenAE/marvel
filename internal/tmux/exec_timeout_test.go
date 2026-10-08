package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// hungDriver returns a driver whose tmux is a script that prints stderr text
// (may be empty) and then sleeps far past the bound, which is what a tmux
// client does while its server is stopped.
func hungDriver(t *testing.T, stderrText string) *Driver {
	t.Helper()
	script := filepath.Join(t.TempDir(), "tmux")
	body := "#!/bin/sh\n"
	if stderrText != "" {
		body += "echo \"" + stderrText + "\" >&2\n"
	}
	body += "sleep 60\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit"}
	d.SetExecTimeout(300 * time.Millisecond)
	return d
}

// within runs f and fails the test if it has not returned inside limit, so a
// regression to an unbounded exec fails instead of hanging the package.
func within(t *testing.T, f func()) {
	const limit = 5 * time.Second
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("call did not return within %s", limit)
	}
}

func TestHungTmuxReturnsTypedTimeout(t *testing.T) {
	d := hungDriver(t, "")
	calls := map[string]func() error{
		"PaneStatus":  func() error { _, err := d.PaneStatus("%1"); return err },
		"ListSession": func() error { _, err := d.ListSessions(); return err },
		"ListPanes":   func() error { _, err := d.ListPanes("s"); return err },
		"PanePID":     func() error { _, err := d.PanePID("%1"); return err },
		"KillPane":    func() error { return d.KillPane("%1") },
		"CapturePane": func() error { _, err := d.CapturePane("%1"); return err },
		"SendKeys":    func() error { return d.SendKeys("%1", "hi", true, true) },
		"NewSession":  func() error { return d.NewSession("s") },
		"KillSession": func() error { return d.KillSession("s") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			var err error
			within(t, func() { err = call() })
			if !errors.Is(err, ErrTmuxTimeout) {
				t.Fatalf("want ErrTmuxTimeout, got %v", err)
			}
		})
	}
}

// A timeout must not read as "pane gone", even when the killed client had
// already printed tmux's gone wording.
func TestTimeoutIsNotPaneGone(t *testing.T) {
	d := hungDriver(t, "can't find pane: %1")
	// Long enough that the script has certainly printed before the kill; a
	// shorter bound lets a loaded machine kill it first and prove nothing.
	d.SetExecTimeout(2 * time.Second)
	var st PaneStatus
	var err error
	within(t, func() { st, err = d.PaneStatus("%1") })
	if !errors.Is(err, ErrTmuxTimeout) || st.Exists {
		t.Fatalf("PaneStatus: want ErrTmuxTimeout, got %+v, %v", st, err)
	}
	within(t, func() { err = d.KillPane("%1") })
	if errors.Is(err, ErrPaneGone) || !errors.Is(err, ErrTmuxTimeout) {
		t.Fatalf("KillPane: want ErrTmuxTimeout and not ErrPaneGone, got %v", err)
	}
	within(t, func() { _, err = d.ListSessions() })
	if !errors.Is(err, ErrTmuxTimeout) {
		t.Fatalf("ListSessions: want ErrTmuxTimeout, got %v", err)
	}
}

func TestExecWithinBoundIsUnaffected(t *testing.T) {
	script := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '0\\t\\t\\t1\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit"}
	d.SetExecTimeout(5 * time.Second)
	st, err := d.PaneStatus("%1")
	if err != nil || !st.Exists || st.Dead || !st.Created {
		t.Fatalf("want a live created pane, got %+v, %v", st, err)
	}
}

func TestDefaultExecTimeoutApplies(t *testing.T) {
	d := &Driver{}
	if got := d.timeout(); got != DefaultExecTimeout {
		t.Fatalf("zero value: want %s, got %s", DefaultExecTimeout, got)
	}
	d.SetExecTimeout(time.Second)
	if got := d.timeout(); got != time.Second {
		t.Fatalf("set: want 1s, got %s", got)
	}
}

// NewSession must not take a has-session that timed out for "no such
// session" and go on to create one. The fake hangs only on has-session and
// records any new-session, so a NewSession that ignored the timeout would
// run it.
func TestNewSessionDoesNotCreateAfterHasSessionTimesOut(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "new-session-ran")
	script := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\ncase \"$3\" in\nhas-session) sleep 60 ;;\nnew-session) : > " + marker + " ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit"}
	d.SetExecTimeout(300 * time.Millisecond)

	var err error
	within(t, func() { err = d.NewSession("s") })
	if !errors.Is(err, ErrTmuxTimeout) {
		t.Fatalf("want ErrTmuxTimeout, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("new-session ran after has-session timed out")
	}
}
