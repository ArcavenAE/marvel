package tmuxtest

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// marvel#805: a server a killed test binary left stopped (SIGSTOP) never
// answers kill-server, and an unbounded client waits on it at full CPU. The
// sweep must give up on such a server within its deadline and move on.
func TestSweepNamesGivesUpOnAStoppedServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	// Under /tmp and not TMPDIR: a tmux socket path must fit in 104 bytes. The dir is private and removed below.
	dir, err := os.MkdirTemp("/tmp", "mxsw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)

	name := "marvel-test-stopped-" + strconv.Itoa(os.Getpid())
	startServer(t, name)

	// The server pid is read from the server itself, so only the process this
	// test started is ever signalled.
	out, err := exec.Command("tmux", "-L", name, "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("read server pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 1 {
		t.Fatalf("server pid %q: %v", out, err)
	}
	// Registered after startServer, so it runs before startServer's own
	// kill-server, which a stopped server would never answer.
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })

	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop server %d: %v", pid, err)
	}
	stopped := false
	for i := 0; i < 100 && !stopped; i++ {
		st, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		stopped = strings.Contains(string(st), "T")
		if !stopped {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !stopped {
		t.Fatalf("server %d did not stop", pid)
	}

	old := killServerDeadline
	killServerDeadline = 500 * time.Millisecond
	t.Cleanup(func() { killServerDeadline = old })

	done := make(chan []string, 1)
	go func() { done <- sweepNames([]string{name}) }()
	select {
	case swept := <-done:
		if len(swept) != 0 {
			t.Errorf("a stopped server was reported swept: %v", swept)
		}
	case <-time.After(15 * time.Second):
		// Let the stuck client exit so the test ends instead of leaving it
		// spinning, then fail.
		_ = syscall.Kill(pid, syscall.SIGCONT)
		t.Fatal("sweepNames did not return within 15s of a 500ms deadline: kill-server on a stopped server is unbounded")
	}
}
