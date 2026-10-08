package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A daemon asked to detach while its tmux server is frozen must still exit:
// shutdown waits for the loops, and each of them reaches tmux only through
// the driver's bounded exec (marvel#715).
func TestDetachReturnsWhileTmuxIsFrozen(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	d.driver.SetExecTimeout(300 * time.Millisecond)
	sock := testSocket(t, "test-detach-frozen")
	if err := d.Start(sock); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		d.Stop()
		_ = os.Remove(sock)
	})

	manifest := `
[workspace]
name = "test-detach-frozen"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`
	resp, err := SendRequest(sock, Request{
		Method: "apply",
		Params: mustMarshal(t, map[string]any{"manifest_data": []byte(manifest)}),
	})
	if err != nil || resp.Error != "" {
		t.Fatalf("apply: err=%v resp.Error=%q", err, resp.Error)
	}
	time.Sleep(600 * time.Millisecond)

	out, err := exec.Command("tmux", "-L", d.driver.Socket(), "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("read tmux server pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse server pid %q: %v", out, err)
	}
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop tmux server: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
	stopped = true
	// Past one reconcile interval, so a loop is inside a tmux exec when the
	// detach starts.
	time.Sleep(3 * time.Second)

	done := make(chan struct{})
	go func() { defer close(done); d.Detach() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Detach did not return while tmux was frozen")
	}

	// Resume, then tear the left-running session down so no sleep outlives
	// the test.
	_ = syscall.Kill(pid, syscall.SIGCONT)
	_ = exec.Command("tmux", "-L", d.driver.Socket(), "kill-server").Run()
	_ = os.Remove(sock)
}
