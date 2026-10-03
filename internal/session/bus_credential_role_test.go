package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

const busRoleManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "supervisor"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"

  [[team.role]]
  name = "reviewer"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"
`

// roleBus hands out a different user per role, the way the managed broker does
// for a role that holds a global address.
type roleBus struct{}

func (roleBus) URL() string { return "nats://127.0.0.1:4222" }

func (roleBus) Credential(team, role string) (string, string, bool) {
	if role == "supervisor" {
		return team + ".supervisor", "sup-pw", true
	}
	return team, "team-pw", true
}

func (roleBus) GlobalRole(role api.Role) string {
	if role.Name == "supervisor" {
		return "supervisor"
	}
	return ""
}

// The spawn path asks for the credential of the seat's own role, so a
// respawned supervisor connects as the dotted user and every other role as the
// team user.
func TestPlanLaunchPassesTheRoleToTheBusCredential(t *testing.T) {
	t.Parallel()
	mgr := &Manager{
		store:         api.NewStore(),
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        events.NewRing(16),
		Bus:           roleBus{},
	}
	m, err := api.ParseManifestBytes([]byte(busRoleManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for role, want := range map[string]string{"supervisor": "squad.supervisor", "reviewer": "squad"} {
		plan := mgr.planLaunch(sessionFor(role, "claude"))
		if got := plan.env["DIRECTOR_NATS_USER"]; got != want {
			t.Errorf("%s: DIRECTOR_NATS_USER = %q, want %q", role, got, want)
		}
	}
}

const globalRoleBusManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "research-supervisor"
  replicas = 1
  global_role = "supervisor"

    [team.role.runtime]
    image = "claude"
    command = "claude"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"
`

// admittedBus resolves through the same config.ResolvedGlobalRole the managed
// broker does, with a fixed admitted set.
type admittedBus struct{ admitted []string }

func (admittedBus) URL() string { return "nats://127.0.0.1:4222" }

func (admittedBus) Credential(team, _ string) (string, string, bool) { return team, "team-pw", true }

func (b admittedBus) GlobalRole(role api.Role) string {
	return config.ResolvedGlobalRole(role, b.admitted)
}

// Test 6, the spawn half, and the fresh-spawn half of test 11. A new spawn of a
// declared and admitted role gets DIRECTOR_GLOBAL_ROLE=supervisor and a worker
// does not. Once the cluster stops admitting the name, the next FRESH spawn
// carries no such key. A seat that is already running keeps the environment it
// started with until it is respawned; the launch plan cannot reach into it.
func TestPlanLaunchCarriesTheGlobalRoleForAFreshSpawn(t *testing.T) {
	t.Parallel()
	mgr := &Manager{
		store:         api.NewStore(),
		adapters:      runtime.NewRegistry(),
		ProjectionDir: t.TempDir(),
		Events:        events.NewRing(16),
		Bus:           admittedBus{admitted: []string{"research-supervisor"}},
	}
	m, err := api.ParseManifestBytes([]byte(globalRoleBusManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	env := func(role string) map[string]string { return mgr.planLaunch(sessionFor(role, "claude")).env }
	if got := env("research-supervisor")["DIRECTOR_GLOBAL_ROLE"]; got != "supervisor" {
		t.Errorf("research-supervisor: DIRECTOR_GLOBAL_ROLE = %q, want supervisor", got)
	}
	if _, ok := env("worker")["DIRECTOR_GLOBAL_ROLE"]; ok {
		t.Error("worker: DIRECTOR_GLOBAL_ROLE is set")
	}

	mgr.Bus = admittedBus{} // restarted under a config that no longer admits the name
	if _, ok := env("research-supervisor")["DIRECTOR_GLOBAL_ROLE"]; ok {
		t.Error("a fresh spawn after de-admission still carries DIRECTOR_GLOBAL_ROLE")
	}
}
