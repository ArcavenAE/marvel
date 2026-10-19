package api

import (
	"strings"
	"testing"
)

// writes on a role names the functions it writes, for the exposure gate
// (marvel#945). The settings_sources helpers build the manifest.

func TestWritesParseAndApply(t *testing.T) {
	t.Parallel()
	for format, src := range map[string]string{
		"toml": settingsTOML(`writes = ["parse", "emit"]`),
		"yaml": settingsYAML(`writes: [parse, emit]`),
	} {
		m, err := ParseManifestBytes([]byte(src))
		if err != nil {
			t.Fatalf("%s parse: %v", format, err)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("%s apply: %v", format, err)
		}
		team, err := store.GetTeam("ss/squad")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(team.Roles[0].Writes, ","); got != "parse,emit" {
			t.Errorf("%s stored writes = %q, want parse,emit", format, got)
		}
	}
	m, err := ParseManifestBytes([]byte(settingsTOML(``)))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Teams[0].Roles[0].Writes) != 0 {
		t.Errorf("a role with no key has writes %v, want none", m.Teams[0].Roles[0].Writes)
	}
}

func TestWritesRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, toml, yaml, want string }{
		{"an empty entry", `writes = ["parse", " "]`, `writes: [parse, " "]`, "empty"},
		{"a duplicate", `writes = ["parse", "parse"]`, `writes: [parse, parse]`, "twice"},
		{"a duplicate in another case", `writes = ["parse", "Parse"]`, `writes: [parse, Parse]`, "twice"},
		{"a duplicate past edge space", `writes = ["parse", "parse "]`, `writes: [parse, "parse "]`, "twice"},
	}
	for _, tc := range cases {
		for format, src := range map[string]string{"toml": settingsTOML(tc.toml), "yaml": settingsYAML(tc.yaml)} {
			_, err := ParseManifestBytes([]byte(src))
			if err == nil || !strings.Contains(err.Error(), "writes") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s/%s: error %v, want it to name writes and %q", tc.name, format, err, tc.want)
			}
		}
	}
}

// TestWritesAreStoredNormalized: " Parse" is the function parse, so the stored
// role carries the one spelling the gate compares.
func TestWritesAreStoredNormalized(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(settingsTOML(`writes = [" Parse", "EMIT "]`)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	team, _ := store.GetTeam("ss/squad")
	if got := strings.Join(team.Roles[0].Writes, ","); got != "parse,emit" {
		t.Errorf("stored writes = %q, want parse,emit", got)
	}
}
