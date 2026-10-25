package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/arcavenae/marvel/contracts/go/envelope"
)

// RoleInboxPublisher publishes one envelope to a role's inbox under the admin
// identity (docs/design/handoff-missing-delivery.md, ruling 3 of 2026-10-02:
// marvel publishes as itself, with no new scoped principal). It is the
// controller's only bus capability, so it builds the subject itself from
// validated tokens and can publish nowhere else, although the principal behind
// it can publish to ">". The connection is long-lived and opened on the first
// publish, not at daemon start (architect answers on marvel#459, 2026-10-25).
type RoleInboxPublisher struct {
	creds func() (url, user, password string)

	mu sync.Mutex
	nc *nats.Conn
}

// NewRoleInboxPublisher returns a publisher that reads the broker address and
// admin credential from creds on each connect, so a broker restarted with new
// credentials is reached without rebuilding the publisher.
func NewRoleInboxPublisher(creds func() (url, user, password string)) *RoleInboxPublisher {
	return &RoleInboxPublisher{creds: creds}
}

// subjectToken is the canonical schema's character class for one address
// segment, which is also what makes a token safe to place in a subject.
var subjectToken = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// RoleInboxSubject is the subject a role's mail is delivered on:
// agent.<ws>.<team>.role.<role>.inbox. A token outside the schema's address
// class is refused, so no wildcard, separator or space can be smuggled in.
func RoleInboxSubject(ws, team, role string) (string, error) {
	for _, tok := range [...]struct{ name, v string }{{"workspace", ws}, {"team", team}, {"role", role}} {
		if !subjectToken.MatchString(tok.v) {
			return "", fmt.Errorf("%s %q is not an addressable name", tok.name, tok.v)
		}
	}
	return "agent." + ws + "." + team + ".role." + role + ".inbox", nil
}

// PublishRoleInbox publishes env to the role's inbox and waits for the stream's
// acknowledgement. msgID is the JetStream dedupe id (Nats-Msg-Id), so a re-send
// after a crash between publish and record is dropped inside the stream's
// dedupe window. ctx bounds the dial and the publish.
func (p *RoleInboxPublisher) PublishRoleInbox(ctx context.Context, ws, team, role string, env envelope.Envelope, msgID string) error {
	subject, err := RoleInboxSubject(ws, team, role)
	if err != nil {
		return err
	}
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	if err := envelope.Validate(data); err != nil {
		return fmt.Errorf("envelope does not validate: %w", err)
	}
	nc, err := p.conn(ctx)
	if err != nil {
		return err
	}
	js, err := nc.JetStream(nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	msg.Header.Set(nats.MsgIdHdr, msgID)
	if _, err := js.PublishMsg(msg, nats.Context(ctx)); err != nil {
		p.reset(nc)
		return fmt.Errorf("publish to %s: %w", subject, err)
	}
	return nil
}

// conn returns the long-lived connection, dialing it when there is none or it
// was closed.
func (p *RoleInboxPublisher) conn(ctx context.Context) (*nats.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nc != nil && !p.nc.IsClosed() {
		return p.nc, nil
	}
	url, user, password := p.creds()
	if url == "" || user == "" {
		return nil, fmt.Errorf("the bus has no address or admin identity yet")
	}
	timeout := 2 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left > 0 && left < timeout {
			timeout = left
		}
	}
	nc, err := nats.Connect(url,
		nats.UserInfo(user, password),
		nats.Name("marvel escalation"),
		nats.Timeout(timeout),
		nats.RetryOnFailedConnect(false),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to %s as %s: %w", url, user, err)
	}
	p.nc = nc
	return nc, nil
}

// reset drops nc so the next publish dials again.
func (p *RoleInboxPublisher) reset(nc *nats.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nc == nc {
		nc.Close()
		p.nc = nil
	}
}

// Close closes the connection, if one was opened.
func (p *RoleInboxPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nc != nil {
		p.nc.Close()
		p.nc = nil
	}
}
