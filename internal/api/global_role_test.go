package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// The declaration side of docs/design/global-role-declaration.md: a role's
// manifest names the global role it holds. These cover the field's values,
// that it reaches the stored role, and that it survives a restart. Which
// roles actually hold the global tier is the resolver's job (internal/config).

// globalRoleManifest is a one-team manifest whose one role is a
// research-supervisor, the role the design is about, declaring globalRole.
func globalRoleManifest(globalRole string) *Manifest {
	return &Manifest{
		Workspace: ManifestWorkspace{Name: "demo"},
		Teams: []ManifestTeam{{
			Name: "ops",
			Roles: []ManifestRole{{
				Name:       "research-supervisor",
				Replicas:   1,
				GlobalRole: globalRole,
				Runtime:    ManifestRuntime{Command: "bash"},
			}},
		}},
	}
}

// Test 5: the value is the address word or "none". Empty is absent. Anything
// else is refused at parse, naming the bad value and the allowed set, so a
// typo is an error and not a role that silently holds nothing.
func TestValidateManifestGlobalRoleValues(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", GlobalRoleSupervisor, GlobalRoleNone} {
		if _, err := validateManifest(globalRoleManifest(ok)); err != nil {
			t.Errorf("global_role %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"director", "Supervisor", "worker", " supervisor", "true"} {
		_, err := validateManifest(globalRoleManifest(bad))
		if err == nil {
			t.Errorf("global_role %q accepted, want a refusal", bad)
			continue
		}
		msg := err.Error()
		for _, want := range []string{"global_role", bad, GlobalRoleSupervisor, GlobalRoleNone} {
			if !strings.Contains(msg, want) {
				t.Errorf("global_role %q: error %q lacks %q", bad, msg, want)
			}
		}
	}
}

// The key parses in both manifest spellings.
func TestGlobalRoleParsesFromYAMLAndTOML(t *testing.T) {
	t.Parallel()
	yamlDoc := `workspace:
  name: demo
teams:
  - name: ops
    roles:
      - name: research-supervisor
        replicas: 1
        global_role: supervisor
        runtime:
          command: bash
`
	tomlDoc := `[workspace]
name = "demo"

[[team]]
name = "ops"

[[team.role]]
name = "research-supervisor"
replicas = 1
global_role = "supervisor"

[team.role.runtime]
command = "bash"
`
	for name, doc := range map[string]string{"yaml": yamlDoc, "toml": tomlDoc} {
		m, err := ParseManifestBytes([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(m.Teams) != 1 || len(m.Teams[0].Roles) != 1 {
			t.Fatalf("%s: parsed %+v, want one team with one role", name, m.Teams)
		}
		if got := m.Teams[0].Roles[0].GlobalRole; got != GlobalRoleSupervisor {
			t.Errorf("%s: parsed global_role = %q, want %q", name, got, GlobalRoleSupervisor)
		}
	}
}

// Apply copies the declaration to the stored role, as it does Persona and
// Identity. The resolver reads the stored role, so a copy that dropped it would
// leave every declaration inert.
func TestApplyStoresTheGlobalRole(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := globalRoleManifest(GlobalRoleSupervisor).Apply(s); err != nil {
		t.Fatal(err)
	}
	team, err := s.GetTeam("demo/ops")
	if err != nil {
		t.Fatal(err)
	}
	if got := team.Roles[0].GlobalRole; got != GlobalRoleSupervisor {
		t.Errorf("stored global_role = %q, want %q", got, GlobalRoleSupervisor)
	}
}

// Test 8: the declaration survives a daemon restart. A daemon start rehydrates
// teams from the store without an apply, and the renderer builds from those
// stored teams, so a declaration that did not persist would vanish at restart.
func TestBoltStoreGlobalRoleSurvivesRehydrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := globalRoleManifest(GlobalRoleSupervisor).Apply(s1); err != nil {
		t.Fatal(err)
	}
	if err := s1.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	team, err := s2.GetTeam("demo/ops")
	if err != nil {
		t.Fatal(err)
	}
	if got := team.Roles[0].GlobalRole; got != GlobalRoleSupervisor {
		t.Errorf("global_role after rehydrate = %q, want %q", got, GlobalRoleSupervisor)
	}
}
