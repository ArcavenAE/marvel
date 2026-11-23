package api

import "testing"

func TestRoleTCCDenyDefaultsOn(t *testing.T) {
	t.Parallel()
	off, on := false, true
	for _, tc := range []struct {
		name string
		role Role
		want bool
	}{{"absent", Role{}, true}, {"true", Role{TCCDeny: &on}, true}, {"false", Role{TCCDeny: &off}, false}} {
		if got := tc.role.TCCDenied(); got != tc.want {
			t.Errorf("%s: TCCDenied() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// tcc_deny is a per-role key, in TOML and in YAML, carried to the applied role.
// Absent means on, and only an explicit false turns the deny off.
func TestTCCDenyIsARoleKeyCarriedToTheAppliedRole(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		extra string
		want  bool
	}{
		"absent": {"", true},
		"false":  {"  tcc_deny = false", false},
		"true":   {"  tcc_deny = true", true},
	} {
		m, err := imageManifest(t, tc.extra)
		if err != nil {
			t.Fatalf("toml %s: %v", name, err)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		team, err := store.GetTeam("aae/squad")
		if err != nil {
			t.Fatal(err)
		}
		if got := team.Roles[0].TCCDenied(); got != tc.want {
			t.Errorf("toml %s: TCCDenied() = %v, want %v", name, got, tc.want)
		}
	}
	y := "workspace:\n  name: aae\nteams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        tcc_deny: false\n        runtime:\n          command: pi\n"
	m, err := ParseManifestBytes([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	team, _ := store.GetTeam("aae/squad")
	if team.Roles[0].TCCDenied() {
		t.Error("yaml: tcc_deny false was not carried")
	}
}
