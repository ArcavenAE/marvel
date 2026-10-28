package bus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// Supervisor peer publish on one cluster. A per-role supervisor user may
// publish the role inbox of every OTHER applied team's supervisor role on its
// cluster, and nothing else new: no seat id inbox, no wildcard, no team user,
// no subscribe. The grant is enumerated from the same GlobalRoles list the
// renderer already holds, so a team removed at apply drops out in the same
// render that drops its users.

// peerSpec is three teams with supervisors in two workspaces: A and B in w1,
// C in w2, a hub set so the per-role users render. Team D has no supervisor.
func peerSpec() Spec {
	s := baseSpec()
	s.HubURL = "tls://hub.example:7422"
	s.Teams = []TeamUser{
		{Workspace: "w1", Team: "A", Password: "apw", GlobalRoles: []RoleUser{{Role: "supervisor", Password: "asup"}}},
		{Workspace: "w1", Team: "B", Password: "bpw", GlobalRoles: []RoleUser{{Role: "supervisor", Password: "bsup"}}},
		{Workspace: "w2", Team: "C", Password: "cpw", GlobalRoles: []RoleUser{{Role: "supervisor", Password: "csup"}}},
		{Workspace: "w1", Team: "D", Password: "dpw"},
	}
	return s
}

// peerEntries is what a user publishes beyond its own base list, the
// supervisor global grants and the plumbing: the role-inbox entries of the
// peer grant.
func peerEntries(p Principal) []string {
	var out []string
	for _, s := range p.Publish {
		if strings.Contains(s, ".role.") && strings.HasSuffix(s, ".inbox") {
			out = append(out, s)
		}
	}
	return out
}

// Test 1: A.supervisor publishes exactly the other teams' supervisor role
// inboxes, across workspaces, and no entry for its own team.
func TestSupervisorPeerGrantNamesEveryOtherTeamsRoleInbox(t *testing.T) {
	t.Parallel()
	ps, err := DeclaredPrincipals(peerSpec())
	if err != nil {
		t.Fatal(err)
	}
	a := principalNamed(t, ps, "A.supervisor")
	want := []string{"agent.w1.B.role.supervisor.inbox", "agent.w2.C.role.supervisor.inbox"}
	if got := peerEntries(a); !slices.Equal(got, want) {
		t.Errorf("A.supervisor peer entries = %v, want %v", got, want)
	}
	// Today's list is kept, in front of the new entries.
	base := append([]string{"agent.w1.A.>", "agent.audit"}, plumbingPublish...)
	base = append(base, "global.director.inbox", "global.*.supervisor.inbox", "$JS.global.API.>")
	if len(a.Publish) != len(base)+len(want) || !slices.Equal(a.Publish[:len(base)], base) {
		t.Errorf("publish = %v\nwant today's list %v then %v", a.Publish, base, want)
	}
	if contains(a.Publish, "agent.w1.A.role.supervisor.inbox") {
		t.Error("the grant names the supervisor's own inbox, which agent.w1.A.> already covers")
	}
	c := principalNamed(t, ps, "C.supervisor")
	wantC := []string{"agent.w1.A.role.supervisor.inbox", "agent.w1.B.role.supervisor.inbox"}
	if got := peerEntries(c); !slices.Equal(got, wantC) {
		t.Errorf("C.supervisor peer entries = %v, want %v", got, wantC)
	}
}

// Test 2: a role other than supervisor that resolves to the supervisor word
// and is admitted by the cluster is in the list; the same role unadmitted is
// absent. The renderer decides admission, so this goes through the Manager.
func TestSupervisorPeerGrantFollowsAdmissionOfANonDefaultRole(t *testing.T) {
	t.Parallel()
	peer := api.Team{Name: "B", Workspace: "w1", Roles: []api.Role{
		{Name: "lead", GlobalRole: api.GlobalRoleSupervisor},
		{Name: "research-supervisor"},
	}}
	own := api.Team{Name: "A", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}}}
	render := func(admitted []string) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "nats")
		rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir, HubURL: "tls://hub.example:7422", GlobalRoles: admitted}
		m, err := NewManager(dir, "kinu", rb, &teams{own, peer}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Regenerate(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, AuthName))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := render([]string{"lead"}); !strings.Contains(got, `"agent.w1.B.role.lead.inbox"`) {
		t.Errorf("with lead admitted, the inbox is not granted:\n%s", got)
	}
	got := render(nil)
	for _, not := range []string{"agent.w1.B.role.lead.inbox", "agent.w1.B.role.research-supervisor.inbox"} {
		if strings.Contains(got, not) {
			t.Errorf("with lead unadmitted, %q is granted:\n%s", not, got)
		}
	}
}

// Test 3: what the grant does not name, one by one.
func TestSupervisorPeerGrantNamesNothingBeyondTheRoleInbox(t *testing.T) {
	t.Parallel()
	ps, err := DeclaredPrincipals(peerSpec())
	if err != nil {
		t.Fatal(err)
	}
	a := principalNamed(t, ps, "A.supervisor")
	for _, not := range []string{
		"agent.w1.B.someid.inbox",
		"agent.w1.B.*.inbox",
		"agent.w1.B.>",
		"agent.w1.B.role.builder.inbox",
		"agent.w1.B.role.research-supervisor.inbox",
		"agent.w1.B.broadcast",
		"agent.w1.broadcast",
		"agent.w1.D.role.supervisor.inbox", // D has no supervisor
	} {
		if contains(a.Publish, not) {
			t.Errorf("A.supervisor may publish %q; the grant must not name it", not)
		}
	}
	for _, e := range peerEntries(a) {
		if strings.ContainsAny(e, "*>") {
			t.Errorf("peer entry %q carries a wildcard token", e)
		}
	}
	wantSub := append([]string{"agent.w1.A.>", "agent.w1.broadcast"}, plumbingSubscribe...)
	wantSub = append(wantSub, "global.kinu.supervisor.inbox")
	if !slices.Equal(a.Subscribe, wantSub) {
		t.Errorf("A.supervisor subscribe = %v\nwant %v; the grant is publish only", a.Subscribe, wantSub)
	}
}

// Test 4: the team user and a team with no supervisor get no peer entry, and
// each team user's lists are exactly what they were before the grant: its own
// subtree and plumbing, plus the global publishes for a team with a supervisor.
func TestSupervisorPeerGrantIsOnThePerRoleUserOnly(t *testing.T) {
	t.Parallel()
	ps := mustPrincipals(t, peerSpec())
	globalPub := []string{"global.director.inbox", "global.*.supervisor.inbox", "$JS.global.API.>"}
	for _, c := range []struct {
		name, own string
		global    bool
	}{
		{"A", "agent.w1.A.>", true},
		{"B", "agent.w1.B.>", true},
		{"C", "agent.w2.C.>", true},
		{"D", "agent.w1.D.>", false},
	} {
		want := append([]string{c.own, "agent.audit"}, plumbingPublish...)
		if c.global {
			want = append(want, globalPub...)
		}
		got := principalNamed(t, ps, c.name)
		if !slices.Equal(got.Publish, want) {
			t.Errorf("team user %s publish = %v\nwant %v", c.name, got.Publish, want)
		}
		if n := peerEntries(got); len(n) != 0 {
			t.Errorf("team user %s carries peer entries %v", c.name, n)
		}
	}
}

// Test 5: no hub, no per-role user, so no peer grant.
func TestNoPeerGrantWithoutAHub(t *testing.T) {
	t.Parallel()
	s := peerSpec()
	s.HubURL = ""
	for _, p := range mustPrincipals(t, s) {
		if got := peerEntries(p); len(got) != 0 {
			t.Errorf("%s carries peer entries %v with no hub", p.Name, got)
		}
		if strings.Contains(p.Name, ".") {
			t.Errorf("a per-role user %s rendered with no hub", p.Name)
		}
	}
}

// Test 6: a team removed at apply is gone from every list, and a render with
// nothing changed rewrites no file.
func TestSupervisorPeerGrantDropsARemovedTeamAndIsStable(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", URL: "nats://127.0.0.1:4222", StoreDir: dir, HubURL: "tls://hub.example:7422"}
	live := &teams{
		{Name: "A", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}}},
		{Name: "B", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}}},
		{Name: "C", Workspace: "w2", Roles: []api.Role{{Name: "supervisor"}}},
	}
	m, err := NewManager(dir, "kinu", rb, live, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if changed, err := m.Regenerate(); err != nil || changed {
		t.Fatalf("a second render with nothing changed = changed %v, err %v; want no rewrite", changed, err)
	}
	conf := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, AuthName))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if !strings.Contains(conf(), `"agent.w1.B.role.supervisor.inbox"`) || !strings.Contains(conf(), `"agent.w2.C.role.supervisor.inbox"`) {
		t.Fatalf("the rendered file lacks the peer entries:\n%s", conf())
	}
	*live = (*live)[:1:1]
	*live = append(*live, api.Team{Name: "C", Workspace: "w2", Roles: []api.Role{{Name: "supervisor"}}})
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	got := conf()
	if strings.Contains(got, "agent.w1.B.role.supervisor.inbox") {
		t.Errorf("a removed team's inbox is still granted:\n%s", got)
	}
	if !strings.Contains(got, `"agent.w2.C.role.supervisor.inbox"`) {
		t.Errorf("a kept team's inbox lost its entry:\n%s", got)
	}
}

// Test 7: the broker conf (leafnodes block included) does not change with the
// grant: it is the same text with or without the supervisors' peer entries.
func TestPeerGrantLeavesTheBrokerConfAlone(t *testing.T) {
	t.Parallel()
	with, err := RenderConf(peerSpec())
	if err != nil {
		t.Fatal(err)
	}
	flat := peerSpec()
	for i := range flat.Teams {
		flat.Teams[i].GlobalRoles = nil
	}
	without, err := RenderConf(flat)
	if err != nil {
		t.Fatal(err)
	}
	if with != without {
		t.Errorf("RenderConf differs when the teams declare supervisors:\n%s\n---\n%s", with, without)
	}
}

func mustPrincipals(t *testing.T, s Spec) []Principal {
	t.Helper()
	ps, err := DeclaredPrincipals(s)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// peerBroker starts a managed broker for teams A and B of workspace w1 (each
// with a supervisor; B also a builder) and returns the manager
// and a connect helper that presents the credential a seat of a role gets.
func peerBroker(t *testing.T, live *teams) *Manager {
	t.Helper()
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	domain := "t" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir, HubURL: "tls://127.0.0.1:1"}
	m, err := NewManager(dir, domain, rb, live, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSupervisor(m, filepath.Join(root, "run"), filepath.Join(root, "log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	attach(t, s, newProcTable(t))
	s.backoff = func(int) time.Duration { return 200 * time.Millisecond }
	s.dialTimeout = loadedBrokerWait
	s.AfterReady = func() error {
		a := m.Admin()
		_, err := Provision(context.Background(), m.URL(), a.Name, a.Password)
		return err
	}
	m.Reloader = s
	t.Cleanup(func() { s.Stop(false) })
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func peerTeams() *teams {
	return &teams{
		{Name: "A", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}}},
		{Name: "B", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}, {Name: "builder"}}},
	}
}

// connectAs dials with the credential a seat of (team, role) is handed once
// the broker has accepted it, and routes the client's error callback to the
// returned channel.
func connectAs(t *testing.T, m *Manager, team, role string) (*nats.Conn, chan string) {
	t.Helper()
	var user, pw string
	eventually(t, "the credential for "+team+"/"+role+" to be handed out", func() bool {
		var ok bool
		user, pw, ok = m.Credential(team, role)
		if role == "supervisor" {
			return ok && user == team+"."+role
		}
		return ok
	})
	violations := make(chan string, 16)
	nc, err := nats.Connect(m.URL(), nats.UserInfo(user, pw), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		violations <- err.Error()
	}))
	if err != nil {
		t.Fatalf("%s/%s does not authenticate as %s: %v", team, role, user, err)
	}
	t.Cleanup(nc.Close)
	return nc, violations
}

func inboxMsgs(t *testing.T, nc *nats.Conn) uint64 {
	t.Helper()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := js.Stream(ctx, InboxStream)
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

// Tests 8, 10 and 11: against a real broker, A's supervisor publishes B's
// supervisor role inbox and gets a JetStream ack, B's supervisor reads it;
// the near misses are refused with the broker's own violation and the stream
// holds nothing new; B's builder (the team user) cannot publish A's inbox.
func TestSupervisorPeerPublishAgainstARealBroker(t *testing.T) {
	m := peerBroker(t, peerTeams())

	aSup, aViol := connectAs(t, m, "A", "supervisor")
	bSup, _ := connectAs(t, m, "B", "supervisor")
	bBuilder, bbViol := connectAs(t, m, "B", "builder")

	const peerSubject = "agent.w1.B.role.supervisor.inbox"
	got, err := bSup.SubscribeSync(peerSubject)
	if err != nil {
		t.Fatal(err)
	}
	if err := bSup.Flush(); err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(aSup)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ack, err := js.Publish(ctx, peerSubject, []byte("from A"))
	if err != nil {
		t.Fatalf("A.supervisor could not publish %s: %v", peerSubject, err)
	}
	if ack.Stream != InboxStream {
		t.Errorf("ack stream = %q, want %q", ack.Stream, InboxStream)
	}
	msg, err := got.NextMsg(5 * time.Second)
	if err != nil || string(msg.Data) != "from A" {
		t.Fatalf("B's supervisor read %v, %v; want the message from A", msg, err)
	}

	// Test 10: the near misses.
	before := inboxMsgs(t, aSup)
	drainViolations(aViol)
	for _, subject := range []string{
		"agent.w1.B.someid.inbox",
		"agent.w1.B.role.builder.inbox",
		"agent.w1.B.broadcast",
	} {
		if err := aSup.Publish(subject, []byte("x")); err != nil {
			t.Fatalf("publish %s: %v", subject, err)
		}
		_ = aSup.Flush()
		if !receivesViolation(aViol, subject, 5*time.Second) {
			t.Errorf("A.supervisor publishing %s got no Permissions Violation", subject)
		}
	}
	if after := inboxMsgs(t, aSup); after != before {
		t.Errorf("the inbox stream grew from %d to %d on refused publishes", before, after)
	}

	// Test 11: B's team user is refused A's supervisor inbox, as today.
	drainViolations(bbViol)
	const toA = "agent.w1.A.role.supervisor.inbox"
	if err := bBuilder.Publish(toA, []byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = bBuilder.Flush()
	if !receivesViolation(bbViol, toA, 5*time.Second) {
		t.Errorf("B's builder publishing %s got no Permissions Violation", toA)
	}
}

// Test 9, and the step 0 question: a supervisor connected under the file
// from before the grant gets it on the SAME connection after the file is
// re-rendered and the broker is signalled to reload. The control is that the
// same publish is refused before the reload.
func TestSupervisorPeerGrantReachesAnAlreadyConnectedClientOnReload(t *testing.T) {
	live := &teams{{Name: "A", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}}}}
	m := peerBroker(t, live)
	aSup, aViol := connectAs(t, m, "A", "supervisor")

	const peerSubject = "agent.w1.B.role.supervisor.inbox"
	if err := aSup.Publish(peerSubject, []byte("before")); err != nil {
		t.Fatal(err)
	}
	_ = aSup.Flush()
	if !receivesViolation(aViol, peerSubject, 5*time.Second) {
		t.Fatal("control: the publish was not refused before team B existed")
	}

	*live = append(*live, api.Team{Name: "B", Workspace: "w1", Roles: []api.Role{{Name: "supervisor"}}})
	if changed, err := m.Regenerate(); err != nil || !changed {
		t.Fatalf("Regenerate = changed %v, err %v; want a rewrite and a reload", changed, err)
	}
	drainViolations(aViol)
	js, err := jetstream.New(aSup)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the reloaded grant to reach the connected client", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := js.Publish(ctx, peerSubject, []byte("after"))
		return err == nil
	})
}
