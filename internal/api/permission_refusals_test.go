package api

import (
	"strings"
	"testing"
)

// apply refuses every role the harness has no mapping for, naming the team,
// the role, the mode and the runtime, and collects them all so one apply shows
// every fix.
func TestPermissionRefusalsNameEveryRefusedRole(t *testing.T) {
	m := &Manifest{Teams: []ManifestTeam{{
		Name: "crew",
		Roles: []ManifestRole{
			{Name: "ok", Permissions: "plan", Runtime: ManifestRuntime{Command: "codex"}},
			{Name: "rev", Permissions: "default", Runtime: ManifestRuntime{Command: "codex"}},
			{Name: "wk", DangerousPermissions: true, Runtime: ManifestRuntime{Image: "codex", Command: "codex"}},
		},
	}}}
	refuse := func(r ManifestRole) string {
		switch {
		case r.Permissions == "default":
			return `permissions "default" has no ruled mapping; set the flags in runtime.args`
		case r.DangerousPermissions:
			return "dangerous_permissions has no ruled mapping; set the flags in runtime.args"
		}
		return ""
	}
	got := m.PermissionRefusals(refuse)
	if len(got) != 2 {
		t.Fatalf("refusals = %q, want 2", got)
	}
	for _, w := range []string{"team crew", "role rev", `permissions "default"`, `runtime "codex"`, "runtime.args"} {
		if !strings.Contains(got[0], w) {
			t.Errorf("first refusal %q does not contain %q", got[0], w)
		}
	}
	for _, w := range []string{"team crew", "role wk", "dangerous_permissions", "runtime.args"} {
		if !strings.Contains(got[1], w) {
			t.Errorf("second refusal %q does not contain %q", got[1], w)
		}
	}
}

func TestPermissionRefusalsWithNoPredicateOrNoRefusalIsEmpty(t *testing.T) {
	m := &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: []ManifestRole{{Name: "a", Permissions: "plan"}}}}}
	if got := m.PermissionRefusals(nil); got != nil {
		t.Errorf("nil predicate: %q, want none", got)
	}
	if got := m.PermissionRefusals(func(ManifestRole) string { return "" }); len(got) != 0 {
		t.Errorf("no refusal: %q, want none", got)
	}
}
