package bus

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The supervises grant against a real scratch broker on loopback, with no hub
// configured: the broker's own refusal is the evidence, not the rendered file.

func supervisesBroker(t *testing.T, live *teams) *Manager {
	t.Helper()
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	domain := "t" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir}
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

func supervisesBrokerTeams() *teams {
	return &teams{
		{Name: "product", Workspace: "dtu", Roles: []api.Role{
			{Name: "supervisor", Supervises: []string{"midway"}},
			{Name: "reviewer"},
		}},
		{Name: "midway", Workspace: "dtu", Roles: []api.Role{{Name: "architect"}, {Name: "worker"}}},
		{Name: "vantage", Workspace: "dtu", Roles: []api.Role{{Name: "lead"}}},
	}
}

// connectUser dials as the credential of (team, role) once it is handed out
// and equal to want.
func connectUser(t *testing.T, m *Manager, team, role, want string) (*nats.Conn, chan string) {
	t.Helper()
	var user, pw string
	eventually(t, "the credential for "+team+"/"+role+" to be "+want, func() bool {
		var ok bool
		user, pw, ok = m.Credential(team, role)
		return ok && user == want
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

// Both directions, against a real broker with no hub: the supervisor
// publishes a supervised role's inbox and gets a JetStream ack, a supervised
// role publishes the supervisor's inbox, and the near misses (an agent-id
// inbox, an unnamed team, a sibling role of the supervisor's team, the team
// user) are refused with the broker's own violation.
func TestSupervisesPublishAgainstARealBrokerWithNoHub(t *testing.T) {
	m := supervisesBroker(t, supervisesBrokerTeams())
	sup, supViol := connectUser(t, m, "product", "supervisor", "product.supervisor")
	arch, archViol := connectUser(t, m, "midway", "architect", "midway.architect")
	rev, revViol := connectUser(t, m, "product", "reviewer", "product")

	const down = "agent.dtu.midway.role.architect.inbox"
	got, err := arch.SubscribeSync(down)
	if err != nil {
		t.Fatal(err)
	}
	if err := arch.Flush(); err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(sup)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := js.Publish(ctx, down, []byte("from the supervisor")); err != nil {
		t.Fatalf("the supervisor could not publish %s: %v", down, err)
	}
	if msg, err := got.NextMsg(5 * time.Second); err != nil || string(msg.Data) != "from the supervisor" {
		t.Fatalf("the architect read %v, %v; want the supervisor's message", msg, err)
	}

	const up = "agent.dtu.product.role.supervisor.inbox"
	supGot, err := sup.SubscribeSync(up)
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Flush(); err != nil {
		t.Fatal(err)
	}
	ajs, err := jetstream.New(arch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ajs.Publish(ctx, up, []byte("from the architect")); err != nil {
		t.Fatalf("the architect could not publish %s: %v", up, err)
	}
	if msg, err := supGot.NextMsg(5 * time.Second); err != nil || string(msg.Data) != "from the architect" {
		t.Fatalf("the supervisor read %v, %v; want the architect's message", msg, err)
	}

	refused := func(nc *nats.Conn, viol chan string, who, subject string) {
		t.Helper()
		drainViolations(viol)
		if err := nc.Publish(subject, []byte("x")); err != nil {
			t.Fatalf("publish %s: %v", subject, err)
		}
		_ = nc.Flush()
		if !receivesViolation(viol, subject, 5*time.Second) {
			t.Errorf("%s publishing %s got no Permissions Violation", who, subject)
		}
	}
	before := inboxMsgs(t, sup)
	refused(sup, supViol, "the supervisor", "agent.dtu.midway.someid.inbox")
	refused(sup, supViol, "the supervisor", "agent.dtu.vantage.role.lead.inbox")
	refused(arch, archViol, "the architect", "agent.dtu.product.someid.inbox")
	refused(arch, archViol, "the architect", "agent.dtu.product.role.reviewer.inbox")
	refused(arch, archViol, "the architect", "agent.dtu.vantage.role.lead.inbox")
	refused(rev, revViol, "the product reviewer", down)
	if after := inboxMsgs(t, sup); after != before {
		t.Errorf("the inbox stream grew from %d to %d on refused publishes", before, after)
	}
}

// An already-connected supervisor gets a newly declared team's grant on the
// same connection after the re-render and reload; before it, the same publish
// is refused.
func TestSupervisesGrantReachesAnAlreadyConnectedClientOnReload(t *testing.T) {
	live := supervisesBrokerTeams()
	m := supervisesBroker(t, live)
	sup, viol := connectUser(t, m, "product", "supervisor", "product.supervisor")

	const subject = "agent.dtu.vantage.role.lead.inbox"
	if err := sup.Publish(subject, []byte("before")); err != nil {
		t.Fatal(err)
	}
	_ = sup.Flush()
	if !receivesViolation(viol, subject, 5*time.Second) {
		t.Fatal("control: the publish was not refused before vantage was named")
	}

	(*live)[0].Roles[0].Supervises = []string{"midway", "vantage"}
	if changed, err := m.Regenerate(); err != nil || !changed {
		t.Fatalf("Regenerate = changed %v, err %v; want a rewrite and a reload", changed, err)
	}
	drainViolations(viol)
	js, err := jetstream.New(sup)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the reloaded grant to reach the connected client", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := js.Publish(ctx, subject, []byte("after"))
		return err == nil
	})
}
