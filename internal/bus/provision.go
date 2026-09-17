package bus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Provisioning makes the managed broker usable, not merely up (brief 10
// section 5, aae-orc-apeoc; finding-166 fix shape 3). A bare broker accepts
// the shim's connection and then strands it: no AGENT_INBOX or AGENT_AUDIT
// stream, no AGENT_STATE bucket, and with --strict-mcp-config the dead shim
// is a non-error the harness starts without. The daemon owns the moment
// after the listener answers and before any session may spawn, so the three
// objects are created there, as the broker's admin identity, with the exact
// parameters of director sim/twin/README.md precondition 6. Exists is
// success; nothing already present is altered.
const (
	InboxStream  = "AGENT_INBOX"
	AuditStream  = "AGENT_AUDIT"
	StateBucket  = "AGENT_STATE"
	provisionRPC = 10 * time.Second
	// authRetryWindow bounds the wait for a broker reload to land.
	authRetryWindow = 3 * time.Second
	authRetryStep   = 150 * time.Millisecond
)

// inboxSubjects are the two inbox shapes the director shim addresses:
// agent.<ws>.<team>.<id>.inbox and agent.<ws>.<team>.role.<role>.inbox.
var inboxSubjects = []string{"agent.*.*.*.inbox", "agent.*.*.role.*.inbox"}

// Provisioned reports what a Provision call found and made, for the log line
// and the bus.provisioned event.
type Provisioned struct {
	Created []string
	Existed []string
}

// Provision connects to the broker as the admin user and ensures every
// declared object exists. Idempotent: an object already present counts as
// existing and is left exactly as found, so a hand-provisioned broker
// (phase 0) and a marvel-provisioned one converge on the same state
// without a rewrite.
func Provision(ctx context.Context, url, user, password string) (Provisioned, error) {
	var out Provisioned
	nc, err := connectAdmin(ctx, url, user, password)
	if err != nil {
		return out, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return out, fmt.Errorf("jetstream context: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, provisionRPC)
	defer cancel()

	note := func(name string, created bool) {
		if created {
			out.Created = append(out.Created, name)
		} else {
			out.Existed = append(out.Existed, name)
		}
	}

	// Look before creating: CreateStream on an existing stream with an equal
	// config succeeds silently and with a differing one errors, and neither
	// is what "exists is success, leave it as found" means. A lookup first
	// keeps a phase-0 hand-provisioned object exactly as it was. The set is
	// the declared one (declared.go); nothing is created that is not in it.
	for _, obj := range DeclaredObjects() {
		switch obj.Kind {
		case ObjectStream:
			_, err := js.Stream(ctx, obj.Name)
			switch {
			case err == nil:
				note(obj.Name, false)
				continue
			case !errors.Is(err, jetstream.ErrStreamNotFound):
				return out, fmt.Errorf("stream %s: %w", obj.Name, err)
			}
			if _, err := js.CreateStream(ctx, *obj.Stream); err != nil {
				return out, fmt.Errorf("create stream %s: %w", obj.Name, err)
			}
			note(obj.Name, true)
		case ObjectKV:
			_, err := js.KeyValue(ctx, obj.Name)
			switch {
			case err == nil:
				note(obj.Name, false)
				continue
			case !errors.Is(err, jetstream.ErrBucketNotFound):
				return out, fmt.Errorf("kv bucket %s: %w", obj.Name, err)
			}
			if _, err := js.CreateKeyValue(ctx, *obj.KV); err != nil {
				return out, fmt.Errorf("create kv bucket %s: %w", obj.Name, err)
			}
			note(obj.Name, true)
		default:
			return out, fmt.Errorf("declared object %s has unknown kind %q", obj.Name, obj.Kind)
		}
	}
	return out, nil
}

// String renders the outcome for a log line: "created AGENT_INBOX,
// AGENT_AUDIT; found AGENT_STATE".
func (p Provisioned) String() string {
	switch {
	case len(p.Created) == 0:
		return fmt.Sprintf("found %s", join(p.Existed))
	case len(p.Existed) == 0:
		return fmt.Sprintf("created %s", join(p.Created))
	default:
		return fmt.Sprintf("created %s; found %s", join(p.Created), join(p.Existed))
	}
}

func join(names []string) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

// connectAdmin dials as the admin identity. An authorization refusal is
// retried briefly: the daemon has usually just rewritten authorization.conf
// and asked the broker to reload it, and the reload is asynchronous, so the
// first connect can race the old credential set. Anything else fails at once.
func connectAdmin(ctx context.Context, url, user, password string) (*nats.Conn, error) {
	deadline := time.Now().Add(authRetryWindow)
	for {
		nc, err := nats.Connect(url,
			nats.UserInfo(user, password),
			nats.Name("marvel provision"),
			nats.Timeout(provisionRPC),
			nats.MaxReconnects(0),
		)
		if err == nil {
			return nc, nil
		}
		if !errors.Is(err, nats.ErrAuthorization) || time.Now().After(deadline) || ctx.Err() != nil {
			return nil, fmt.Errorf("connect to %s as %s: %w", url, user, err)
		}
		time.Sleep(authRetryStep)
	}
}
