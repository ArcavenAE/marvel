package bus

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
)

// docs/design/global-role-declaration.md sections 4 and 6: every role whose
// resolved global role is the supervisor word gets its own `<team>.<role>`
// user, and the renderer, Credential and the seat env all read one resolver
// with the cluster's admitted set.

func declRole(name, declared string) api.Role {
	return api.Role{Name: name, GlobalRole: declared}
}

func declTeam(roles ...api.Role) api.Team {
	return api.Team{Name: "ops", Workspace: "acme", Roles: roles}
}

// declManager is a managed bus with a hub that admits the named roles.
func declManager(t *testing.T, dir string, live *teams, admitted ...string) *Manager {
	t.Helper()
	rb := config.ResolvedBus{
		Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir,
		HubURL: "tls://hub.example:7422", GlobalRoles: admitted,
	}
	m, err := NewManager(dir, "kinu", rb, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func renderedUsers(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for user := range RecoverPasswords(filepath.Join(dir, AuthName)) {
		out = append(out, user)
	}
	slices.Sort(out)
	return out
}

// The renderer emits one principal per global role the team holds, each with
// the grants `<team>.supervisor` carries today, and the team user still has its
// global grants until stage 3.
func TestRendererEmitsOneUserPerGlobalRole(t *testing.T) {
	t.Parallel()
	s := hubSpec()
	s.Teams[0].GlobalRoles = []RoleUser{
		{Role: "supervisor", Password: "pw-sup"},
		{Role: "research-supervisor", Password: "pw-rs"},
	}
	ps, err := DeclaredPrincipals(s)
	if err != nil {
		t.Fatal(err)
	}
	sup := principalNamed(t, ps, "ops.supervisor")
	rs := principalNamed(t, ps, "ops.research-supervisor")
	if sup.Password != "pw-sup" || rs.Password != "pw-rs" {
		t.Errorf("passwords = %q and %q, want pw-sup and pw-rs", sup.Password, rs.Password)
	}
	if !slices.Equal(sup.Publish, rs.Publish) || !slices.Equal(sup.Subscribe, rs.Subscribe) {
		t.Errorf("the two global roles carry different grants:\nsup %v / %v\nrs  %v / %v", sup.Publish, sup.Subscribe, rs.Publish, rs.Subscribe)
	}
	if !contains(rs.Publish, "global.director.inbox") || !contains(rs.Subscribe, "global.kinu.supervisor.inbox") {
		t.Errorf("research-supervisor lacks the global grants: %v / %v", rs.Publish, rs.Subscribe)
	}
	if contains(rs.Subscribe, "global.kinu.>") {
		t.Error("a global role user subscribes the whole global domain; it must be narrowed to its inbox")
	}
	team := principalNamed(t, ps, "ops")
	if !contains(team.Publish, "global.director.inbox") {
		t.Errorf("the team user lost its global grants before stage 3: %v", team.Publish)
	}
}

// A global role user with no password is refused, never rendered open.
func TestARoleUserWithoutAPasswordIsRefused(t *testing.T) {
	t.Parallel()
	s := hubSpec()
	s.Teams[0].GlobalRoles = []RoleUser{{Role: "supervisor", Password: "pw"}, {Role: "research-supervisor"}}
	if _, err := DeclaredPrincipals(s); err == nil || !strings.Contains(err.Error(), "research-supervisor") {
		t.Fatalf("error = %v, want a refusal that names the role", err)
	}
}

// Test 2: a role that declares the global role, admitted by the cluster, in a
// team with no role named supervisor, gets its own user, and Credential hands
// it out once the broker has accepted it.
func TestADeclaredRoleGetsItsOwnUser(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{declTeam(declRole("research-supervisor", "supervisor"), declRole("worker", ""))}
	m := declManager(t, dir, live, "research-supervisor")
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got := renderedUsers(t, dir); !slices.Contains(got, "ops.research-supervisor") {
		t.Fatalf("rendered users = %v, want ops.research-supervisor", got)
	}
	if u, _, ok := m.Credential("ops", "research-supervisor"); !ok || u != "ops" {
		t.Errorf("before the broker accepts it, Credential = %q %v, want the team user", u, ok)
	}
	confirmed(t, m)
	u, pw, ok := m.Credential("ops", "research-supervisor")
	if !ok || u != "ops.research-supervisor" || pw == "" {
		t.Fatalf("Credential(ops, research-supervisor) = %q %q %v, want its own user", u, pw, ok)
	}
	if wu, _, ok := m.Credential("ops", "worker"); !ok || wu != "ops" {
		t.Errorf("Credential(ops, worker) = %q %v, want the team user", wu, ok)
	}
	if got := m.GlobalRole(declRole("research-supervisor", "supervisor")); got != "supervisor" {
		t.Errorf("GlobalRole = %q, want supervisor, the word the seat env carries", got)
	}
	if got := m.GlobalRole(declRole("worker", "")); got != "" {
		t.Errorf("GlobalRole of a worker = %q, want none", got)
	}
}

// Test 4: the renderer and Credential agree. Whatever user Credential returns
// for a role, the rendered authorization file declares it. This is the test
// that would have caught the name mismatch in design section 1, where
// Credential looked up `<team>.<role>` for a role the renderer never minted.
func TestTheRendererAndCredentialAgree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		roles    []api.Role
		admitted []string
		global   []string // roles that must resolve to their own user
	}{
		{"default supervisor", []api.Role{declRole("supervisor", ""), declRole("worker", "")}, nil, []string{"supervisor"}},
		{"declared and admitted", []api.Role{declRole("research-supervisor", "supervisor")}, []string{"research-supervisor"}, []string{"research-supervisor"}},
		{"declared but not admitted", []api.Role{declRole("research-supervisor", "supervisor")}, nil, nil},
		{"undeclared by another name", []api.Role{declRole("research-supervisor", "")}, []string{"research-supervisor"}, nil},
		{"supervisor opted out", []api.Role{declRole("supervisor", "none"), declRole("worker", "")}, nil, nil},
		{"two global roles", []api.Role{declRole("supervisor", ""), declRole("research-supervisor", "supervisor")}, []string{"research-supervisor"}, []string{"supervisor", "research-supervisor"}},
	}
	for _, tc := range cases {
		dir := filepath.Join(t.TempDir(), "nats")
		m := declManager(t, dir, &teams{declTeam(tc.roles...)}, tc.admitted...)
		if _, err := m.Regenerate(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		confirmed(t, m)
		rendered := renderedUsers(t, dir)
		for _, r := range tc.roles {
			user, _, ok := m.Credential("ops", r.Name)
			if !ok {
				t.Errorf("%s: no credential for %s", tc.name, r.Name)
				continue
			}
			if !slices.Contains(rendered, user) {
				t.Errorf("%s: Credential(%s) = %q, which the rendered file does not declare (%v)", tc.name, r.Name, user, rendered)
			}
			wantOwn := slices.Contains(tc.global, r.Name)
			if own := user == "ops."+r.Name; own != wantOwn {
				t.Errorf("%s: %s has its own user = %v, want %v (got %q)", tc.name, r.Name, own, wantOwn, user)
			}
		}
	}
}

// Test 10: revoking a role. A role turned from "supervisor" to "none" leaves
// the rendered users at the next render and its password is dropped, while a
// supervisor in the same team keeps its own user and password. This is why the
// design chose one user per role over one shared user.
func TestRevokingARoleRemovesOnlyItsUser(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{declTeam(declRole("supervisor", ""), declRole("research-supervisor", "supervisor"))}
	m := declManager(t, dir, live, "research-supervisor")
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	confirmed(t, m)
	_, supBefore, _ := m.Credential("ops", "supervisor")
	if got := renderedUsers(t, dir); !slices.Contains(got, "ops.research-supervisor") || !slices.Contains(got, "ops.supervisor") {
		t.Fatalf("rendered users = %v, want both global role users", got)
	}

	*live = teams{declTeam(declRole("supervisor", ""), declRole("research-supervisor", "none"))}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	got := renderedUsers(t, dir)
	if slices.Contains(got, "ops.research-supervisor") {
		t.Errorf("the revoked role's user is still rendered: %v", got)
	}
	if u, _, ok := m.Credential("ops", "research-supervisor"); !ok || u != "ops" {
		t.Errorf("after revocation Credential = %q %v, want the team user", u, ok)
	}
	if _, supAfter, _ := m.Credential("ops", "supervisor"); supAfter != supBefore {
		t.Error("revoking one role changed the supervisor's password")
	}
	if !slices.Contains(got, "ops.supervisor") {
		t.Errorf("the supervisor's user was removed with the revoked role's: %v", got)
	}
}

// Test 11: de-admit, then restart. A stored team whose research-supervisor
// declares the global role, under a config that admitted it; the daemon
// restarts (rehydrate, no apply) with a config that no longer admits the name.
// The first render drops the user, Credential hands a new spawn the team user,
// the resolver says none (so the seat env carries no DIRECTOR_GLOBAL_ROLE), and
// bus.global-role-unadmitted is emitted once.
//
// The env claim holds for a FRESH spawn. A seat that was already running keeps
// the environment it started with until it is respawned.
func TestDeAdmittingARoleAcrossARestart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	live := &teams{declTeam(declRole("research-supervisor", "supervisor"), declRole("worker", ""))}
	before := declManager(t, dir, live, "research-supervisor")
	if _, err := before.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got := renderedUsers(t, dir); !slices.Contains(got, "ops.research-supervisor") {
		t.Fatalf("setup: rendered users = %v", got)
	}

	ring := events.NewRing(32)
	after := declManager(t, dir, live) // restarted; the config no longer admits the name
	after.Events = ring
	for i := 0; i < 2; i++ { // two renders: the event is once per change, not per render
		if _, err := after.Regenerate(); err != nil {
			t.Fatal(err)
		}
	}
	confirmed(t, after)

	if got := renderedUsers(t, dir); slices.Contains(got, "ops.research-supervisor") {
		t.Errorf("the de-admitted role's user is still rendered: %v", got)
	}
	if u, _, ok := after.Credential("ops", "research-supervisor"); !ok || u != "ops" {
		t.Errorf("Credential after de-admission = %q %v, want the team user", u, ok)
	}
	if got := after.GlobalRole(declRole("research-supervisor", "supervisor")); got != "" {
		t.Errorf("GlobalRole after de-admission = %q, want none", got)
	}
	evs := ring.Snapshot(events.Filter{Kind: events.KindBusGlobalRoleUnadmitted}, 0)
	if len(evs) != 1 {
		t.Fatalf("%d bus.global-role-unadmitted events, want 1: %+v", len(evs), evs)
	}
	if !strings.Contains(evs[0].Message, "ops") || !strings.Contains(evs[0].Message, "research-supervisor") {
		t.Errorf("event message %q does not name the team and the role", evs[0].Message)
	}

	// Admitting it again clears the condition; a later de-admission is a new change.
	again := declManager(t, dir, live, "research-supervisor")
	again.Events = ring
	if _, err := again.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if got := renderedUsers(t, dir); !slices.Contains(got, "ops.research-supervisor") {
		t.Errorf("re-admitting the role did not render its user: %v", got)
	}
	if n := len(ring.Snapshot(events.Filter{Kind: events.KindBusGlobalRoleUnadmitted}, 0)); n != 1 {
		t.Errorf("re-admitting emitted an unadmitted event: %d total, want 1", n)
	}
}

// The authorization file is never written with a user for a role the cluster
// does not admit, whatever else is in the file.
func TestNoUserForAnUnadmittedDeclaration(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	m := declManager(t, dir, &teams{declTeam(declRole("research-supervisor", "supervisor"))})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, AuthName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "research-supervisor") {
		t.Errorf("an unadmitted declaration reached the authorization file:\n%s", raw)
	}
}
