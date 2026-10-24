package runtime

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// docs/design/declared-cadence.md V6 (ticket T2): marvel sets DIRECTOR_CLUSTER
// and DIRECTOR_ROLE at spawn, beside DIRECTOR_TEAM and DIRECTOR_WORKSPACE, so a
// marvel-native launch does not depend on a cast script to name its cluster and
// role on the bus. A role's declared value still wins, as for the others.

func directorEnvCtx(cluster string) *LaunchContext {
	return &LaunchContext{
		Session:   &api.Session{Name: "ops-envoy-g1-0", Workspace: "ws", Team: "ops"},
		Role:      &api.Role{Name: "envoy"},
		Team:      &api.Team{Name: "ops"},
		Workspace: &api.Workspace{Name: "ws"},
		Cluster:   cluster,
	}
}

func TestConstructedEnvCarriesDirectorClusterAndRole(t *testing.T) {
	t.Parallel()
	env := baseEnv(directorEnvCtx("mokuzai"))
	if got := env["DIRECTOR_CLUSTER"]; got != "mokuzai" {
		t.Errorf("DIRECTOR_CLUSTER = %q, want mokuzai", got)
	}
	if got := env["DIRECTOR_ROLE"]; got != "envoy" {
		t.Errorf("DIRECTOR_ROLE = %q, want envoy", got)
	}
}

// A daemon with no cluster entry name has no cluster to state, and marvel does
// not invent one: the variable is absent, as the seat URI is (seatURI refuses
// the same case), while the role is always known.
func TestConstructedEnvOmitsDirectorClusterWhenUnnamed(t *testing.T) {
	t.Parallel()
	env := baseEnv(directorEnvCtx(""))
	if v, set := env["DIRECTOR_CLUSTER"]; set {
		t.Errorf("DIRECTOR_CLUSTER = %q with no cluster name, want it unset", v)
	}
	if got := env["DIRECTOR_ROLE"]; got != "envoy" {
		t.Errorf("DIRECTOR_ROLE = %q, want envoy", got)
	}
}

func TestDirectorClusterAndRoleCanBeOverriddenByTheRole(t *testing.T) {
	t.Parallel()
	ctx := directorEnvCtx("mokuzai")
	ctx.Role.Runtime.Env = map[string]string{"DIRECTOR_CLUSTER": "other", "DIRECTOR_ROLE": "sweeper"}
	env := baseEnv(ctx)
	if env["DIRECTOR_CLUSTER"] != "other" || env["DIRECTOR_ROLE"] != "sweeper" {
		t.Errorf("DIRECTOR_CLUSTER=%q DIRECTOR_ROLE=%q, want the role's other and sweeper", env["DIRECTOR_CLUSTER"], env["DIRECTOR_ROLE"])
	}
}

// Both names are in the constructed set that a harness which does not pass its
// environment on is told to forward.
func TestDirectorClusterAndRoleAreForwardedNames(t *testing.T) {
	t.Parallel()
	env := constructedEnv(directorEnvCtx("mokuzai"))
	for _, name := range []string{"DIRECTOR_CLUSTER", "DIRECTOR_ROLE"} {
		if _, ok := env[name]; !ok {
			t.Errorf("%s missing from the constructed environment", name)
		}
	}
}
