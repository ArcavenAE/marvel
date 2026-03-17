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
		Name:      "agent-0",
		Workspace: "test-ws",
		Team:      "agents",
		Runtime:   Runtime{Name: "shell", Command: "bash"},
		State:     SessionPending,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := s.GetSession("test-ws/agent-0")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.State != SessionPending {
		t.Fatalf("expected pending, got %s", got.State)
	}

	// List by team
	teamSessions := s.ListSessionsByTeam("test-ws", "agents")
	if len(teamSessions) != 1 {
		t.Fatalf("expected 1 team session, got %d", len(teamSessions))
	}

	// Delete
	if err := s.DeleteSession("test-ws/agent-0"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetSession("test-ws/agent-0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete")
	}
}

func TestTeamCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	team := &Team{
		Name:      "agents",
		Workspace: "test-ws",
		Replicas:  3,
		Runtime:   Runtime{Name: "shell", Command: "bash"},
		CreatedAt: time.Now().UTC(),
	}

	if err := s.CreateTeam(team); err != nil {
		t.Fatalf("create team: %v", err)
	}

	got, err := s.GetTeam("test-ws/agents")
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

	if err := s.DeleteTeam("test-ws/agents"); err != nil {
		t.Fatalf("delete team: %v", err)
	}
}

func TestEndpointCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	ep := &Endpoint{Name: "agent-svc", Workspace: "test-ws", Team: "agents"}

	if err := s.CreateEndpoint(ep); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	got, err := s.GetEndpoint("test-ws/agent-svc")
	if err != nil {
		t.Fatalf("get endpoint: %v", err)
	}
	if got.Team != "agents" {
		t.Fatalf("expected agents team, got %s", got.Team)
	}

	if err := s.DeleteEndpoint("test-ws/agent-svc"); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
}

func TestUpdateSessionHeartbeat(t *testing.T) {
	t.Parallel()
	s := NewStore()

	sess := &Session{
		Name:      "agent-0",
		Workspace: "test-ws",
		Team:      "agents",
		Runtime:   Runtime{Name: "simulator", Command: "simulator"},
		State:     SessionRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := s.UpdateSessionHeartbeat("test-ws/agent-0", 42.5); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession("test-ws/agent-0")
	if got.ContextPercent != 42.5 {
		t.Fatalf("expected context 42.5%%, got %.1f%%", got.ContextPercent)
	}
	if got.LastHeartbeat.IsZero() {
		t.Fatal("expected non-zero heartbeat timestamp")
	}

	// Not found case
	if err := s.UpdateSessionHeartbeat("test-ws/nonexistent", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
