package api

import (
	"testing"
)

const validManifest = `
[workspace]
name = "test-project"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 3

    [team.role.runtime]
    command = "bash"
    args = ["-c", "while true; do sleep 1; done"]

  [[team.role]]
  name = "monitor"
  replicas = 1

    [team.role.runtime]
    image = "top"
    command = "top"

[[endpoint]]
name = "agent-svc"
team = "squad"
`

func TestParseManifest(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse valid manifest: %v", err)
	}
	if m.Workspace.Name != "test-project" {
		t.Fatalf("expected workspace test-project, got %s", m.Workspace.Name)
	}
	if len(m.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(m.Teams))
	}
	if m.Teams[0].Name != "squad" {
		t.Fatalf("expected team squad, got %s", m.Teams[0].Name)
	}
	if len(m.Teams[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(m.Teams[0].Roles))
	}
	if m.Teams[0].Roles[0].Name != "worker" {
		t.Fatalf("expected role worker, got %s", m.Teams[0].Roles[0].Name)
	}
	if m.Teams[0].Roles[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", m.Teams[0].Roles[0].Replicas)
	}
	if len(m.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(m.Endpoints))
	}
}

func TestParseManifestMissingWorkspace(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[[team]]
name = "agents"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for missing workspace name")
	}
}

func TestParseManifestNoRoles(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"
`))
	if err == nil {
		t.Fatal("expected error for team with no roles")
	}
}

func TestParseManifestBadReplicas(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"

  [[team.role]]
  name = "worker"
  replicas = 0

    [team.role.runtime]
    command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for zero replicas")
	}
}

func TestParseManifestMultipleRoles(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "supervisor"
  replicas = 1

    [team.role.runtime]
    image = "simulator"
    command = "simulator"
    script = "scripts/chaos.lua"

  [[team.role]]
  name = "worker"
  replicas = 5

    [team.role.runtime]
    image = "simulator"
    command = "simulator"
`))
	if err != nil {
		t.Fatalf("parse manifest with multiple roles: %v", err)
	}
	if len(m.Teams[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(m.Teams[0].Roles))
	}
	if m.Teams[0].Roles[0].Name != "supervisor" {
		t.Fatalf("expected supervisor, got %s", m.Teams[0].Roles[0].Name)
	}
	if m.Teams[0].Roles[0].Runtime.Script != "scripts/chaos.lua" {
		t.Fatalf("expected script path, got %s", m.Teams[0].Roles[0].Runtime.Script)
	}
	if m.Teams[0].Roles[1].Replicas != 5 {
		t.Fatalf("expected 5 replicas, got %d", m.Teams[0].Roles[1].Replicas)
	}
}

func TestManifestApply(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Workspace created
	if _, err := store.GetWorkspace("test-project"); err != nil {
		t.Fatalf("workspace not created: %v", err)
	}

	// Teams created
	teams := store.ListTeams()
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}

	squad, err := store.GetTeam("test-project/squad")
	if err != nil {
		t.Fatalf("get squad team: %v", err)
	}
	if len(squad.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(squad.Roles))
	}
	if squad.Roles[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas for worker, got %d", squad.Roles[0].Replicas)
	}
	if squad.Generation != 1 {
		t.Fatalf("expected generation 1 for new team, got %d", squad.Generation)
	}

	// Endpoint created
	eps := store.ListEndpoints()
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}

	// Idempotent re-apply
	if err := m.Apply(store); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}
