package bus

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/events"
)

// TestProvisionCreatesThenFindsAgainstRealBroker runs the provisioning twice
// against a supervised nats-server: the first pass creates all three
// objects, the second finds them and changes nothing. Then it checks the
// server-side configs carry precondition 6's parameters, since that spec
// is what connected the shim on the second host.
func TestProvisionCreatesThenFindsAgainstRealBroker(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin := m.Admin()

	first, err := Provision(ctx, m.URL(), admin.Name, admin.Password)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Created) != 3 || len(first.Existed) != 0 {
		t.Fatalf("first pass = %+v, want three created", first)
	}
	second, err := Provision(ctx, m.URL(), admin.Name, admin.Password)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Created) != 0 || len(second.Existed) != 3 {
		t.Fatalf("second pass = %+v, want three found", second)
	}
	if got := second.String(); got != "found AGENT_INBOX, AGENT_AUDIT, AGENT_STATE" {
		t.Errorf("String() = %q", got)
	}

	nc, err := nats.Connect(m.URL(), nats.UserInfo(admin.Name, admin.Password))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := js.Stream(ctx, InboxStream)
	if err != nil {
		t.Fatal(err)
	}
	ic := inbox.CachedInfo().Config
	if strings.Join(ic.Subjects, ",") != "agent.*.*.*.inbox,agent.*.*.role.*.inbox" || ic.MaxAge != 24*time.Hour || ic.MaxMsgSize != 65536 || ic.Duplicates != 2*time.Minute || ic.Storage != jetstream.FileStorage || ic.Retention != jetstream.LimitsPolicy {
		t.Errorf("AGENT_INBOX config = %+v", ic)
	}
	audit, err := js.Stream(ctx, AuditStream)
	if err != nil {
		t.Fatal(err)
	}
	if ac := audit.CachedInfo().Config; strings.Join(ac.Subjects, ",") != "agent.audit" || ac.MaxAge != 720*time.Hour {
		t.Errorf("AGENT_AUDIT config = %+v", ac)
	}
	kv, err := js.KeyValue(ctx, StateBucket)
	if err != nil {
		t.Fatal(err)
	}
	st, err := kv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.TTL() != 90*time.Second {
		t.Errorf("AGENT_STATE ttl = %s, want 90s", st.TTL())
	}
}

func TestProvisionUnreachableBrokerNamesTheURL(t *testing.T) {
	t.Parallel()
	_, err := Provision(context.Background(), "nats://127.0.0.1:1", "a", "b")
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("err = %v", err)
	}
}

// TestProvisionAfterAdoptWithFreshPasswords is the ticket 5 live-check
// failure as a test: the successor daemon mints new passwords and adopts a
// broker that still holds the old ones. The adopt path reloads and the
// provisioning connect rides out the reload, so the new admin identity works.
func TestProvisionAfterAdoptWithFreshPasswords(t *testing.T) {
	s1, m1, _ := newTestSupervisor(t, "")
	if err := s1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s1.Status().PID
	s1.Stop(true)
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// A new manager over the same directory: fresh admin and team passwords,
	// rewritten to disk, unknown to the running broker.
	m2, err := NewManager(m1.Dir(), m1.Domain(), m1.bus, m1.teams, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if m2.Admin().Password == m1.Admin().Password {
		t.Fatal("test premise: successor minted the same admin password")
	}
	s2, err := NewSupervisor(m2, filepath.Dir(s1.pidFile), filepath.Dir(s1.logPath), events.NewRing(16))
	if err != nil {
		t.Fatal(err)
	}
	s2.dialTimeout = 5 * time.Second
	m2.Reloader = s2
	t.Cleanup(func() { s2.Stop(false) })
	provisioned := false
	s2.AfterReady = func() error {
		admin := m2.Admin()
		got, perr := Provision(context.Background(), m2.URL(), admin.Name, admin.Password)
		provisioned = perr == nil
		if perr != nil {
			return perr
		}
		if len(got.Created) != 3 {
			return fmt.Errorf("created %v, want three objects on a fresh broker", got.Created)
		}
		return nil
	}
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("successor start: %v", err)
	}
	if !provisioned || !s2.Status().Adopted {
		t.Fatalf("provisioned=%v adopted=%v", provisioned, s2.Status().Adopted)
	}
}
