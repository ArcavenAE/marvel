package api

import (
	"path/filepath"
	"strings"
	"testing"
)

const policyManifestTOML = `
[workspace]
name = "acme"

[[policy]]
name = "reviewer-contract"
version = "1.0"

  [policy.settings.permissions]
  allow = ["Read", "Grep"]
  deny = ["Bash"]

[[team]]
name = "squad"

  [[team.role]]
  name = "reviewer"
  replicas = 1
  policy = "reviewer-contract"

    [team.role.runtime]
    image = "claude"
    command = "claude"
`

func TestParseManifestPolicy(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(policyManifestTOML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Policies) != 1 {
		t.Fatalf("policies = %d, want 1", len(m.Policies))
	}
	p := m.Policies[0]
	if p.Name != "reviewer-contract" {
		t.Errorf("policy name = %q, want reviewer-contract", p.Name)
	}
	if p.Version != "1.0" {
		t.Errorf("policy version = %q, want 1.0", p.Version)
	}
	perms, ok := p.Settings["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("settings.permissions is %T, want map", p.Settings["permissions"])
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) != 2 {
		t.Fatalf("permissions.allow = %v, want two entries", perms["allow"])
	}
	if m.Teams[0].Roles[0].Policy != "reviewer-contract" {
		t.Errorf("role policy = %q, want reviewer-contract", m.Teams[0].Roles[0].Policy)
	}
}

func TestValidateManifestPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr string // substring; empty means expect success
	}{
		{
			name: "valid policy reference",
			body: policyManifestTOML,
		},
		{
			name: "policy without name",
			body: `
[workspace]
name = "acme"
[[policy]]
version = "1.0"
`,
			wantErr: "policy[0].name is required",
		},
		{
			name: "duplicate policy names",
			body: `
[workspace]
name = "acme"
[[policy]]
name = "dup"
[[policy]]
name = "dup"
`,
			wantErr: `policy[1].name "dup" is duplicated`,
		},
		{
			name: "role references undefined policy",
			body: `
[workspace]
name = "acme"
[[team]]
name = "squad"
  [[team.role]]
  name = "reviewer"
  replicas = 1
  policy = "ghost"
    [team.role.runtime]
    command = "claude"
`,
			wantErr: `references undefined policy "ghost"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseManifestBytes([]byte(tt.body))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestApplyPolicyCreatesAndUpdates(t *testing.T) {
	t.Parallel()
	store := NewStore()

	m, err := ParseManifestBytes([]byte(policyManifestTOML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := store.GetPolicy("acme/reviewer-contract")
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if got.Version != "1.0" {
		t.Errorf("version = %q, want 1.0", got.Version)
	}
	if got.Workspace != "acme" {
		t.Errorf("workspace = %q, want acme", got.Workspace)
	}

	// Re-apply an edited version: same name, new settings. Apply must
	// update in place rather than fail on already-exists.
	edited := strings.Replace(policyManifestTOML, `version = "1.0"`, `version = "2.0"`, 1)
	m2, err := ParseManifestBytes([]byte(edited))
	if err != nil {
		t.Fatalf("parse edited: %v", err)
	}
	if err := m2.Apply(store); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	got2, err := store.GetPolicy("acme/reviewer-contract")
	if err != nil {
		t.Fatalf("get policy after re-apply: %v", err)
	}
	if got2.Version != "2.0" {
		t.Errorf("version after re-apply = %q, want 2.0", got2.Version)
	}
	if n := len(store.ListPolicies()); n != 1 {
		t.Errorf("policy count = %d, want 1 (update in place, not duplicate)", n)
	}
}

func TestPolicyProjectionExamplesParse(t *testing.T) {
	t.Parallel()
	// Guard the demo manifests so a schema change that breaks them fails
	// here rather than in a live demo.
	for _, name := range []string{
		"policy-projection.toml", "policy-projection-v2.toml",
		"policy-projection.yaml", "policy-projection-v2.yaml",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("..", "..", "examples", name)
			m, err := ParseManifest(path)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			if len(m.Policies) == 0 {
				t.Fatalf("%s declares no policies", name)
			}
			if m.Teams[0].Roles[0].Policy == "" {
				t.Fatalf("%s role does not reference a policy", name)
			}
			store := NewStore()
			if err := m.Apply(store); err != nil {
				t.Fatalf("apply %s: %v", name, err)
			}
		})
	}
}

func TestStorePolicySnapshotIsolation(t *testing.T) {
	t.Parallel()
	store := NewStore()
	p := &Policy{
		Name:      "base",
		Workspace: "acme",
		Settings:  map[string]any{"permissions": map[string]any{"allow": []any{"Read"}}},
	}
	if err := store.CreatePolicy(p); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Mutating the caller's map after Create must not affect the store.
	p.Settings["permissions"] = "clobbered"

	got, err := store.GetPolicy("acme/base")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := got.Settings["permissions"].(map[string]any); !ok {
		t.Fatalf("store snapshot aliased caller state: permissions is %T", got.Settings["permissions"])
	}
	// Mutating the returned snapshot must not affect a later read.
	got.Settings["permissions"] = "clobbered-again"
	again, err := store.GetPolicy("acme/base")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if _, ok := again.Settings["permissions"].(map[string]any); !ok {
		t.Fatalf("returned snapshot aliased store state: permissions is %T", again.Settings["permissions"])
	}
}
