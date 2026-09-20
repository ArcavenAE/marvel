package api

import "testing"

// TestParseManifestBackendEnv proves the Layer B declaration surface parses
// from a TOML manifest and lands on the role's runtime: the per-role Env map
// and the intended Backend label (design-backend-swaps.md, BT1).
func TestParseManifestBackendEnv(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"
    backend = "bedrock"

      [team.role.runtime.env]
      CLAUDE_CODE_USE_BEDROCK = "1"
      ANTHROPIC_API_KEY = ""
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatalf("team test/squad not found: %v", err)
	}
	rt := team.Roles[0].Runtime
	if rt.Backend != "bedrock" {
		t.Errorf("backend = %q, want %q", rt.Backend, "bedrock")
	}
	if got := rt.Env["CLAUDE_CODE_USE_BEDROCK"]; got != "1" {
		t.Errorf("env[CLAUDE_CODE_USE_BEDROCK] = %q, want %q", got, "1")
	}
	// An empty value is meaningful (pin a competing key OFF), so it must
	// survive parsing rather than being dropped.
	if got, ok := rt.Env["ANTHROPIC_API_KEY"]; !ok || got != "" {
		t.Errorf("env[ANTHROPIC_API_KEY] = %q (present=%v), want empty and present", got, ok)
	}
}

// TestParseManifestBackendDefaults confirms a role with no backend surface
// carries a nil Env and an empty Backend, so nothing is inferred.
func TestParseManifestBackendDefaults(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	rt := team.Roles[0].Runtime
	if rt.Backend != "" {
		t.Errorf("backend = %q, want empty", rt.Backend)
	}
	if rt.Env != nil {
		t.Errorf("env = %v, want nil", rt.Env)
	}
}
