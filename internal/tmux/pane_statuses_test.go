package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scriptedDriver is a driver whose tmux is a shell script: it appends each
// call's arguments to a log, then runs body.
func scriptedDriver(t *testing.T, body string) (*Driver, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := filepath.Join(dir, "tmux")
	src := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + body + "\n"
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit"}
	d.SetExecTimeout(5 * time.Second)
	return d, log
}

func callCount(t *testing.T, log string) int {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
}

// marvel#1214: one list-panes -a answers for every pane.
func TestPaneStatusesReadsEveryPaneInOneCall(t *testing.T) {
	t.Parallel()
	d, log := scriptedDriver(t, `printf '%%1\t0\t\t\t1\n%%2\t1\t3\t15\t1\n%%3\t0\t\t\t\n'`)
	got, err := d.PaneStatuses()
	if err != nil {
		t.Fatalf("PaneStatuses: %v", err)
	}
	if n := callCount(t, log); n != 1 {
		t.Errorf("PaneStatuses ran tmux %d times, want 1", n)
	}
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "list-panes -a") {
		t.Errorf("the call was %q, want a list-panes -a", raw)
	}
	if len(got) != 3 {
		t.Fatalf("got %d panes, want 3: %+v", len(got), got)
	}
	if st := got["%1"]; !st.Exists || st.Dead || !st.Created {
		t.Errorf("%%1 = %+v, want a live pane marvel created", st)
	}
	if st := got["%2"]; !st.Exists || !st.Dead || st.ExitStatus != "3" || st.Signal != "15" || !st.Created {
		t.Errorf("%%2 = %+v, want a dead created pane with status 3 and signal 15", st)
	}
	if st := got["%3"]; !st.Exists || st.Dead || st.Created {
		t.Errorf("%%3 = %+v, want a live pane marvel did not create", st)
	}
	if _, ok := got["%9"]; ok {
		t.Error("a pane that is not in the listing appeared in the map")
	}
}

// It reads each pane as PaneStatus does, so the two agree on one pane.
func TestPaneStatusesAgreesWithPaneStatus(t *testing.T) {
	t.Parallel()
	d, _ := scriptedDriver(t, `printf '%%7\t1\t2\t\t1\n'`)
	all, err := d.PaneStatuses()
	if err != nil {
		t.Fatal(err)
	}
	// The one-pane form's reply has no pane id column.
	one, _ := scriptedDriver(t, `printf '1\t2\t\t1\n'`)
	st, err := one.PaneStatus("%7")
	if err != nil {
		t.Fatal(err)
	}
	if all["%7"] != st {
		t.Errorf("PaneStatuses %+v and PaneStatus %+v disagree", all["%7"], st)
	}
}

func TestPaneStatusesRefusesAnUnparseableLine(t *testing.T) {
	t.Parallel()
	d, _ := scriptedDriver(t, `printf '%%1\t0\n'`)
	if got, err := d.PaneStatuses(); err == nil {
		t.Errorf("PaneStatuses = %+v, nil; want an error for a short line", got)
	}
}

// With no server process for this socket alive, the listing is empty and not
// an error; with one alive, the same reply is an outage. PaneStatus has the
// same rule, so a caller reads the two alike.
func TestPaneStatusesNoServerIsAbsenceOnlyWhenNoServerProcessIsAlive(t *testing.T) {
	gone := absentDriver(t, noServerText, procLines(" 1 /opt/homebrew/bin/tmux -L another new-session -d"))
	got, err := gone.PaneStatuses()
	if err != nil || len(got) != 0 {
		t.Errorf("no server alive: PaneStatuses = %+v, %v; want an empty map and no error", got, err)
	}
	alive := absentDriver(t, noServerText, procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d"))
	if got, err := alive.PaneStatuses(); err == nil {
		t.Errorf("server alive: PaneStatuses = %+v, nil; want an outage error", got)
	}
}

func TestPaneStatusesTimeoutIsTyped(t *testing.T) {
	d := hungDriver(t, "")
	var err error
	within(t, func() { _, err = d.PaneStatuses() })
	if !errors.Is(err, ErrTmuxTimeout) {
		t.Fatalf("want ErrTmuxTimeout, got %v", err)
	}
}
