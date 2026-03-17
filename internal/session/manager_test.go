package session

import (
	"os/exec"
	"testing"

	"github.com/arcaven/marvel/internal/api"
	"github.com/arcaven/marvel/internal/tmux"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func TestSessionCreateDelete(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-sess-mgr"
	t.Cleanup(func() {
		_ = mgr.CleanupWorkspace(ws)
	})

	sess := &api.Session{
		Name:      "worker-0",
		Workspace: ws,
		Team:      "workers",
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}

	// Create
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.State != api.SessionRunning {
		t.Fatalf("expected running, got %s", sess.State)
	}
	if sess.PaneID == "" {
		t.Fatal("expected pane ID")
	}

	// Verify in store
	got, err := store.GetSession(ws + "/worker-0")
	if err != nil {
		t.Fatalf("get from store: %v", err)
	}
	if got.PaneID != sess.PaneID {
		t.Fatalf("store pane ID mismatch")
	}

	// Delete
	if err := mgr.Delete(ws + "/worker-0"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	// Verify gone from store
	sessions := store.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after delete, got %d", len(sessions))
	}
}

func TestCleanupWorkspace(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)

	ws := "test-cleanup"
	t.Cleanup(func() {
		_ = mgr.CleanupWorkspace(ws)
	})

	// Create two sessions
	for _, name := range []string{"w-0", "w-1"} {
		sess := &api.Session{
			Name:      name,
			Workspace: ws,
			Team:      "workers",
			Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		}
		if err := mgr.Create(sess); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	if len(store.ListSessions()) != 2 {
		t.Fatal("expected 2 sessions")
	}

	// Cleanup
	if err := mgr.CleanupWorkspace(ws); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if len(store.ListSessions()) != 0 {
		t.Fatal("expected 0 sessions after cleanup")
	}

	if driver.HasSession("marvel-" + ws) {
		t.Fatal("tmux session should be gone after cleanup")
	}
}
