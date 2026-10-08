package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
)

// lockedErr is what the daemon returns when the state file lock is held.
func lockedErr() error {
	return fmt.Errorf("open state bolt at /s/marvel.bolt: %w",
		fmt.Errorf("open bbolt at /s/marvel.bolt: %w: %w", api.ErrBoltLocked, errors.New("timeout")))
}

// deadPid returns the pid of a process that has exited.
func deadPid(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Skipf("no true(1) to make a dead pid: %v", err)
	}
	return c.Process.Pid
}

func writePidfile(t *testing.T, pid int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "marvel.pid")
	if err := os.WriteFile(p, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A lock held by a live daemon is reported as the answer, not as a broken
// daemon: it names the holder and points at the read (marvel#606).
func TestLockedStateNamesTheLiveHolder(t *testing.T) {
	pid := os.Getpid()
	err := lockedStateHint(lockedErr(), writePidfile(t, pid))
	msg := err.Error()
	for _, want := range []string{
		"another marvel daemon holds the state lock",
		fmt.Sprintf("pid %d", pid),
		"already running",
		"marvel describe daemon",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("want %q in:\n%s", want, msg)
		}
	}
	if !errors.Is(err, api.ErrBoltLocked) {
		t.Errorf("the hint must keep the cause in the chain: %v", err)
	}
	if !strings.Contains(msg, "timeout") {
		t.Errorf("the original cause text is dropped:\n%s", msg)
	}
}

// With no live daemon named, the message must not claim one is running.
func TestLockedStateDoesNotClaimADaemonItCannotSee(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pid")
	corrupt := filepath.Join(t.TempDir(), "corrupt.pid")
	if err := os.WriteFile(corrupt, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, pidfile := range map[string]string{
		"dead pid":     writePidfile(t, deadPid(t)),
		"missing file": missing,
		"corrupt file": corrupt,
		"pidfile off":  "",
		"pid zero":     writePidfile(t, 0),
		"negative pid": writePidfile(t, -5),
	} {
		err := lockedStateHint(lockedErr(), pidfile)
		msg := err.Error()
		if strings.Contains(msg, "already running") || strings.Contains(msg, "another marvel daemon holds") {
			t.Errorf("%s: claims a live daemon it did not find:\n%s", name, msg)
		}
		if !strings.Contains(msg, "held by another process") {
			t.Errorf("%s: want a plain statement that another process holds the lock:\n%s", name, msg)
		}
		if !strings.Contains(msg, "marvel describe daemon") {
			t.Errorf("%s: want the pointer to marvel describe daemon:\n%s", name, msg)
		}
		if !errors.Is(err, api.ErrBoltLocked) {
			t.Errorf("%s: cause dropped from the chain: %v", name, err)
		}
	}
}

// Any other failure passes through untouched.
func TestLockedStateLeavesOtherErrorsAlone(t *testing.T) {
	other := errors.New("open state bolt at /s/marvel.bolt: permission denied")
	if got := lockedStateHint(other, writePidfile(t, os.Getpid())); got != other {
		t.Errorf("a non-lock error was changed: %v", got)
	}
	if got := lockedStateHint(nil, ""); got != nil {
		t.Errorf("nil became %v", got)
	}
}

// `marvel daemon status` is the thing people reach for; it is refused and
// pointed at the read that exists (marvel#606).
func TestDaemonStatusIsPointedAtDescribeDaemon(t *testing.T) {
	root := &cobra.Command{Use: "marvel"}
	root.AddCommand(daemonCmd())
	daemon, _, err := root.Find([]string{"daemon"})
	if err != nil {
		t.Fatal(err)
	}
	err = daemon.ValidateArgs([]string{"status"})
	if err == nil {
		t.Fatal("marvel daemon status was accepted")
	}
	for _, want := range []string{
		`unknown command "status" for "marvel daemon"`,
		"did you mean `marvel describe daemon`?",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
	for _, arg := range []string{"zzz-bogus-control", "start", "statuses"} {
		err := daemon.ValidateArgs([]string{arg})
		if err == nil || strings.Contains(err.Error(), "describe daemon") {
			t.Errorf("marvel daemon %s: want a plain refusal, got %v", arg, err)
		}
	}
}
