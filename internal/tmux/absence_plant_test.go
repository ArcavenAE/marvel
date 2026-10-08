package tmux

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// plantServer starts a real tmux server on its own -L name under a scratch
// TMUX_TMPDIR and returns the driver, the pane, the socket path and the
// server pid. The server is stopped by pid on cleanup, because a test may
// remove its socket file, which leaves KillServer unable to reach it.
func plantServer(t *testing.T) (d *Driver, pane, sock string, pid int) {
	t.Helper()
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxabs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", "mxabs"+filepath.Base(dir))
	d, err = NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NewSession("probe"); err != nil {
		t.Fatalf("start server: %v", err)
	}
	pane, err = d.NewPane("probe", "sleep 60", "probe", nil, false)
	if err != nil {
		t.Fatalf("start a pane: %v", err)
	}
	out, err := d.cmd("display-message", "-p", "#{socket_path}\t#{pid}").Output()
	if err != nil {
		t.Fatalf("ask the server for its socket and pid: %v", err)
	}
	f := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(f) != 2 {
		t.Fatalf("socket and pid %q", out)
	}
	pid, err = strconv.Atoi(f[1])
	if err != nil || pid <= 1 {
		t.Fatalf("server pid %q: %v", f[1], err)
	}
	t.Cleanup(func() { _ = d.KillServer() }) // a server a failing test started on a fresh socket
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGCONT)
		_ = syscall.Kill(pid, syscall.SIGTERM)
	})
	return d, pane, f[0], pid
}

// serversNamed counts tmux server processes carrying this driver's -L name.
func serversNamed(t *testing.T, d *Driver) int {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid,args").Output()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if carriesSocket(strings.Fields(line), d.socket) {
			n++
		}
	}
	return n
}

// With the server alive and its socket file gone, create-if-absent must
// refuse: it used to read the failed has-session as "no session" and start a
// second server on the same name.
func TestNewSessionRefusesWhenALiveServerLostItsSocketFile(t *testing.T) {
	d, _, sock, _ := plantServer(t)
	if err := os.Remove(sock); err != nil {
		t.Fatal(err)
	}
	if err := d.NewSession("s"); err == nil {
		t.Error("NewSession returned nil with the server alive and its socket file gone; want an error")
	}
	if n := serversNamed(t, d); n != 1 {
		t.Errorf("%d tmux servers carry this name, want 1", n)
	}
}

// A stopped server with a full listen backlog refuses connections, and tmux
// then prints "no server running" for a server that is alive. Every caller
// must read that as an outage. The backlog is filled with raw connections
// the stopped server never accepts.
func TestStoppedServerWithAFullBacklogIsAnOutageForEveryCaller(t *testing.T) {
	d, pane, sock, pid := plantServer(t)
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	d.SetExecTimeout(300 * time.Millisecond)
	refused := false
	for i := 0; i < 1000 && !refused; i++ {
		c, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err != nil {
			refused = true
			break
		}
		conns = append(conns, c)
	}
	if !refused {
		t.Skip("could not fill the listen backlog on this host")
	}
	if names, err := d.ListSessions(); err == nil {
		t.Errorf("ListSessions = %v, nil; want an error", names)
	}
	if st, err := d.PaneStatus(pane); err == nil {
		t.Errorf("PaneStatus = %+v, nil; want an error, not a pane that reaps", st)
	}
	if err := d.KillPane(pane); err == nil || errors.Is(err, ErrPaneGone) {
		t.Errorf("KillPane = %v; want an outage error, not ErrPaneGone", err)
	}
	if err := d.NewSession("s"); err == nil {
		t.Error("NewSession returned nil on a stopped server")
	}
	if n := serversNamed(t, d); n != 1 {
		t.Errorf("%d tmux servers carry this name, want 1", n)
	}
}
