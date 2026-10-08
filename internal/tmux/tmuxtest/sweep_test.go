package tmuxtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// serverProcess reports whether a tmux server process started with -L name is
// still in the process list. Removing a server's socket file hides it from
// has-session while the process runs on, so a check of the socket alone cannot
// tell a stopped server from an orphaned one.
func serverProcess(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "args=").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "tmux") && strings.Contains(line, "-L "+name+" ") {
			return true
		}
	}
	return false
}

// startSocketServer starts a server addressed by socket path (-S), whose
// command text carries a "-L <name>" that is only text.
func startSocketServer(t *testing.T, sock, text string) {
	t.Helper()
	if out, err := exec.Command("tmux", "-S", sock, "new-session", "-d", "-s", "p", "sleep 120 # "+text).CombinedOutput(); err != nil {
		t.Fatalf("start -S server: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
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
	for end := time.Now().Add(5 * time.Second); serverProcess(t, dead) && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
	if serverProcess(t, dead) {
		t.Errorf("the server process of a finished test binary (%s) outlived the sweep", dead)
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

// Names that are close to the test naming, with a dead pid in each, must be
// left alone: a looser pattern, a missing anchor or a -L read from anywhere in
// the arguments would each stop one of them.
func TestSweepDeadLeavesNamesThatOnlyLookLikeTestServers(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	dir, err := os.MkdirTemp("/tmp", "mxsw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)

	pid := strconv.Itoa(deadPid(t))
	noTest := "marvel-sweepx-" + pid
	prefixed := "xmarvel-test-sweepprefix-" + pid
	for _, n := range []string{noTest, prefixed} {
		startServer(t, n)
	}
	sock := filepath.Join(dir, "by-path.sock")
	startSocketServer(t, sock, "-L marvel-test-sweeptext-"+pid)

	swept := SweepDead()

	// A wrongly selected server would be stopped within a moment; give it that
	// moment before concluding it was left alone.
	time.Sleep(500 * time.Millisecond)
	for _, n := range []string{noTest, prefixed} {
		if !serverUp(n) {
			t.Errorf("%s does not follow marvel-test-<package>-<pid> and was swept", n)
		}
	}
	if exec.Command("tmux", "-S", sock, "has-session", "-t", "p").Run() != nil {
		t.Error("a -S server whose command text mentions -L marvel-test-... was swept")
	}
	if len(swept) != 0 {
		t.Errorf("SweepDead reported %v, want nothing swept", swept)
	}
}

// A server this process cannot reach (here, under another TMUX_TMPDIR) is
// selected by name but not stopped, so it must not be reported as swept.
func TestSweepDeadReportsOnlyServersItStopped(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	other, err := os.MkdirTemp("/tmp", "mxsw")
	if err != nil {
		t.Fatal(err)
	}
	mine, err := os.MkdirTemp("/tmp", "mxsw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other); _ = os.RemoveAll(mine) })

	name := "marvel-test-sweepelsewhere-" + strconv.Itoa(deadPid(t))
	t.Setenv("TMUX_TMPDIR", other)
	startServer(t, name)
	t.Setenv("TMUX_TMPDIR", mine)

	swept := SweepDead()
	for _, n := range swept {
		if n == name {
			t.Errorf("%s is under another TMUX_TMPDIR, was not stopped, and was reported as swept", name)
		}
	}
	t.Setenv("TMUX_TMPDIR", other)
	if !serverUp(name) {
		t.Errorf("the server under the other directory should still be running")
	}
}

// -L counts only among the server's own flags, before its command word. A
// "-L marvel-test-<package>-<pid>" later in the line is command text, and the
// -S form (a server addressed by path) has no -L name to sweep at all.
func TestDeadTestServerNamesReadsLOnlyFromTheServerFlags(t *testing.T) {
	pid := strconv.Itoa(deadPid(t))
	ps := "" +
		"101 tmux -L marvel-test-real-" + pid + " new-session -d -s p sleep 120\n" +
		"102 /opt/homebrew/bin/tmux -L marvel-test-abs-" + pid + " new-session -d -s p sleep 120\n" +
		"103 tmux -S /tmp/x/sock new-session -d -s p sleep 120 # -L marvel-test-text-" + pid + "\n" +
		"104 tmux new-session -d -s p sleep 120 -L marvel-test-late-" + pid + "\n" +
		"105 sh -c tmux -L marvel-test-shell-" + pid + " new-session\n"
	got := deadTestServerNames(ps)
	want := map[string]bool{"marvel-test-real-" + pid: true, "marvel-test-abs-" + pid: true}
	if len(got) != len(want) {
		t.Fatalf("selected %v, want only %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("selected %s, which is not a server's own -L flag", n)
		}
	}
}
