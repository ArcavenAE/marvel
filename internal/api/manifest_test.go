package api

import (
	"testing"
)

const validManifest = `
[workspace]
name = "test-project"

[[team]]
name = "workers"
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
name = "worker-svc"
team = "workers"
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
	if m.Teams[0].Name != "workers" {
		t.Fatalf("expected team workers, got %s", m.Teams[0].Name)
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
name = "workers"
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
name = "workers"
replicas = 0
  [team.runtime]
  command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for zero replicas")
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

	workers, err := store.GetTeam("test-project/workers")
	if err != nil {
		t.Fatalf("get workers team: %v", err)
	}
	if workers.Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", workers.Replicas)
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
