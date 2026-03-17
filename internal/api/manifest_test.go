package api

import (
	"testing"
)

const validManifest = `
[workspace]
name = "test-project"

[[team]]
name = "agents"
replicas = 3

  [team.runtime]
  command = "bash"
  args = ["-c", "while true; do sleep 1; done"]

[[team]]
name = "monitors"
replicas = 1

  [team.runtime]
  image = "top"
  command = "top"

[[endpoint]]
name = "agent-svc"
team = "agents"
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
	if len(m.Teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(m.Teams))
	}
	if m.Teams[0].Name != "agents" {
		t.Fatalf("expected team agents, got %s", m.Teams[0].Name)
	}
	if m.Teams[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", m.Teams[0].Replicas)
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
replicas = 1
  [team.runtime]
  command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for missing workspace name")
	}
}

func TestParseManifestBadReplicas(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"
replicas = 0
  [team.runtime]
  command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for zero replicas")
	}
}

func TestParseManifestWithRoleAndScript(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"
replicas = 2
role = "agent"

  [team.runtime]
  image = "simulator"
  command = "simulator"

[[team]]
name = "supervisor"
replicas = 1
role = "supervisor"

  [team.runtime]
  image = "simulator"
  command = "simulator"
  script = "scripts/chaos.lua"
`))
	if err != nil {
		t.Fatalf("parse manifest with role/script: %v", err)
	}
	if m.Teams[0].Role != "agent" {
		t.Fatalf("expected role agent, got %s", m.Teams[0].Role)
	}
	if m.Teams[1].Role != "supervisor" {
		t.Fatalf("expected role supervisor, got %s", m.Teams[1].Role)
	}
	if m.Teams[1].Runtime.Script != "scripts/chaos.lua" {
		t.Fatalf("expected script path, got %s", m.Teams[1].Runtime.Script)
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
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(teams))
	}

	agents, err := store.GetTeam("test-project/agents")
	if err != nil {
		t.Fatalf("get agents team: %v", err)
	}
	if agents.Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", agents.Replicas)
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
