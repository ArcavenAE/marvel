package tmux

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
)

// A suspend stops the server first and then every pane's process, so the
// driver must name both while the server still answers. Real tmux, a private
// socket.
func TestServerPIDAndPanePIDs(t *testing.T) {
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxpid")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", "mxpid"+filepath.Base(dir))
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NewSession("probe"); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() { _ = d.KillServer() })
	pane, err := d.NewPane("probe", "sleep 60", "probe", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	panePid, err := d.PanePID(pane)
	if err != nil {
		t.Fatal(err)
	}

	server, err := d.ServerPID()
	if err != nil {
		t.Fatalf("ServerPID: %v", err)
	}
	if server <= 1 {
		t.Fatalf("ServerPID = %d", server)
	}
	if err := syscall.Kill(server, 0); err != nil {
		t.Errorf("ServerPID %d is not a live process: %v", server, err)
	}
	pids, err := d.PanePIDs()
	if err != nil {
		t.Fatalf("PanePIDs: %v", err)
	}
	if !slices.Contains(pids, panePid) {
		t.Errorf("PanePIDs = %v, want it to hold pane pid %s", pids, strconv.Itoa(panePid))
	}
	if slices.Contains(pids, server) {
		t.Errorf("PanePIDs holds the server pid %d", server)
	}
}

// With no server running, neither call invents a pid.
func TestServerPIDWithNoServerIsAnError(t *testing.T) {
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxpid")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", "mxpid"+filepath.Base(dir))
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	if pid, err := d.ServerPID(); err == nil {
		t.Errorf("ServerPID = %d with no server, want an error", pid)
	}
	if pids, err := d.PanePIDs(); err == nil && len(pids) != 0 {
		t.Errorf("PanePIDs = %v with no server, want none", pids)
	}
}
