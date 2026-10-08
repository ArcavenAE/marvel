package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// A stopped tmux server is an outage, not a fleet of vanished panes: ReapDead
// must leave a running session exactly as it was (marvel#715).
func TestReapDeadLeavesSessionsAloneWhenTmuxHangs(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-reap-hung"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })

	sess := &api.Session{
		Name: "w-0", Workspace: ws, Team: "agents", Role: "worker",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	before, err := store.GetSession(ws + "/w-0")
	if err != nil || before.PaneID == "" {
		t.Fatalf("session not running: %+v, %v", before, err)
	}

	out, err := exec.Command("tmux", "-L", driver.Socket(), "display-message", "-p", "#{pid}").Output()
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
	// Registered after the workspace cleanup, so it runs first.
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
	driver.SetExecTimeout(300 * time.Millisecond)

	done := make(chan []ReapedSession, 1)
	go func() { done <- mgr.ReapDead() }()
	select {
	case reaped := <-done:
		if len(reaped) != 0 {
			t.Fatalf("a hung tmux reaped sessions: %v", reaped)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ReapDead did not return while tmux was stopped")
	}
	after, err := store.GetSession(ws + "/w-0")
	if err != nil {
		t.Fatalf("session vanished: %v", err)
	}
	if after.State != before.State || after.PaneID != before.PaneID {
		t.Fatalf("session changed under a hung tmux: before %s/%s, after %s/%s",
			before.State, before.PaneID, after.State, after.PaneID)
	}
}

// fakeTmuxOnPath puts a tmux script first on PATH. It lists one marvel
// session and hangs on list-panes, so the preview reaches the per-session
// pane read and that read is the one that stalls.
func fakeTmuxOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$4\" in\nlist-sessions) echo marvel-ws-fake ;;\nlist-panes) sleep 60 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The reap preview must say it could not read a session's panes, not list
// the rest and leave that session out as though it held nothing.
func TestUnrecordedTmuxStateReportsATimeoutOnPaneRead(t *testing.T) {
	fakeTmuxOnPath(t)
	store := api.NewStore()
	if err := store.CreateWorkspace(&api.Workspace{Name: "ws-fake"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	driver.SetExecTimeout(300 * time.Millisecond)
	mgr := NewManager(store, driver)

	type result struct {
		found []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		f, e := mgr.UnrecordedTmuxState()
		done <- result{f, e}
	}()
	select {
	case r := <-done:
		if r.err == nil || !strings.Contains(r.err.Error(), tmux.ErrTmuxTimeout.Error()) {
			t.Fatalf("want an error naming the tmux timeout, got found=%v err=%v", r.found, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UnrecordedTmuxState did not return")
	}
}
