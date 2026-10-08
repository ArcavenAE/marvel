package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// freezeTmux starts a tmux server on the driver's socket and stops it with
// SIGSTOP, so every tmux call the daemon makes waits for its bound. The
// returned func resumes the server.
func freezeTmux(t *testing.T, d *Daemon) func() {
	t.Helper()
	if err := d.driver.NewSession("freeze-scratch"); err != nil {
		t.Fatalf("start tmux server: %v", err)
	}
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
	resume := func() { _ = syscall.Kill(pid, syscall.SIGCONT) }
	t.Cleanup(func() {
		resume()
		_ = exec.Command("tmux", "-L", d.driver.Socket(), "kill-server").Run()
	})
	return resume
}

// startFrozen starts a daemon whose tmux is stopped, so adoption waits out the
// driver's bound. The channel carries Start's result.
func startFrozen(t *testing.T, name string, bound time.Duration) (*Daemon, string, chan error) {
	t.Helper()
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	d.driver.SetExecTimeout(bound)
	freezeTmux(t, d)
	sock := testSocket(t, name)
	started := make(chan error, 1)
	go func() { started <- d.Start(sock) }()
	return d, sock, started
}

func send(t *testing.T, sock, method string, params any) *Response {
	t.Helper()
	req := Request{Method: method}
	if params != nil {
		req.Params = mustMarshal(t, params)
	}
	var resp *Response
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err = SendRequestWith(sock, req, DialOptions{Timeout: time.Second})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("%s while adopting: %v", method, err)
	}
	return resp
}

// A daemon starting under a hung tmux serves its read methods while it waits,
// refuses the rest with the typed starting error, and finishes starting once
// tmux has timed out (marvel#716).
func TestStartServesReadsWhileAdoptionWaitsOnTmux(t *testing.T) {
	skipIfNoTmux(t)
	d, sock, started := startFrozen(t, "test-start-frozen", 4*time.Second)
	t.Cleanup(func() {
		<-started
		d.Stop()
		_ = os.Remove(sock)
	})

	for _, m := range []struct {
		method string
		params any
	}{
		{"daemon.status", nil},
		{"get", map[string]string{"resource_type": "sessions"}},
		{"describe", map[string]string{"resource_type": "workspace", "name": "none"}},
		{"logs", nil},
		{"orphans", nil},
		{"bus.status", nil},
	} {
		resp := send(t, sock, m.method, m.params)
		if !resp.Starting {
			t.Errorf("%s: want the starting marker on the response, got %+v", m.method, resp)
		}
		if resp.Error == ErrDaemonStarting.Error() {
			t.Errorf("%s: a read method was refused while starting", m.method)
		}
	}

	var st DaemonStatus
	resp := send(t, sock, "daemon.status", nil)
	if err := json.Unmarshal(resp.Result, &st); err != nil || st.AdoptingSince == nil {
		t.Fatalf("daemon.status: want adopting_since while adopting, got %s (%v)", resp.Result, err)
	}

	// Refused before any handler runs: apply and plan change or read an
	// unreconciled store, inject and capture drive tmux, and heartbeat and
	// account.limits are refused retryably so they never count as a failure.
	for _, method := range []string{
		"apply", "delete", "scale", "converge", "reap", "run", "shift",
		"reset-health", "inject", "capture", "heartbeat", "account.limits", "plan", "view.refresh",
		"credential.put", "credential.get", "credential.reveal", "credential.delete",
		"backend.verify", "bus.leaf.connect", "bus.leaf.disconnect", "reexec",
	} {
		resp := send(t, sock, method, map[string]any{})
		if resp.Error != ErrDaemonStarting.Error() || !resp.Starting {
			t.Errorf("%s while adopting: want the typed starting refusal, got %+v", method, resp)
		}
	}
	select {
	case e := <-started:
		t.Fatalf("Start returned (%v) before the adoption bound; the test did not observe the wait", e)
	default:
	}

	if e := <-started; e != nil {
		t.Fatalf("Start: %v", e)
	}
	started <- nil
	resp = send(t, sock, "daemon.status", nil)
	if resp.Error != "" || resp.Starting {
		t.Fatalf("daemon.status after start: want no starting marker, got %+v", resp)
	}
	st = DaemonStatus{}
	if err := json.Unmarshal(resp.Result, &st); err != nil || st.AdoptingSince != nil {
		t.Fatalf("daemon.status after start: want no adopting_since, got %s (%v)", resp.Result, err)
	}
}

// A shutdown that arrives while adoption waits ends the start instead of
// letting loops start behind it, and the daemon does not wait out tmux first.
func TestShutdownDuringAdoptionEndsTheStart(t *testing.T) {
	skipIfNoTmux(t)
	d, sock, started := startFrozen(t, "test-stop-starting", 4*time.Second)
	t.Cleanup(func() { _ = os.Remove(sock) })

	send(t, sock, "daemon.status", nil)
	d.Stop()
	select {
	case e := <-started:
		if !errors.Is(e, errStoppedWhileStarting) {
			t.Fatalf("Start: want errStoppedWhileStarting, got %v", e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Start did not return after a shutdown during adoption")
	}
}

// A plain stop is answered while adoption is blocked on tmux, and it detaches:
// the daemon leaves through the shutdown path and the panes keep running. A
// stop with teardown is refused, because it would destroy panes adoption has
// not finished accounting for (marvel#716).
func TestStopDuringBlockedAdoptionDetachesAndTeardownIsRefused(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	d.driver.SetExecTimeout(4 * time.Second)
	exited := make(chan int, 1)
	d.exit = func(code int) { exited <- code }

	if err := d.driver.NewSession("marvel-stop-keep"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	paneID, err := d.driver.NewPane("marvel-stop-keep", "sleep 300", "keep", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	resume := freezeTmux(t, d)
	sock := testSocket(t, "test-stop-detach")
	started := make(chan error, 1)
	go func() { started <- d.Start(sock) }()

	resp := send(t, sock, "stop", map[string]any{"teardown": true})
	if resp.Error != ErrDaemonStarting.Error() {
		t.Fatalf("stop --teardown while adopting: want the typed starting refusal, got %+v", resp)
	}
	if resp = send(t, sock, "daemon.status", nil); resp.Error != "" {
		t.Fatalf("the refused teardown stopped the daemon: %+v", resp)
	}

	resp = send(t, sock, "stop", nil)
	if resp.Error != "" {
		t.Fatalf("plain stop while adopting: %+v", resp)
	}
	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("exit code %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not leave after a plain stop during adoption")
	}
	// Shutdown ran before the exit: it releases the socket.
	if _, err := os.Stat(sock); err == nil {
		t.Fatal("socket still present: the exit did not follow the shutdown path")
	}
	select {
	case e := <-started:
		if !errors.Is(e, errStoppedWhileStarting) {
			t.Fatalf("Start: want errStoppedWhileStarting, got %v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the stop")
	}

	resume()
	deadline := time.Now().Add(5 * time.Second)
	for !d.driver.HasPane(paneID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !d.driver.HasPane(paneID) {
		t.Fatalf("pane %s did not survive a detaching stop", paneID)
	}
}
