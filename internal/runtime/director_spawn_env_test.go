package runtime

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// docs/design/declared-cadence.md V6 (ticket T2): marvel sets DIRECTOR_CLUSTER
// at spawn, beside DIRECTOR_TEAM and DIRECTOR_WORKSPACE, so a marvel-native
// launch names its cluster without a cast script. It does not set
// DIRECTOR_ROLE: in director's shim that is role membership, which a role opts
// into through its own runtime.env. A role's declared value still wins.

func directorEnvCtx(cluster string) *LaunchContext {
	return &LaunchContext{
		Session:   &api.Session{Name: "ops-envoy-g1-0", Workspace: "ws", Team: "ops"},
		Role:      &api.Role{Name: "envoy"},
		Team:      &api.Team{Name: "ops"},
		Workspace: &api.Workspace{Name: "ws"},
		Cluster:   cluster,
	}
}

func TestConstructedEnvCarriesDirectorCluster(t *testing.T) {
	t.Parallel()
	env := baseEnv(directorEnvCtx("mokuzai"))
	if got := env["DIRECTOR_CLUSTER"]; got != "mokuzai" {
		t.Errorf("DIRECTOR_CLUSTER = %q, want mokuzai", got)
	}
}

// A daemon with no cluster entry name has no cluster to state, and marvel does
// not invent one: the variable is absent, as the seat URI is (seatURI refuses
// the same case).
func TestConstructedEnvOmitsDirectorClusterWhenUnnamed(t *testing.T) {
	t.Parallel()
	env := baseEnv(directorEnvCtx(""))
	if v, set := env["DIRECTOR_CLUSTER"]; set {
		t.Errorf("DIRECTOR_CLUSTER = %q with no cluster name, want it unset", v)
	}
}

// Holding a role is opt-in. A spawn whose role declares no DIRECTOR_ROLE must
// not make the seat a holder of its role.
func TestSpawnWithNoRoleEnvHasNoDirectorRole(t *testing.T) {
	t.Parallel()
	for _, cluster := range []string{"mokuzai", ""} {
		env := baseEnv(directorEnvCtx(cluster))
		if v, set := env["DIRECTOR_ROLE"]; set {
			t.Errorf("cluster %q: DIRECTOR_ROLE = %q with no role env, want it unset", cluster, v)
		}
	}
}

// A role that wants role mail declares DIRECTOR_ROLE in its own runtime.env.
func TestRoleDeclaredDirectorRoleIsKept(t *testing.T) {
	t.Parallel()
	ctx := directorEnvCtx("mokuzai")
	ctx.Role.Runtime.Env = map[string]string{"DIRECTOR_ROLE": "sweeper"}
	env := baseEnv(ctx)
	if got := env["DIRECTOR_ROLE"]; got != "sweeper" {
		t.Errorf("DIRECTOR_ROLE = %q, want the role's sweeper", got)
	}
}

func TestDirectorClusterCanBeOverriddenByTheRole(t *testing.T) {
	t.Parallel()
	ctx := directorEnvCtx("mokuzai")
	ctx.Role.Runtime.Env = map[string]string{"DIRECTOR_CLUSTER": "other"}
	env := baseEnv(ctx)
	if got := env["DIRECTOR_CLUSTER"]; got != "other" {
		t.Errorf("DIRECTOR_CLUSTER = %q, want the role's other", got)
	}
}

// The cluster is in the constructed set that a harness which does not pass its
// environment on is told to forward.
func TestDirectorClusterIsAForwardedName(t *testing.T) {
	t.Parallel()
	env := constructedEnv(directorEnvCtx("mokuzai"))
	if _, ok := env["DIRECTOR_CLUSTER"]; !ok {
		t.Errorf("DIRECTOR_CLUSTER missing from the constructed environment")
	}
}
