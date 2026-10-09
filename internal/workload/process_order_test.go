package workload

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func sleepSpec(t *testing.T) (ProcessSpec, string) {
	t.Helper()
	dir := t.TempDir()
	pf := filepath.Join(dir, "run", "child.pid")
	return ProcessSpec{
		Name:    "t",
		Binary:  "/bin/sleep",
		Args:    []string{"30"},
		LogPath: filepath.Join(dir, "log", "child.log"),
		PidFile: pf,
	}, pf
}

func reap(t *testing.T, c *Child) {
	t.Helper()
	t.Cleanup(func() { _ = c.Cmd.Process.Kill(); _ = c.Cmd.Wait() })
}

// An identity record must never trail the pidfile that names it: a crash
// between the two would leave a pidfile with no proof, which reads as a
// legacy file. So the hook runs after the child starts and before the
// pidfile exists (docs/design/bus-pidfile-identity.md section 3.1).
func TestBeforePidFileRunsAfterTheChildStartsAndBeforeThePidfile(t *testing.T) {
	spec, pf := sleepSpec(t)
	var sawPid int
	var pidfileThen bool
	spec.BeforePidFile = func(pid int) error {
		sawPid = pid
		_, err := os.Stat(pf)
		pidfileThen = err == nil
		if syscall.Kill(pid, 0) != nil {
			t.Errorf("the child %d is not running when the hook runs", pid)
		}
		return nil
	}
	c, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	reap(t, c)
	if sawPid != c.Pid || sawPid == 0 {
		t.Errorf("hook saw pid %d, the child is %d", sawPid, c.Pid)
	}
	if pidfileThen {
		t.Error("the pidfile existed before the hook ran")
	}
	data, err := os.ReadFile(pf)
	if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(c.Pid) {
		t.Errorf("pidfile = %q, %v; want the child's pid after the hook", data, err)
	}
}

func TestBeforePidFileErrorStopsTheChildAndWritesNoPidfile(t *testing.T) {
	spec, pf := sleepSpec(t)
	var hookPid int
	spec.BeforePidFile = func(pid int) error {
		hookPid = pid
		return errors.New("cannot read the child's identity")
	}
	c, err := Start(spec)
	if c != nil {
		reap(t, c) // only reached when Start wrongly succeeded
	}
	if err == nil || !strings.Contains(err.Error(), "cannot read the child's identity") {
		t.Fatalf("Start = %v, %v; want the hook's error", c, err)
	}
	if c != nil {
		t.Error("a failed Start returned a child")
	}
	if _, serr := os.Stat(pf); !os.IsNotExist(serr) {
		t.Errorf("a pidfile was written for a child that was stopped: %v", serr)
	}
	if hookPid == 0 {
		t.Fatal("the hook never ran")
	}
	// Start waited for the child it stopped, so the pid is no longer a process
	// of ours; a stopped child does not linger.
	if err := syscall.Kill(hookPid, 0); err == nil {
		t.Errorf("the child %d is still running after the hook refused it", hookPid)
	}
}

func TestStartWithoutAHookWritesThePidfileAsBefore(t *testing.T) {
	spec, pf := sleepSpec(t)
	c, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	reap(t, c)
	data, err := os.ReadFile(pf)
	if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(c.Pid) {
		t.Errorf("pidfile = %q, %v", data, err)
	}
}
