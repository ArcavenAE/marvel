package bus

import (
	"context"
	"strings"
	"testing"
	"time"

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
