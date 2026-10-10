package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/broker"
)

// storeSessionAuth lets the broker check a seat against the session store.
// An unknown session and a wrong token read the same to the caller: the
// broker has no reason to say which session keys exist.
type storeSessionAuth struct{ store *api.Store }

func (a storeSessionAuth) Authenticate(key, token string) (broker.Identity, error) {
	sess, err := a.store.AuthenticateLiveSession(key, token)
	if err != nil {
		return broker.Identity{}, fmt.Errorf("%w: %w", broker.ErrUnauthorized, err)
	}
	return broker.Identity{Session: sess.Key(), Team: sess.Team, Role: sess.Role}, nil
}

// UseBrokerDriver turns the credential broker on with drv as its driver. A
// daemon that never calls it answers broker.token with a refusal and issues
// nothing.
func (d *Daemon) UseBrokerDriver(drv broker.Driver) {
	d.broker = broker.New(drv, storeSessionAuth{store: d.store})
}

// handleBrokerToken issues one short-lived token to a seat that proves it
// holds its session's heartbeat token. The response is the only place the
// token value appears: it is not logged, stored or put on the event ring.
func (d *Daemon) handleBrokerToken(ctx context.Context, params json.RawMessage) Response {
	if d.broker == nil {
		return Response{Error: "no credential broker is configured on this daemon"}
	}
	var p api.BrokerTokenRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	tok, err := d.broker.Issue(ctx, p.SessionKey, p.SessionToken)
	if err != nil {
		if errors.Is(err, broker.ErrUnauthorized) {
			return Response{Error: broker.ErrUnauthorized.Error()}
		}
		return Response{Error: err.Error()}
	}
	return respond(api.BrokerTokenResponse{ID: tok.ID, Value: tok.Value, Expires: tok.Expires})
}
