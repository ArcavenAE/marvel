package api

import (
	"slices"
	"strings"
	"testing"
)

// duties on a role names the readiness checks it must pass
// (docs/design/seat-readiness.md section 3). The settings_sources helpers
// build the manifest.

// TestDutyLibraryIsFixed pins the library. A role cannot invent a check, so
// growing this list is a design ruling and an edit here, never a manifest
// input.
func TestDutyLibraryIsFixed(t *testing.T) {
	t.Parallel()
	want := []string{"ack", "first-turn", "gh.comment", "gh.post", "gh.read", "jira.read", "jira.write"}
	if got := DutyNames(); !slices.Equal(got, want) {
		t.Errorf("DutyNames() = %v, want %v", got, want)
	}
	for _, d := range want {
		if !IsDuty(d) {
			t.Errorf("IsDuty(%q) = false, want true", d)
		}
		// Every library name is also a valid readiness cell name, so a
		// declared duty can always be recorded.
		if !dutyNameRE.MatchString(d) || len(d) > dutyNameMax {
			t.Errorf("library duty %q is not a valid readiness duty name", d)
		}
	}
	if IsDuty("gh.delete") {
		t.Error(`IsDuty("gh.delete") = true, want false: not in the library`)
	}
}

// TestDutyNamesIsACopy: a caller that changes the returned slice does not
// change the library.
func TestDutyNamesIsACopy(t *testing.T) {
	t.Parallel()
	got := DutyNames()
	if len(got) == 0 {
		t.Fatal("DutyNames() is empty")
	}
	got[0] = "gh.delete"
	if IsDuty("gh.delete") || DutyNames()[0] == "gh.delete" {
		t.Error("changing the returned slice changed the library")
	}
}

func TestDutiesParseAndApply(t *testing.T) {
	t.Parallel()
	for format, src := range map[string]string{
		"toml": settingsTOML(`duties = ["gh.read", "gh.comment"]`),
		"yaml": settingsYAML(`duties: [gh.read, gh.comment]`),
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
		if got := strings.Join(team.Roles[0].Duties, ","); got != "gh.read,gh.comment" {
			t.Errorf("%s stored duties = %q, want gh.read,gh.comment", format, got)
		}
	}
	m, err := ParseManifestBytes([]byte(settingsTOML(``)))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	team, _ := store.GetTeam("ss/squad")
	if len(team.Roles[0].Duties) != 0 {
		t.Errorf("a role with no key has duties %v, want none", team.Roles[0].Duties)
	}
}

// TestDutiesRefusals: a name outside the library is refused at parse, so a
// typo cannot silently declare nothing. Names compare exactly; the library is
// lower case and a check name is not normalized.
func TestDutiesRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, toml, yaml, want string }{
		{"a name outside the library", `duties = ["gh.delete"]`, `duties: [gh.delete]`, "not a duty"},
		{"an empty entry", `duties = ["gh.read", ""]`, `duties: [gh.read, ""]`, "empty"},
		{"a duplicate", `duties = ["gh.read", "gh.read"]`, `duties: [gh.read, gh.read]`, "twice"},
		{"another case", `duties = ["GH.read"]`, `duties: [GH.read]`, "not a duty"},
		{"edge space", `duties = ["gh.read "]`, `duties: ["gh.read "]`, "not a duty"},
	}
	for _, tc := range cases {
		for format, src := range map[string]string{"toml": settingsTOML(tc.toml), "yaml": settingsYAML(tc.yaml)} {
			_, err := ParseManifestBytes([]byte(src))
			if err == nil || !strings.Contains(err.Error(), "duties") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s/%s: error %v, want it to name duties and %q", tc.name, format, err, tc.want)
			}
		}
	}
}

// TestDutiesRefusalDoesNotEchoAnUnknownName: the refusal names the position,
// not the value, the same rule ValidateDutyRecord follows, so a pasted secret
// in a manifest cannot ride into a log through the error.
func TestDutiesRefusalDoesNotEchoAnUnknownName(t *testing.T) {
	t.Parallel()
	const planted = "sk-ant-planted-not-a-duty"
	_, err := ParseManifestBytes([]byte(settingsTOML(`duties = ["` + planted + `"]`)))
	if err == nil {
		t.Fatal("an unknown duty parsed")
	}
	if strings.Contains(err.Error(), planted) {
		t.Errorf("error echoes the value: %v", err)
	}
}
