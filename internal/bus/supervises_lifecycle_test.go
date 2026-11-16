package bus

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The lifecycle of the supervises users: pruned with the relation, hidden
// until the broker accepts them after a restart, and no new subscribe.

func supervisesLive() *teams {
	return &teams{
		{Name: "product", Workspace: "dtu", Roles: []api.Role{
			{Name: "supervisor", Supervises: []string{"midway"}},
			{Name: "reviewer"},
		}},
		{Name: "midway", Workspace: "dtu", Roles: []api.Role{{Name: "architect"}}},
		{Name: "vantage", Workspace: "dtu", Roles: []api.Role{{Name: "lead"}}},
	}
}

func supervisesManagerOver(t *testing.T, dir string, live *teams) *Manager {
	t.Helper()
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir}
	m, err := NewManager(dir, "corporate", rb, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A role that drops its supervises takes the users and their grants with it,
// and so does a supervisor whose team is removed, in the same render.
func TestSupervisesUsersAreRemovedWhenTheRelationGoes(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := supervisesLive()
	m := supervisesManagerOver(t, dir, live)
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users := authGrants(t, dir)
	for _, name := range []string{"product.supervisor", "midway.architect"} {
		if _, ok := users[name]; !ok {
			t.Fatalf("control: no %s user before the change: %v", name, users)
		}
	}

	(*live)[0].Roles[0].Supervises = nil
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users = authGrants(t, dir)
	for _, name := range []string{"product.supervisor", "midway.architect"} {
		if _, ok := users[name]; ok {
			t.Errorf("%s still has a user after the relation was dropped", name)
		}
		if _, ok := m.TeamPassword(name); ok {
			t.Errorf("%s still has a password after the relation was dropped", name)
		}
	}

	// Bring it back, then remove the supervisor's whole team.
	(*live)[0].Roles[0].Supervises = []string{"midway"}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	*live = (*live)[1:]
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users = authGrants(t, dir)
	for _, name := range []string{"product.supervisor", "midway.architect"} {
		if _, ok := users[name]; ok {
			t.Errorf("%s still has a user after the supervisor's team was removed", name)
		}
	}
}

// The relation adds publish and nothing else: a supervised role subscribes to
// exactly what its team user does, and the supervisor likewise.
func TestSupervisesGrantsAddNoSubscribe(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	m := supervisesManagerOver(t, dir, supervisesLive())
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users := authGrants(t, dir)
	for _, c := range []struct{ user, teamUser string }{
		{"midway.architect", "midway"},
		{"product.supervisor", "product"},
	} {
		got, ok := users[c.user]
		if !ok {
			t.Fatalf("no %s user in %v", c.user, users)
		}
		if want := users[c.teamUser][1]; !slices.Equal(got[1], want) {
			t.Errorf("%s subscribes %v, want its team user's %v", c.user, got[1], want)
		}
	}
}

// A manager started over a file it rendered before keeps the users'
// passwords, and hands the per-role user out only once the broker accepts it
// again.
func TestARestartedManagerHidesASupervisesUserUntilConfirmed(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := supervisesLive()
	m1 := supervisesManagerOver(t, dir, live)
	if _, err := m1.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m1)
	first, _ := m1.TeamPassword("midway.architect")
	if first == "" {
		t.Fatal("control: no password was minted for midway.architect")
	}
	m2 := supervisesManagerOver(t, dir, live)
	if _, err := m2.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got, _ := m2.TeamPassword("midway.architect"); got != first {
		t.Errorf("the password changed across a restart: %q then %q", first, got)
	}
	if u, _, _ := m2.Credential("midway", "architect"); u != "midway" {
		t.Fatalf("a fresh manager handed out %q before the broker was asked, want the team user", u)
	}
	confirmed(t, m2)
	if u, _, _ := m2.Credential("midway", "architect"); u != "midway.architect" {
		t.Fatalf("after confirming, got %q", u)
	}
}

// A name in another workspace is not this supervisor's team: it renders no
// user and no grant, at the manager as at the grant function.
func TestSupervisesDoesNotReachAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{
		{Name: "product", Workspace: "dtu", Roles: []api.Role{{Name: "supervisor", Supervises: []string{"midway", "elsewhere"}}}},
		{Name: "midway", Workspace: "dtu", Roles: []api.Role{{Name: "architect"}}},
		{Name: "elsewhere", Workspace: "other", Roles: []api.Role{{Name: "architect"}}},
	}
	m := supervisesManagerOver(t, dir, live)
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users := authGrants(t, dir)
	// Control: the same-workspace name resolves.
	sup, ok := users["product.supervisor"]
	if !ok {
		t.Fatalf("control: no product.supervisor user in %v", users)
	}
	if !contains(sup[0], "agent.dtu.midway.role.architect.inbox") {
		t.Errorf("control: the same-workspace team is not granted: %v", sup[0])
	}
	if contains(sup[0], "agent.other.elsewhere.role.architect.inbox") {
		t.Errorf("the supervisor publishes a team of another workspace: %v", sup[0])
	}
	if _, ok := users["elsewhere.architect"]; ok {
		t.Error("a team of another workspace got a user from the relation")
	}
}
