package team

import (
	"os/exec"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/tmux"
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
		Roles: []api.Role{
			{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	// Reconcile should create 3 sessions
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeamRole("test-reconcile", "agents", "worker")
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	for _, s := range sessions {
		if s.State != api.SessionRunning {
			t.Fatalf("session %s: expected running, got %s", s.Name, s.State)
		}
		if s.Role != "worker" {
			t.Fatalf("session %s: expected role worker, got %s", s.Name, s.Role)
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
		Roles: []api.Role{
			{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	// Scale up
	ctrl.ReconcileOnce()
	if len(store.ListSessionsByTeamRole("test-scaledown", "agents", "worker")) != 3 {
		t.Fatal("expected 3 sessions after scale up")
	}

	// Scale down
	team.Roles[0].Replicas = 1
	ctrl.ReconcileOnce()

	sessions := store.ListSessionsByTeamRole("test-scaledown", "agents", "worker")
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
		Roles: []api.Role{
			{Name: "worker", Replicas: 2, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	// Create initial sessions
	ctrl.ReconcileOnce()
	sessions := store.ListSessionsByTeamRole("test-replace", "agents", "worker")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Kill one session (simulating failure)
	if err := sessMgr.Delete(sessions[0].Key()); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	if len(store.ListSessionsByTeamRole("test-replace", "agents", "worker")) != 1 {
		t.Fatal("expected 1 session after kill")
	}

	// Reconcile should replace it
	ctrl.ReconcileOnce()

	sessions = store.ListSessionsByTeamRole("test-replace", "agents", "worker")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions after reconcile, got %d", len(sessions))
	}
}

func TestReconcileMultipleRoles(t *testing.T) {
	skipIfNoTmux(t)
	store, _, ctrl, cleanup := setup(t)
	t.Cleanup(cleanup)

	ws := &api.Workspace{Name: "test-multi", CreatedAt: time.Now().UTC()}
	if err := store.CreateWorkspace(ws); err != nil {
		t.Fatal(err)
	}

	team := &api.Team{
		Name:      "squad",
		Workspace: "test-multi",
		Roles: []api.Role{
			{Name: "supervisor", Replicas: 1, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
			{Name: "worker", Replicas: 3, Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateTeam(team); err != nil {
		t.Fatal(err)
	}

	ctrl.ReconcileOnce()

	supervisors := store.ListSessionsByTeamRole("test-multi", "squad", "supervisor")
	if len(supervisors) != 1 {
		t.Fatalf("expected 1 supervisor, got %d", len(supervisors))
	}
	if supervisors[0].Role != "supervisor" {
		t.Fatalf("expected role supervisor, got %s", supervisors[0].Role)
	}

	workers := store.ListSessionsByTeamRole("test-multi", "squad", "worker")
	if len(workers) != 3 {
		t.Fatalf("expected 3 workers, got %d", len(workers))
	}

	// Total sessions for the team
	all := store.ListSessionsByTeam("test-multi", "squad")
	if len(all) != 4 {
		t.Fatalf("expected 4 total sessions, got %d", len(all))
	}

	// Scale workers independently
	team.Roles[1].Replicas = 1
	ctrl.ReconcileOnce()

	workers = store.ListSessionsByTeamRole("test-multi", "squad", "worker")
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker after scale, got %d", len(workers))
	}

	// Supervisor unaffected
	supervisors = store.ListSessionsByTeamRole("test-multi", "squad", "supervisor")
	if len(supervisors) != 1 {
		t.Fatalf("expected 1 supervisor still, got %d", len(supervisors))
	}
}
