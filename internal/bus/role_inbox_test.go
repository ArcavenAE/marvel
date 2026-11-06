package bus

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/arcavenae/marvel/contracts/go/envelope"
)

func TestRoleInboxSubject(t *testing.T) {
	t.Parallel()
	got, err := RoleInboxSubject("aae", "migrated", "supervisor")
	if err != nil || got != "agent.aae.migrated.role.supervisor.inbox" {
		t.Fatalf("subject = %q, %v", got, err)
	}
	for _, bad := range [][3]string{
		{"aae", "Team_A", "supervisor"},
		{"a.b", "team", "supervisor"},
		{"aae", "team", "super*"},
		{"aae", "team", "super visor"},
		{"aae", "team>", "supervisor"},
		{"", "team", "supervisor"},
		{"aae", "team", strings.Repeat("x", 33)},
	} {
		if s, err := RoleInboxSubject(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("RoleInboxSubject(%q) = %q, want a refusal", bad, s)
		}
	}
}

// A publish with a token that is not addressable is refused before any dial:
// the credential source is never asked.
func TestPublishRoleInboxRefusesBeforeDialing(t *testing.T) {
	t.Parallel()
	asked := false
	p := NewRoleInboxPublisher(func() (string, string, string) {
		asked = true
		return "nats://127.0.0.1:1", "u", "p"
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.PublishRoleInbox(ctx, "aae", "Team_A", "supervisor", envelope.Envelope{}, "id"); err == nil {
		t.Fatal("want a refusal for an unaddressable team")
	}
	if asked {
		t.Fatal("the credential source was asked for a publish that should have been refused first")
	}
}

// With no address or admin identity yet, a publish fails with a reason and does
// not hang.
func TestPublishRoleInboxWithoutABusFails(t *testing.T) {
	t.Parallel()
	p := NewRoleInboxPublisher(func() (string, string, string) { return "", "", "" })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	env := envelope.Envelope{
		SchemaVersion: 1,
		MessageID:     "01M5RQ6HW082MW6HWQD96T7WGD",
		Sender:        envelope.Sender{AgentID: "marvel", Workspace: "aae"},
		Recipient:     envelope.Recipient{Address: "role://team/supervisor"},
		Performative:  envelope.PerformativeREQUEST,
		Content:       envelope.Content{Type: envelope.TypeSignal, Data: "x"},
		SentAt:        time.Now().UTC(),
	}
	err := p.PublishRoleInbox(ctx, "aae", "team", "supervisor", env, "id")
	if err == nil || !strings.Contains(err.Error(), "no address") {
		t.Fatalf("err = %v, want the missing bus named", err)
	}
}

func TestSeatInboxSubject(t *testing.T) {
	t.Parallel()
	got, err := SeatInboxSubject("ws", "team-a", "team-a-supervisor-g1-0")
	if err != nil || got != "agent.ws.team-a.team-a-supervisor-g1-0.inbox" {
		t.Fatalf("subject = %q, %v", got, err)
	}
	for _, bad := range [][3]string{
		{"aae", "Team_A", "seat-0"},
		{"aae", "team", "seat.0"},
		{"aae", "team", "seat*"},
		{"aae", "team", "seat>"},
		{"aae", "team", ""},
		{"aae", "team", strings.Repeat("x", 33)},
	} {
		if s, err := SeatInboxSubject(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("SeatInboxSubject(%q) = %q, want a refusal", bad, s)
		}
	}
}

func TestDirectorInboxSubjectIsTheGlobalAddress(t *testing.T) {
	t.Parallel()
	if DirectorInboxSubject != "global.director.inbox" {
		t.Errorf("DirectorInboxSubject = %q, want the director's global subject", DirectorInboxSubject)
	}
}

// The seat publish is refused before any dial on a name outside the address
// class, as the role publish is.
func TestPublishSeatInboxRefusesBeforeDialing(t *testing.T) {
	t.Parallel()
	asked := false
	p := NewRoleInboxPublisher(func() (string, string, string) { asked = true; return "nats://127.0.0.1:1", "admin", "x" })
	err := p.PublishSeatInbox(context.Background(), "aae", "Team_A", "seat-0", envelope.Envelope{}, "id")
	if err == nil {
		t.Fatal("a publish to an unaddressable team was accepted")
	}
	if asked {
		t.Error("the credential source was asked before the name was refused")
	}
}

// startJetStream runs a nats-server with JetStream on a free localhost port and
// returns its URL, or skips when the binary is not on PATH.
func startJetStream(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	markStarted(fmt.Sprintf("127.0.0.1:%d", port))
	cmd := exec.Command(bin, "-js", "-a", "127.0.0.1", "-p", fmt.Sprint(port), "-sd", t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start nats-server: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if nc, err := nats.Connect(url); err == nil {
			nc.Close()
			return url
		}
	}
	t.Fatal("nats-server did not come up")
	return ""
}

func testEnvelope(address string) envelope.Envelope {
	corr := "handoff-missing/aae/squad/squad-worker-g1-0/1"
	return envelope.Envelope{
		SchemaVersion: 1,
		MessageID:     "01M6QNPPP6PK48RHQYKQSV9W7R",
		CorrelationID: &corr,
		Sender:        envelope.Sender{AgentID: "marvel", Workspace: "aae"},
		Recipient:     envelope.Recipient{Address: address},
		Performative:  envelope.PerformativeREQUEST,
		Authority:     &envelope.Authority{Strength: envelope.StrengthNone},
		Content:       envelope.Content{Type: envelope.TypeSignal, Data: "handoff-missing: test"},
		SentAt:        time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC),
	}
}

// Each public publish lands on its own subject in a real stream, the envelope
// reads back valid, and a second publish with the same dedupe id is dropped by
// the stream. The stream holds the subjects the real broker declares for the
// local inbox and the director's global address.
func TestPublishersLandOnTheirSubjectsInAStream(t *testing.T) {
	url := startJetStream(t)
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.AddStream(&nats.StreamConfig{Name: "INBOX", Subjects: []string{"agent.>", "global.director.inbox"}}); err != nil {
		t.Fatal(err)
	}
	p := NewRoleInboxPublisher(func() (string, string, string) { return url, "marvel", "x" })
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.PublishRoleInbox(ctx, "aae", "squad", "supervisor", testEnvelope("role://squad/supervisor"), "role-1"); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := p.PublishSeatInbox(ctx, "aae", "squad", "squad-supervisor-g1-1", testEnvelope("agent://squad/squad-supervisor-g1-1"), "seat-1"); err != nil {
		t.Fatalf("seat: %v", err)
	}
	if err := p.PublishDirectorInbox(ctx, testEnvelope("global://director"), "dir-1"); err != nil {
		t.Fatalf("director: %v", err)
	}
	if err := p.PublishDirectorInbox(ctx, testEnvelope("global://director"), "dir-1"); err != nil {
		t.Fatalf("director repeat: %v", err)
	}

	want := map[string]string{
		"agent.aae.squad.role.supervisor.inbox":       "role://squad/supervisor",
		"agent.aae.squad.squad-supervisor-g1-1.inbox": "agent://squad/squad-supervisor-g1-1",
		"global.director.inbox":                       "global://director",
	}
	for subject, address := range want {
		info, err := js.StreamInfo("INBOX", &nats.StreamInfoRequest{SubjectsFilter: subject})
		if err != nil {
			t.Fatal(err)
		}
		if n := info.State.Subjects[subject]; n != 1 {
			t.Errorf("%s holds %d messages, want 1 (a repeat with the same dedupe id is dropped)", subject, n)
			continue
		}
		raw, err := js.GetLastMsg("INBOX", subject)
		if err != nil {
			t.Fatal(err)
		}
		if err := envelope.Validate(raw.Data); err != nil {
			t.Errorf("%s: stored envelope does not validate: %v", subject, err)
		}
		if !strings.Contains(string(raw.Data), address) {
			t.Errorf("%s: stored envelope does not carry the recipient %s", subject, address)
		}
	}
}
