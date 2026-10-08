package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

// seams swaps the liveness probe and the argv reader for one test and puts
// them back. These tests do not run in parallel: both are package variables.
func seams(t *testing.T, sig func(int) error, args func(int) ([]string, error)) {
	t.Helper()
	oldSig, oldArgs := liveSignal, daemonArgs
	liveSignal, daemonArgs = sig, args
	t.Cleanup(func() { liveSignal, daemonArgs = oldSig, oldArgs })
}

func alive(int) error { return nil }

func argvOf(argv ...string) func(int) ([]string, error) {
	return func(int) ([]string, error) { return argv, nil }
}

// Never name a holder that was not identified: these are the words of a
// holder claim, and a hedged message must carry none of them.
var claimWords = []string{"another marvel daemon holds", "already running"}

// A live process the pidfile names, whose argv says it is `marvel daemon`, is
// named with how it was started (marvel#606).
func TestLockedStateNamesAnIdentifiedHolder(t *testing.T) {
	for name, argv := range map[string][]string{
		"bare":                  {"marvel", "daemon"},
		"absolute path":         {"/opt/bin/marvel", "daemon", "--socket", "/s/m.sock"},
		"flag before daemon":    {"marvel", "--cluster-flag", "daemon"},
		"flag after daemon":     {"/usr/local/bin/marvel", "daemon", "--mrvl=:7000"},
		"relative with a slash": {"./marvel", "daemon"},
	} {
		seams(t, alive, argvOf(argv...))
		pid := 4242
		err := lockedStateHint(lockedErr(), writePidfile(t, pid))
		msg := err.Error()
		for _, want := range []string{
			"another marvel daemon holds the state lock",
			fmt.Sprintf("pid %d", pid),
			"started as `" + strings.Join(argv, " ") + "`",
			"it is already running",
			"marvel describe daemon",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: want %q in:\n%s", name, want, msg)
			}
		}
		if !errors.Is(err, api.ErrBoltLocked) || !strings.Contains(msg, "timeout") {
			t.Errorf("%s: the cause is dropped: %v", name, err)
		}
	}
}

// Anything short of an identified marvel daemon gets the hedged wording, which
// names the pid as a lead and not as a fact.
func TestLockedStateHedgesWhenTheHolderIsNotIdentified(t *testing.T) {
	notDaemon := map[string][]string{
		"a sleep":                      {"sleep", "30"},
		"marvel but not daemon":        {"marvel", "get", "sessions"},
		"marvel daemon as a later arg": {"marvel", "get", "daemon"},
		"another name":                 {"marvel-alpha", "daemon"},
		"a name ending in marvel":      {"notmarvel", "daemon"},
		"daemon only":                  {"daemon"},
		"only flags":                   {"marvel", "--flag"},
		"empty argv":                   {},
	}
	for name, argv := range notDaemon {
		seams(t, alive, argvOf(argv...))
		err := lockedStateHint(lockedErr(), writePidfile(t, 4242))
		assertHedged(t, name, err, "pid 4242, which may not be the holder")
	}
	seams(t, alive, func(int) ([]string, error) { return nil, errors.New("no argv here") })
	assertHedged(t, "argv unreadable", lockedStateHint(lockedErr(), writePidfile(t, 4242)), "pid 4242, which may not be the holder")
}

func assertHedged(t *testing.T, name string, err error, wantPid string) {
	t.Helper()
	msg := err.Error()
	if !strings.Contains(msg, "the state lock is held") {
		t.Errorf("%s: want the plain statement that the lock is held:\n%s", name, msg)
	}
	if wantPid != "" && !strings.Contains(msg, wantPid) {
		t.Errorf("%s: want %q:\n%s", name, wantPid, msg)
	}
	for _, bad := range claimWords {
		if strings.Contains(msg, bad) {
			t.Errorf("%s: names a holder it did not identify (%q):\n%s", name, bad, msg)
		}
	}
	if !strings.Contains(msg, "marvel describe daemon") {
		t.Errorf("%s: want the pointer to marvel describe daemon:\n%s", name, msg)
	}
	if !errors.Is(err, api.ErrBoltLocked) {
		t.Errorf("%s: cause dropped from the chain: %v", name, err)
	}
}

// A pid that is not alive, or that the caller may not signal, is not a holder:
// the bolt file is 0600, so a process of another user cannot have opened it.
func TestLockedStateDoesNotNameADeadOrForeignProcess(t *testing.T) {
	argvCalled := false
	read := func(int) ([]string, error) { argvCalled = true; return []string{"marvel", "daemon"}, nil }
	for name, sigErr := range map[string]error{
		"no such process":  syscall.ESRCH,
		"not permitted":    syscall.EPERM,
		"some other error": errors.New("boom"),
	} {
		argvCalled = false
		seams(t, func(int) error { return sigErr }, read)
		assertHedged(t, name, lockedStateHint(lockedErr(), writePidfile(t, 4242)), "pid 4242, which may not be the holder")
		if argvCalled {
			t.Errorf("%s: the argv of a process that is not live was read", name)
		}
	}
	// And a real one: a process that has exited.
	seams(t, liveSignal, read)
	assertHedged(t, "an exited process", lockedStateHint(lockedErr(), writePidfile(t, deadPid(t))), "which may not be the holder")
}

// A real unrelated live process, read through the real argv reader, is never
// named as a daemon.
func TestLockedStateDoesNotNameARealUnrelatedProcess(t *testing.T) {
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Skipf("no sleep(1): %v", err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	if liveSignal(c.Process.Pid) != nil {
		t.Fatal("the probe does not see the sleep as alive")
	}
	err := lockedStateHint(lockedErr(), writePidfile(t, c.Process.Pid))
	assertHedged(t, "a live sleep", err, fmt.Sprintf("pid %d, which may not be the holder", c.Process.Pid))
}

// The pidfile is read strictly: a pid with anything after it is not a pid, and
// a pid at or below zero is never probed (kill(-5, 0) signals a process group).
func TestLockedStateReadsThePidfileStrictly(t *testing.T) {
	probed := 0
	seams(t, func(int) error { probed++; return nil }, argvOf("marvel", "daemon"))
	own := os.Getpid()
	write := func(content string) string {
		p := filepath.Join(t.TempDir(), "marvel.pid")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, content := range map[string]string{
		"digits then text": fmt.Sprintf("%dxyz\n", own),
		"two numbers":      fmt.Sprintf("%d %d\n", own, own),
		"a sign and text":  "+" + fmt.Sprint(own) + "x",
		"not a number":     "not-a-pid\n",
		"empty":            "",
		"only whitespace":  " \n",
		"zero":             "0\n",
		"negative":         "-5\n",
		"minus one":        "-1\n",
	} {
		probed = 0
		err := lockedStateHint(lockedErr(), write(content))
		assertHedged(t, name, err, "")
		if probed != 0 {
			t.Errorf("%s: the probe ran %d time(s) on a pidfile that names no pid", name, probed)
		}
		if strings.Contains(err.Error(), "which may not be the holder") {
			t.Errorf("%s: names a pid from a pidfile that holds none:\n%v", name, err)
		}
	}
	for name, content := range map[string]string{
		"digits and a newline": fmt.Sprintf("%d\n", own),
		"padded":               fmt.Sprintf("  %d \n", own),
	} {
		err := lockedStateHint(lockedErr(), write(content))
		if !strings.Contains(err.Error(), fmt.Sprintf("pid %d,", own)) {
			t.Errorf("%s: a clean pid was not read:\n%v", name, err)
		}
	}
	missing := filepath.Join(t.TempDir(), "absent.pid")
	assertHedged(t, "missing file", lockedStateHint(lockedErr(), missing), "")
	assertHedged(t, "pidfile off", lockedStateHint(lockedErr(), ""), "")
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
