package team

import (
	"os/exec"
	"testing"
	"time"

	"github.com/arcaven/marvel/internal/api"
	"github.com/arcaven/marvel/internal/session"
	"github.com/arcaven/marvel/internal/tmux"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func setup(t *testing.T) (*api.Store, *session.Manager, *Controller, func()) {
	t.Helper()
	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	sessMgr := session.NewManager(store, driver)
	ctrl := NewController(store, sessMgr)

	cleanup := func() {
		for _, ws := range store.ListWorkspaces() {
			_ = sessMgr.CleanupWorkspace(ws.Name)
		}
	}

	return store, sessMgr, ctrl, cleanup
}

func TestReconcileScaleUp(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	ws := &api.Workspace{Name: "test-reconcile", CreatedAt: time.Now().UTC()}
	if err := store.CreateWorkspace(ws); err != nil {
		t.Fatal(err)
	}

	team := &api.Team{
		Name:      "agents",
		Workspace: "test-reconcile",
		Replicas:  3,
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	// Reconcile should create 3 sessions
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeam("test-reconcile", "agents")
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	for _, s := range sessions {
		if s.State != api.SessionRunning {
			t.Fatalf("session %s: expected running, got %s", s.Name, s.State)
		}
	}
}

func TestReconcileScaleDown(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	ws := &api.Workspace{Name: "test-scaledown", CreatedAt: time.Now().UTC()}
	if err := store.CreateWorkspace(ws); err != nil {
		t.Fatal(err)
	}

	team := &api.Team{
		Name:      "agents",
		Workspace: "test-scaledown",
		Replicas:  3,
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	// Scale up
	ctrl.ReconcileOnce()
	if len(store.ListSessionsByTeam("test-scaledown", "agents")) != 3 {
		t.Fatal("expected 3 sessions after scale up")
	}

	// Scale down
	team.Replicas = 1
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeam("test-scaledown", "agents")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after scale down, got %d", len(sessions))
	}
}

func TestReconcileReplaceDead(t *testing.T) {
	skipIfNoTmux(t)
	store, sessMgr, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	ws := &api.Workspace{Name: "test-replace", CreatedAt: time.Now().UTC()}
	if err := store.CreateWorkspace(ws); err != nil {
		t.Fatal(err)
	}

	team := &api.Team{
		Name:      "agents",
		Workspace: "test-replace",
		Replicas:  2,
		Runtime:   api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	// Create initial sessions
	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeam("test-replace", "agents")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Kill one session (simulating failure)
	if err := sessMgr.Delete(sessions[0].Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	if len(store.ListSessionsByTeam("test-replace", "agents")) != 1 {
		t.Fatal("expected 1 session after kill")
	}

	// Reconcile should replace it
	ctrl.ReconcileOnce()

	sessions = store.ListSessionsByTeam("test-replace", "agents")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions after reconcile, got %d", len(sessions))
	}
}
