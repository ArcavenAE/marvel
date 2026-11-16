package bus

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The supervises grant (aae-orc-spk60). A role that declares `supervises`
// may publish to the exact role inboxes of the teams it names, and every role
// of a named team may publish to the supervisor's role inbox, nothing else.
// The subjects are role inboxes only: never an agent id, never a wildcard, so
// the grant does not move when a seat shifts, and it does not need a hub.

// roleInbox matches exactly one role inbox: no wildcard token, no agent id.
var roleInbox = regexp.MustCompile(`^agent\.[a-z0-9-]+\.[a-z0-9-]+\.role\.[a-z0-9-]+\.inbox$`)

// superviseTeams is a ops team whose supervisor supervises alpha and
// beta, with gamma applied but not named, and a alpha team of
// the same name in another workspace.
func superviseTeams() []AppliedTeam {
	return []AppliedTeam{
		{Workspace: "ws1", Team: "ops", Roles: []AppliedRole{
			{Name: "supervisor", Supervises: []string{"alpha", "beta"}},
			{Name: "reviewer"},
		}},
		{Workspace: "ws1", Team: "alpha", Roles: []AppliedRole{{Name: "architect"}, {Name: "worker"}}},
		{Workspace: "ws1", Team: "beta", Roles: []AppliedRole{{Name: "lead"}}},
		{Workspace: "ws1", Team: "gamma", Roles: []AppliedRole{{Name: "architect"}}},
		{Workspace: "other", Team: "alpha", Roles: []AppliedRole{{Name: "architect"}}},
	}
}

// Allowed for a supervised team: the supervisor's user names every role inbox
// of each named team, exactly.
func TestSupervisorGrantNamesEveryRoleInboxOfEachSupervisedTeam(t *testing.T) {
	t.Parallel()
	g, err := SupervisedGrants(superviseTeams())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"agent.ws1.alpha.role.architect.inbox",
		"agent.ws1.alpha.role.worker.inbox",
		"agent.ws1.beta.role.lead.inbox",
	}
	if got := g["ops.supervisor"]; !slices.Equal(got, want) {
		t.Errorf("ops.supervisor grants = %v, want %v", got, want)
	}
}

// Denied for an unnamed team, for a team of the same name in another
// workspace, and for a non-supervisor role of the supervisor's own team.
func TestSupervisorGrantNamesNothingForAnUnnamedTeamOrANonSupervisorRole(t *testing.T) {
	t.Parallel()
	g, err := SupervisedGrants(superviseTeams())
	if err != nil {
		t.Fatal(err)
	}
	// Not vacuous: the supervised teams are granted, the others are not.
	if len(g["ops.supervisor"]) == 0 || len(g["alpha.architect"]) == 0 {
		t.Fatalf("the named teams are not granted at all: %v", g)
	}
	for user, subjects := range g {
		if user == "gamma.architect" {
			t.Errorf("an unnamed team has a grant user: %v", subjects)
		}
		for _, s := range subjects {
			if strings.Contains(s, "gamma") || strings.Contains(s, "agent.other.") {
				t.Errorf("%s is granted %q, a team the supervisor does not name", user, s)
			}
		}
	}
	if got, ok := g["ops.reviewer"]; ok {
		t.Errorf("a non-supervisor role of the supervisor's team has grants: %v", got)
	}
}

// The upward grant is exact: every role of a supervised team gets the
// supervisor's role inbox and nothing else, and a sibling supervised team's
// inboxes are not part of it.
func TestSupervisedRolesGetExactlyTheSupervisorRoleInbox(t *testing.T) {
	t.Parallel()
	g, err := SupervisedGrants(superviseTeams())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agent.ws1.ops.role.supervisor.inbox"}
	for _, user := range []string{"alpha.architect", "alpha.worker", "beta.lead"} {
		if got := g[user]; !slices.Equal(got, want) {
			t.Errorf("%s grants = %v, want %v", user, got, want)
		}
	}
	if _, ok := g["gamma.architect"]; ok {
		t.Error("a team the supervisor does not name got the upward grant")
	}
}

// No agent id and no wildcard in any subject, so nothing in the grant follows
// a seat's generation or index across a shift.
func TestSupervisesGrantsAreRoleInboxesOnly(t *testing.T) {
	t.Parallel()
	g, err := SupervisedGrants(superviseTeams())
	if err != nil {
		t.Fatal(err)
	}
	if len(g) == 0 {
		t.Fatal("no grants at all")
	}
	for user, subjects := range g {
		for _, s := range subjects {
			if !roleInbox.MatchString(s) {
				t.Errorf("%s: %q is not an exact role-inbox subject", user, s)
			}
		}
	}
}

// The grant is a function of the applied teams and their declarations, not of
// the order they arrive in, so a render is stable from one pass to the next.
func TestSupervisesGrantsAreStableUnderInputOrder(t *testing.T) {
	t.Parallel()
	a, err := SupervisedGrants(superviseTeams())
	if err != nil {
		t.Fatal(err)
	}
	rev := superviseTeams()
	slices.Reverse(rev)
	b, err := SupervisedGrants(rev)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("grants differ in size or are empty: %d and %d", len(a), len(b))
	}
	for user, want := range a {
		if !slices.Equal(b[user], want) {
			t.Errorf("%s: %v in one order, %v in the other", user, want, b[user])
		}
	}
}

// A named team that is not applied in the supervisor's workspace grants
// nothing, and is reported so the operator can see the declaration is inert.
func TestSupervisesNamingAnUnappliedTeamIsReportedAndGrantsNothing(t *testing.T) {
	t.Parallel()
	applied := []AppliedTeam{
		{Workspace: "ws1", Team: "ops", Roles: []AppliedRole{{Name: "supervisor", Supervises: []string{"ghost", "alpha"}}}},
		{Workspace: "other", Team: "alpha", Roles: []AppliedRole{{Name: "architect"}}},
	}
	g, err := SupervisedGrants(applied)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := g["ops.supervisor"]; ok {
		t.Errorf("an unresolved relation granted %v", got)
	}
	if _, ok := g["alpha.architect"]; ok {
		t.Error("a team of the same name in another workspace got the upward grant")
	}
	want := []string{"ws1/ops.supervisor -> alpha", "ws1/ops.supervisor -> ghost"}
	if got := UnresolvedSupervises(applied); !slices.Equal(got, want) {
		t.Errorf("unresolved = %v, want %v", got, want)
	}
}

// A name that is not one address token is refused, never rendered into a
// subject.
func TestSupervisedGrantsRefuseAnUnaddressableName(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"*", ">", "a.b", "A", ""} {
		applied := []AppliedTeam{
			{Workspace: "ws1", Team: "ops", Roles: []AppliedRole{{Name: "supervisor", Supervises: []string{bad}}}},
		}
		if _, err := SupervisedGrants(applied); err == nil {
			t.Errorf("supervises %q accepted", bad)
		}
	}
}

// supervisesManager builds a Manager over the same shape as superviseTeams,
// with or without a hub.
func supervisesManager(t *testing.T, hub string) (*Manager, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir, HubURL: hub}
	live := &teams{
		{Name: "ops", Workspace: "ws1", Roles: []api.Role{
			{Name: "supervisor", Supervises: []string{"alpha", "beta"}},
			{Name: "reviewer"},
		}},
		{Name: "alpha", Workspace: "ws1", Roles: []api.Role{{Name: "architect"}, {Name: "worker"}}},
		{Name: "beta", Workspace: "ws1", Roles: []api.Role{{Name: "lead"}}},
		{Name: "gamma", Workspace: "ws1", Roles: []api.Role{{Name: "architect"}}},
	}
	m, err := NewManager(dir, "c1", rb, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, dir
}

// authGrants reads the rendered auth file and returns, for each user, its
// publish and subscribe lists.
func authGrants(t *testing.T, dir string) map[string][2][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, AuthName))
	if err != nil {
		t.Fatal(err)
	}
	quoted := regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	out := map[string][2][]string{}
	for _, seg := range strings.Split(string(b), "{ user: ")[1:] {
		name, rest, _ := strings.Cut(seg, ",")
		var lists [2][]string
		for i, key := range []string{"publish", "subscribe"} {
			_, after, ok := strings.Cut(rest, key)
			if !ok {
				continue
			}
			line, _, _ := strings.Cut(after, "]")
			for _, m := range quoted.FindAllStringSubmatch(line, -1) {
				lists[i] = append(lists[i], m[1])
			}
		}
		out[name] = lists
	}
	return out
}

// The grant renders on the broker's own auth file with no hub configured, as
// per-role users, and not on any team user.
func TestSupervisesGrantRendersWithNoHubOnPerRoleUsers(t *testing.T) {
	t.Parallel()
	m, dir := supervisesManager(t, "")
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users := authGrants(t, dir)
	sup, ok := users["ops.supervisor"]
	if !ok {
		t.Fatalf("no ops.supervisor user in %v", users)
	}
	for _, want := range []string{"agent.ws1.alpha.role.architect.inbox", "agent.ws1.alpha.role.worker.inbox", "agent.ws1.beta.role.lead.inbox"} {
		if !contains(sup[0], want) {
			t.Errorf("ops.supervisor does not publish %q: %v", want, sup[0])
		}
	}
	mid, ok := users["alpha.architect"]
	if !ok {
		t.Fatalf("no alpha.architect user in %v", users)
	}
	if !contains(mid[0], "agent.ws1.ops.role.supervisor.inbox") {
		t.Errorf("alpha.architect does not publish the supervisor inbox: %v", mid[0])
	}
	if contains(mid[0], "agent.ws1.beta.role.lead.inbox") {
		t.Errorf("alpha.architect publishes a sibling team's inbox: %v", mid[0])
	}
	for _, name := range []string{"ops", "alpha", "beta", "gamma"} {
		for _, s := range users[name][0] {
			if strings.Contains(s, ".role.") {
				t.Errorf("team user %s carries a role-inbox grant %q", name, s)
			}
		}
	}
	for _, name := range []string{"ops.reviewer", "gamma.architect"} {
		if _, has := users[name]; has {
			t.Errorf("user %s exists but holds no supervises relation", name)
		}
	}
	// No hub, so no global grants on any of them.
	for _, name := range []string{"ops.supervisor", "alpha.architect"} {
		for _, s := range append(slices.Clone(users[name][0]), users[name][1]...) {
			if strings.HasPrefix(s, "global.") || strings.HasPrefix(s, "$JS.global") {
				t.Errorf("%s has a global grant %q without a hub", name, s)
			}
		}
	}
}

// A seat of a supervised role presents its own per-role user once the broker
// has accepted it, and the team user until then; a role with no relation
// always presents the team user.
func TestCredentialSelectsTheSupervisesUserOnceAccepted(t *testing.T) {
	t.Parallel()
	m, _ := supervisesManager(t, "")
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if u, _, _ := m.Credential("alpha", "architect"); u != "alpha" {
		t.Fatalf("before the broker accepts it, alpha.architect got %q, want the team user", u)
	}
	confirmed(t, m)
	for _, c := range []struct{ team, role, want string }{
		{"ops", "supervisor", "ops.supervisor"},
		{"alpha", "architect", "alpha.architect"},
		{"alpha", "worker", "alpha.worker"},
		{"ops", "reviewer", "ops"},
		{"gamma", "architect", "gamma"},
	} {
		if u, _, ok := m.Credential(c.team, c.role); !ok || u != c.want {
			t.Errorf("Credential(%s, %s) = %q, want %q", c.team, c.role, u, c.want)
		}
	}
}

// With a hub the supervisor's user is the one global per-role user: the
// relation adds to it, and the global grants stay. A supervised role with no
// global role gets only its inbox grant, with no global subject.
func TestSupervisesGrantAddsToTheGlobalSupervisorUserWithAHub(t *testing.T) {
	t.Parallel()
	m, dir := supervisesManager(t, "tls://hub.example:7422")
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	users := authGrants(t, dir)
	sup, ok := users["ops.supervisor"]
	if !ok {
		t.Fatalf("no ops.supervisor user in %v", users)
	}
	for _, want := range []string{"global.director.inbox", "agent.ws1.alpha.role.architect.inbox", "agent.ws1.beta.role.lead.inbox"} {
		if !contains(sup[0], want) {
			t.Errorf("ops.supervisor does not publish %q: %v", want, sup[0])
		}
	}
	if !contains(sup[1], "global.c1.supervisor.inbox") {
		t.Errorf("ops.supervisor lost its global subscribe: %v", sup[1])
	}
	mid, ok := users["alpha.architect"]
	if !ok {
		t.Fatalf("no alpha.architect user in %v", users)
	}
	for _, s := range append(slices.Clone(mid[0]), mid[1]...) {
		if strings.HasPrefix(s, "global.") || strings.HasPrefix(s, "$JS.global") {
			t.Errorf("alpha.architect has a global grant %q", s)
		}
	}
	if !contains(mid[0], "agent.ws1.ops.role.supervisor.inbox") {
		t.Errorf("alpha.architect does not publish the supervisor inbox: %v", mid[0])
	}
}

// A name that did not resolve becomes valid, with no change to the
// supervisor's declaration, once its team is applied: the declaration is read
// at every render, so apply order does not matter.
func TestSupervisesNameResolvesOnceItsTeamIsApplied(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir}
	live := &teams{
		{Name: "ops", Workspace: "ws1", Roles: []api.Role{{Name: "supervisor", Supervises: []string{"alpha"}}}},
	}
	m, err := NewManager(dir, "c1", rb, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	before := authGrants(t, dir)
	if _, has := before["ops.supervisor"]; has {
		t.Errorf("the supervisor has a grant user before its team is applied: %v", before["ops.supervisor"])
	}
	*live = append(*live, api.Team{Name: "alpha", Workspace: "ws1", Roles: []api.Role{{Name: "architect"}}})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	after := authGrants(t, dir)
	if got := after["ops.supervisor"][0]; !contains(got, "agent.ws1.alpha.role.architect.inbox") {
		t.Errorf("after alpha is applied the supervisor does not publish its inbox: %v", got)
	}
	if got := after["alpha.architect"][0]; !contains(got, "agent.ws1.ops.role.supervisor.inbox") {
		t.Errorf("after alpha is applied its architect does not publish the supervisor inbox: %v", got)
	}
}
