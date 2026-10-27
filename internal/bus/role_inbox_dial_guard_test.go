package bus

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
)

// The escalation publisher dials the broker under the admin identity, so a
// test that hands it a live bus address would reach the real broker and put a
// scratch password in its authentication log (marvel#998 is the same hazard on
// the provisioning dial). These tests reach the publisher's own dial and check
// it answers to the package's dial guard. Neither names a bus port: one uses a
// loopback port this test opened and closed, the other a listener it holds.

func inboxEnvelope() envelope.Envelope {
	return envelope.Envelope{
		SchemaVersion: 1,
		MessageID:     "01M5RQ6HW082MW6HWQD96T7WGD",
		Sender:        envelope.Sender{AgentID: "marvel", Workspace: "aae"},
		Recipient:     envelope.Recipient{Address: "role://team/supervisor"},
		Performative:  envelope.PerformativeREQUEST,
		Content:       envelope.Content{Type: envelope.TypeSignal, Data: "x"},
		SentAt:        time.Now().UTC(),
	}
}

// A publish to an address no test started is refused before the dial. The
// address is a loopback port opened and closed here and never marked started,
// so without the guard the dial is refused by the host and reaches no broker,
// and the error is a connect failure, not the guard's.
func TestRoleInboxPublisherDialAnswersToTheDialGuard(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "nats://" + ln.Addr().String()
	_ = ln.Close()
	t.Cleanup(func() { forgetRefused(closed) })

	p := NewRoleInboxPublisher(func() (string, string, string) { return closed, "u", "p" })
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = p.PublishRoleInbox(ctx, "aae", "team", "supervisor", inboxEnvelope(), "id")
	if !errors.Is(err, errNotStarted) {
		t.Fatalf("publish to %s = %v, want the dial guard's refusal", closed, err)
	}
}

// An address a test started passes the guard and is dialed. The listener
// accepts the connection and never speaks, so the dial ends at its own timeout
// with a connect error, which shows the guard let it through.
func TestRoleInboxPublisherDialsABrokerTheTestStarted(t *testing.T) {
	ln := listenLoopback(t)
	t.Cleanup(func() { _ = ln.Close() })
	addr := "nats://" + ln.Addr().String()

	p := NewRoleInboxPublisher(func() (string, string, string) { return addr, "u", "p" })
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := p.PublishRoleInbox(ctx, "aae", "team", "supervisor", inboxEnvelope(), "id")
	if err == nil {
		t.Fatalf("publish to a listener that never speaks NATS succeeded")
	}
	if errors.Is(err, errNotStarted) {
		t.Fatalf("publish to a started address was refused by the guard: %v", err)
	}
}
