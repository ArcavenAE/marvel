package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// The declaration side of aae-orc-spk60: a role's manifest names the teams of
// its workspace that it supervises. These cover the field's values, that it
// reaches the stored role, and that it survives a restart. What the field
// grants on the broker is internal/bus's job.

// superviseManifest is a one-team manifest whose supervisor role declares
// supervises.
func superviseManifest(supervises ...string) *Manifest {
	return &Manifest{
		Workspace: ManifestWorkspace{Name: "dtu"},
		Teams: []ManifestTeam{{
			Name: "product",
			Roles: []ManifestRole{{
				Name:       "supervisor",
				Replicas:   1,
				Supervises: supervises,
				Runtime:    ManifestRuntime{Command: "bash"},
			}},
		}},
	}
}

// Valid names parse; anything that is not one address token, the role's own
// team, an empty name and a repeat are refused at parse, naming the key and
// the bad value, so a typo is an error and not a grant that silently names
// nothing.
func TestValidateManifestSuperviseValues(t *testing.T) {
	t.Parallel()
	for _, ok := range [][]string{nil, {"midway"}, {"midway", "vantage", "product-mythos"}} {
		if _, err := validateManifest(superviseManifest(ok...)); err != nil {
			t.Errorf("supervises %v refused: %v", ok, err)
		}
	}
	bad := map[string][]string{
		"Midway":      {"Midway"},
		"mid.way":     {"mid.way"},
		"mid way":     {"mid way"},
		"*":           {"*"},
		">":           {">"},
		"empty":       {""},
		"own team":    {"product"},
		"a repeat":    {"midway", "midway"},
		"a long name": {strings.Repeat("a", 33)},
	}
	for name, list := range bad {
		_, err := validateManifest(superviseManifest(list...))
		if err == nil {
			t.Errorf("supervises %v (%s) accepted, want a refusal", list, name)
			continue
		}
		if !strings.Contains(err.Error(), "supervises") {
			t.Errorf("supervises %v (%s): error %q does not name the key", list, name, err)
		}
	}
}

// The key parses in both manifest spellings.
func TestSupervisesParsesFromYAMLAndTOML(t *testing.T) {
	t.Parallel()
	yamlDoc := `workspace:
  name: dtu
teams:
  - name: product
    roles:
      - name: supervisor
        replicas: 1
        supervises: [midway, vantage]
        runtime:
          command: bash
`
	tomlDoc := `[workspace]
name = "dtu"

[[team]]
name = "product"

[[team.role]]
name = "supervisor"
replicas = 1
supervises = ["midway", "vantage"]

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
		if got := m.Teams[0].Roles[0].Supervises; len(got) != 2 || got[0] != "midway" || got[1] != "vantage" {
			t.Errorf("%s: parsed supervises = %v, want [midway vantage]", name, got)
		}
		// The parsed key must reach the stored role, not stop at the manifest.
		s := NewStore()
		if err := m.Apply(s); err != nil {
			t.Fatalf("%s: apply: %v", name, err)
		}
		team, err := s.GetTeam("dtu/product")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := team.Roles[0].Supervises; len(got) != 2 || got[0] != "midway" || got[1] != "vantage" {
			t.Errorf("%s: stored supervises = %v, want [midway vantage]", name, got)
		}
	}
}

// Apply copies the declaration to the stored role. The renderer reads the
// stored role, so a copy that dropped it would leave every declaration inert.
func TestApplyStoresSupervises(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := superviseManifest("midway", "vantage").Apply(s); err != nil {
		t.Fatal(err)
	}
	team, err := s.GetTeam("dtu/product")
	if err != nil {
		t.Fatal(err)
	}
	got := team.Roles[0].Supervises
	if len(got) != 2 || got[0] != "midway" || got[1] != "vantage" {
		t.Errorf("stored supervises = %v, want [midway vantage]", got)
	}
}

// The declaration survives a daemon restart: a start rehydrates teams from the
// store without an apply, and the renderer builds from those stored teams.
func TestBoltStoreSupervisesSurvivesRehydrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := superviseManifest("midway").Apply(s1); err != nil {
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
	team, err := s2.GetTeam("dtu/product")
	if err != nil {
		t.Fatal(err)
	}
	if got := team.Roles[0].Supervises; len(got) != 1 || got[0] != "midway" {
		t.Errorf("supervises after rehydrate = %v, want [midway]", got)
	}
}
