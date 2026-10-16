package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

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

// UseBrokerDriver turns the credential broker on with drv as its driver and
// points every seat's environment at helper. A daemon that never calls it
// answers broker.token with a refusal and issues nothing.
func (d *Daemon) UseBrokerDriver(drv broker.Driver, helper string) {
	d.sessMgr.CredentialHelper = helper
	d.broker = broker.New(drv, storeSessionAuth{store: d.store})
	b := d.broker
	d.sessMgr.SessionEnded = func(key string) {
		if err := b.RevokeSession(context.Background(), key); err != nil {
			log.Printf("warning: session %s: revoke broker tokens: %v", key, err)
		}
	}
}

// RunBrokerRetry retries, on a tick, the broker tokens of seats that are gone
// and whose revoke failed. A seat's end is reported once, so without this a
// token the provider would not revoke at that moment stays live until its TTL.
// The kept tokens are in memory only: a restart forgets them.
func (d *Daemon) RunBrokerRetry(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.retryBrokerRevokes(ctx)
		}
	}
}

// retryBrokerRevokes is one pass of RunBrokerRetry. A daemon with no broker
// has nothing to retry.
func (d *Daemon) retryBrokerRevokes(ctx context.Context) {
	b := d.broker
	if b == nil {
		return
	}
	// A key is held back only while a Running row has it. A Crashed, Failed or
	// Succeeded row is not a seat to protect, and the tokens kept for it are
	// retried now, not at their TTL. For those rows this is the first revoke,
	// not a retry, and a row in backoff or marked Failed by the health loop can
	// still have a live pane. The pass revokes only ids it read before asking,
	// so a seat that takes the name meanwhile is not touched.
	exists := func(key string) bool {
		sess, err := d.store.GetSession(key)
		return err == nil && sess.State == api.SessionRunning
	}
	if err := b.RetryRevokes(ctx, exists); err != nil {
		log.Printf("warning: retry revoke of broker tokens: %v", err)
	}
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
		if errors.Is(err, broker.ErrRevokeFailed) {
			// The refusal below hides a live credential: say so, by id and never by value.
			log.Printf("warning: session %s: %v", p.SessionKey, err)
		}
		if errors.Is(err, broker.ErrUnauthorized) {
			return Response{Error: broker.ErrUnauthorized.Error()}
		}
		return Response{Error: err.Error()}
	}
	return respond(api.BrokerTokenResponse{ID: tok.ID, Value: tok.Value, Expires: tok.Expires})
}
