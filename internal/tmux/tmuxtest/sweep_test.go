package tmuxtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// deadPid returns the pid of a process that has exited.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	return pid
}

func startServer(t *testing.T, name string) {
	t.Helper()
	if out, err := exec.Command("tmux", "-L", name, "new-session", "-d", "-s", "p", "sleep 120").CombinedOutput(); err != nil {
		t.Fatalf("start %s: %v: %s", name, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", name, "kill-server").Run() })
}

func serverUp(name string) bool {
	return exec.Command("tmux", "-L", name, "has-session", "-t", "p").Run() == nil
}

// A server whose test binary is gone is swept; a server named for a live
// test binary, and a server with any other name, are left alone.
func TestSweepDeadStopsOnlyDeadTestServers(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	dir, err := os.MkdirTemp("/tmp", "mxsw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)

	dead := "marvel-test-sweepdead-" + strconv.Itoa(deadPid(t))
	live := "marvel-test-sweeplive-" + strconv.Itoa(os.Getpid())
	other := "other-sweep-" + strconv.Itoa(deadPid(t))
	for _, n := range []string{dead, live, other} {
		startServer(t, n)
	}

	swept := SweepDead()

	// tmux exits on its own a moment after kill-server; poll rather than race it.
	deadline := time.Now().Add(5 * time.Second)
	for serverUp(dead) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if serverUp(dead) {
		t.Errorf("the server of a finished test binary (%s) is still running", dead)
	}
	if !serverUp(live) {
		t.Errorf("the server named for this live test binary (%s) was swept", live)
	}
	if !serverUp(other) {
		t.Errorf("a server outside the marvel-test-<package>-<pid> names (%s) was swept", other)
	}
	found := false
	for _, n := range swept {
		if n == dead {
			found = true
		}
		if n == live || n == other {
			t.Errorf("SweepDead reported %s as swept", n)
		}
	}
	if !found {
		t.Errorf("SweepDead returned %v, want it to include %s", swept, dead)
	}
	if _, err := os.Stat(filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), dead)); !os.IsNotExist(err) {
		t.Errorf("the swept server's socket file is still there (stat err %v)", err)
	}
}
