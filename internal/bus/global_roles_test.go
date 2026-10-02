package bus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The role set that holds a global address is one declared list, and the
// supervisor check reads it (aae-orc-6vy9x, design section 3).
func TestHasSupervisorRoleReadsTheDeclaredList(t *testing.T) {
	team := func(names ...string) api.Team {
		var roles []api.Role
		for _, n := range names {
			roles = append(roles, api.Role{Name: n})
		}
		return api.Team{Name: "t", Roles: roles}
	}
	if got := config.GlobalAddressRoles; len(got) != 1 || got[0] != "supervisor" {
		t.Fatalf("GlobalAddressRoles = %v, want [supervisor]", got)
	}
	if !hasSupervisorRole(team("worker", "supervisor")) || hasSupervisorRole(team("worker", "research-supervisor")) {
		t.Fatal("the default list must match the exact role name supervisor and nothing like it")
	}
	saved := config.GlobalAddressRoles
	t.Cleanup(func() { config.GlobalAddressRoles = saved })
	config.GlobalAddressRoles = []string{"lead"}
	if !hasSupervisorRole(team("worker", "lead")) {
		t.Error("a team with the role in the declared list is not recognised: the check does not read the list")
	}
	if hasSupervisorRole(team("worker", "supervisor")) {
		t.Error("supervisor still recognised after it left the declared list: the check is hard-coded")
	}
}

// A user whose name carries a dot (the per-role user, <team>.supervisor) keeps
// its password across a daemon restart. The recovery pattern used to stop at
// the dot, so the user was reminted and every running seat on it lost its
// credential (design section 3, test 3).
func TestRecoverPasswordsReadsADottedUser(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), AuthName)
	body := `authorization {
  users = [
    { user: arcaven, password: "pw-team",
      permissions: {} },
    { user: arcaven.supervisor, password: "pw-sup",
      permissions: {} },
  ]
}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := RecoverPasswords(path)
	if got["arcaven"] != "pw-team" {
		t.Errorf("team user = %q, want pw-team (recovered %v)", got["arcaven"], got)
	}
	if got["arcaven.supervisor"] != "pw-sup" {
		t.Errorf("dotted user = %q, want pw-sup (recovered %v)", got["arcaven.supervisor"], got)
	}
}
