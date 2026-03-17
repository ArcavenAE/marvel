package api

import (
	"errors"
	"testing"
	"time"
)

func TestWorkspaceCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	w := &Workspace{Name: "test-ws", CreatedAt: time.Now().UTC()}

	// Create
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Duplicate
	if err := s.CreateWorkspace(w); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	// Get
	got, err := s.GetWorkspace("test-ws")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if got.Name != "test-ws" {
		t.Fatalf("expected test-ws, got %s", got.Name)
	}

	// List
	list := s.ListWorkspaces()
	if len(list) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(list))
	}

	// Delete
	if err := s.DeleteWorkspace("test-ws"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	// Get after delete
	if _, err := s.GetWorkspace("test-ws"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSessionCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	sess := &Session{
		Name:      "worker-0",
		Workspace: "test-ws",
		Team:      "workers",
		Runtime:   Runtime{Name: "shell", Command: "bash"},
		State:     SessionPending,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := s.GetSession("test-ws/worker-0")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.State != SessionPending {
		t.Fatalf("expected pending, got %s", got.State)
	}

	// List by team
	teamSessions := s.ListSessionsByTeam("test-ws", "workers")
	if len(teamSessions) != 1 {
		t.Fatalf("expected 1 team session, got %d", len(teamSessions))
	}

	// Delete
	if err := s.DeleteSession("test-ws/worker-0"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetSession("test-ws/worker-0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete")
	}
}

func TestTeamCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	team := &Team{
		Name:      "workers",
		Workspace: "test-ws",
		Replicas:  3,
		Runtime:   Runtime{Name: "shell", Command: "bash"},
		CreatedAt: time.Now().UTC(),
	}

	if err := s.CreateTeam(team); err != nil {
		t.Fatalf("create team: %v", err)
	}

	got, err := s.GetTeam("test-ws/workers")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if got.Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", got.Replicas)
	}

	teams := s.ListTeams()
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}

	if err := s.DeleteTeam("test-ws/workers"); err != nil {
		t.Fatalf("delete team: %v", err)
	}
}

func TestEndpointCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	ep := &Endpoint{Name: "worker-svc", Workspace: "test-ws", Team: "workers"}

	if err := s.CreateEndpoint(ep); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	got, err := s.GetEndpoint("test-ws/worker-svc")
	if err != nil {
		t.Fatalf("get endpoint: %v", err)
	}
	if got.Team != "workers" {
		t.Fatalf("expected workers team, got %s", got.Team)
	}

	if err := s.DeleteEndpoint("test-ws/worker-svc"); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
}
