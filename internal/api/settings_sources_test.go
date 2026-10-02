package api

import (
	"strings"
	"testing"
)

// settings_sources on a role (docs/design/seat-bootstrap.md section 4, SB-2).

func settingsTOML(v string) string {
	return `
[workspace]
name = "ss"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1
  ` + v + `

    [team.role.runtime]
    command = "claude"
`
}

func settingsYAML(v string) string {
	return `
workspace:
  name: ss
teams:
  - name: squad
    roles:
      - name: worker
        replicas: 1
        ` + v + `
        runtime:
          command: claude
`
}

func TestSettingsSourcesParseAndApply(t *testing.T) {
	t.Parallel()
	for format, src := range map[string]string{
		"toml": settingsTOML(`settings_sources = ["user", "project", "local"]`),
		"yaml": settingsYAML(`settings_sources: [user, project, local]`),
	} {
		m, err := ParseManifestBytes([]byte(src))
		if err != nil {
			t.Fatalf("%s parse: %v", format, err)
		}
		want := "user,project,local"
		if got := strings.Join(m.Teams[0].Roles[0].SettingsSources, ","); got != want {
			t.Fatalf("%s parsed sources = %q, want %q", format, got, want)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("%s apply: %v", format, err)
		}
		team, err := store.GetTeam("ss/squad")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(team.Roles[0].SettingsSources, ","); got != want {
			t.Errorf("%s stored sources = %q, want %q", format, got, want)
		}
	}
}

func TestSettingsSourcesRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, toml, yaml, want string
	}{
		{"an unknown source", `settings_sources = ["user", "everything"]`, `settings_sources: [user, everything]`, "everything"},
		{"a duplicate", `settings_sources = ["user", "user"]`, `settings_sources: [user, user]`, "twice"},
		{"an empty list", `settings_sources = []`, `settings_sources: []`, "empty"},
	}
	for _, tc := range cases {
		for format, src := range map[string]string{"toml": settingsTOML(tc.toml), "yaml": settingsYAML(tc.yaml)} {
			_, err := ParseManifestBytes([]byte(src))
			if err == nil {
				t.Errorf("%s/%s: parse succeeded, want an error containing %q", tc.name, format, tc.want)
				continue
			}
			if !strings.Contains(err.Error(), "settings_sources") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s/%s: error %q, want it to name settings_sources and %q", tc.name, format, err, tc.want)
			}
		}
	}
}
