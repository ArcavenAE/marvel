package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/broker"
	"github.com/arcavenae/marvel/internal/events"
)

func brokerDaemon(t *testing.T) (*Daemon, *broker.Mock) {
	t.Helper()
	d := newHandlerDaemon(t)
	m := broker.NewMock(15*time.Minute, time.Now)
	d.UseBrokerDriver(m, "/opt/marvel/credential-helper")
	return d, m
}

func brokerParams(t *testing.T, key, token string) json.RawMessage {
	t.Helper()
	return mustMarshal(t, api.BrokerTokenRequest{SessionKey: key, SessionToken: token})
}

func TestBrokerTokenIssuesToTheSeatThatHoldsItsToken(t *testing.T) {
	d, m := brokerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)

	resp := d.handleBrokerToken(context.Background(), brokerParams(t, key, token))
	if resp.Error != "" {
		t.Fatalf("broker.token: %s", resp.Error)
	}
	var got api.BrokerTokenResponse
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(got.Value, "mock.") || got.ID == "" || got.Expires.IsZero() {
		t.Fatalf("response = %+v, want a mock token with an id and an expiry", got)
	}
	if live := m.Live(); len(live) != 1 || live[0] != got.ID {
		t.Fatalf("driver live = %v, want [%s]", live, got.ID)
	}
}

func TestBrokerTokenRefusesWhoCannotProveTheSeat(t *testing.T) {
	d, m := brokerDaemon(t)
	selfKey, _ := registerSession(t, d, "agent-0", true)
	_, peerToken := registerSession(t, d, "agent-1", true)
	legacyKey, _ := registerSession(t, d, "agent-legacy", false)

	for _, c := range []struct{ name, key, token string }{
		{"a peer's token", selfKey, peerToken},
		{"no token", selfKey, ""},
		{"a record with no hash", legacyKey, ""},
		{"an unknown session", "ws/nobody", peerToken},
	} {
		resp := d.handleBrokerToken(context.Background(), brokerParams(t, c.key, c.token))
		if resp.Error == "" || len(resp.Result) != 0 {
			t.Errorf("%s: resp = %+v, want a refusal and no result", c.name, resp)
		}
	}
	if live := m.Live(); len(live) != 0 {
		t.Fatalf("driver issued %v to refused seats", live)
	}
}

func TestBrokerTokenWithNoBrokerSaysSo(t *testing.T) {
	d := newHandlerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)
	resp := d.handleBrokerToken(context.Background(), brokerParams(t, key, token))
	if !strings.Contains(resp.Error, "no credential broker") {
		t.Fatalf("error = %q, want it to say no credential broker is configured", resp.Error)
	}
}

func TestBrokerTokenIsLocalSocketOnly(t *testing.T) {
	if !methodRequiresLocal("broker.token") {
		t.Fatal("broker.token must be local-socket only: its response is bearer authority")
	}
	d, _ := brokerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)
	remoteAdmin := caller{scope: ScopeAdmin, fingerprint: "SHA256:remote", local: false}
	resp := d.dispatchAs(Request{Method: "broker.token", Params: brokerParams(t, key, token)}, remoteAdmin)
	if !strings.Contains(resp.Error, "only available on the local unix socket") {
		t.Fatalf("error = %q, want a local-only refusal", resp.Error)
	}
}

func TestBrokerTokenValueStaysOutOfStateAndEvents(t *testing.T) {
	d, _ := brokerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)
	resp := d.handleBrokerToken(context.Background(), brokerParams(t, key, token))
	var got api.BrokerTokenResponse
	if err := json.Unmarshal(resp.Result, &got); err != nil || got.Value == "" {
		t.Fatalf("no token to look for: err=%v resp=%+v", err, resp)
	}
	sessions, err := json.Marshal(d.store.ListSessions())
	if err != nil {
		t.Fatal(err)
	}
	ring, err := json.Marshal(d.events.Snapshot(events.Filter{}, 0))
	if err != nil {
		t.Fatal(err)
	}
	for name, dump := range map[string]string{"session records": string(sessions), "event ring": string(ring)} {
		if strings.Contains(dump, got.Value) {
			t.Errorf("the issued token is readable in the %s", name)
		}
	}
}

func TestBrokerRevokesAnEndedSeatsTokens(t *testing.T) {
	d, m := brokerDaemon(t)
	keyA, tokenA := registerSession(t, d, "agent-0", true)
	keyB, tokenB := registerSession(t, d, "agent-1", true)
	for _, c := range []struct{ key, token string }{{keyA, tokenA}, {keyA, tokenA}, {keyB, tokenB}} {
		if resp := d.handleBrokerToken(context.Background(), brokerParams(t, c.key, c.token)); resp.Error != "" {
			t.Fatalf("broker.token: %s", resp.Error)
		}
	}
	if d.sessMgr.SessionEnded == nil {
		t.Fatal("the session manager is not told when a seat ends")
	}

	d.sessMgr.SessionEnded(keyA)
	if live := m.Live(); len(live) != 1 {
		t.Fatalf("live tokens after seat A ended = %v, want only seat B's", live)
	}
	d.sessMgr.SessionEnded(keyB)
	if live := m.Live(); len(live) != 0 {
		t.Fatalf("live tokens after both seats ended = %v, want none", live)
	}
}

func TestUseBrokerDriverPointsSeatsAtTheHelper(t *testing.T) {
	d, _ := brokerDaemon(t)
	if d.sessMgr.CredentialHelper != "/opt/marvel/credential-helper" {
		t.Fatalf("CredentialHelper = %q, want the path UseBrokerDriver was given", d.sessMgr.CredentialHelper)
	}
}

// A seat that asks in the window between its end being reported and its row
// leaving the store is still alive and still holds its heartbeat token. The
// broker must refuse it, or the token it is given outlives the seat.
func TestBrokerRefusesATokenAskMadeWhileTheSeatIsBeingDeleted(t *testing.T) {
	d, m := brokerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)

	revoke := d.sessMgr.SessionEnded
	var window Response
	d.sessMgr.SessionEnded = func(k string) {
		revoke(k)
		window = d.handleBrokerToken(context.Background(), brokerParams(t, k, token))
	}

	if err := d.sessMgr.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if window.Error == "" {
		t.Error("broker.token inside the delete window was answered, want a refusal")
	}
	if live := m.Live(); len(live) != 0 {
		t.Errorf("live tokens after the delete = %v, want none", live)
	}
}

// endingDriver reports the seat's end from inside Issue, as a delete that
// lands mid-ask would, and fails every Revoke with an error that echoes the
// bearer value, as an identity provider's error body can.
type endingDriver struct {
	broker.Driver
	onIssue func()

	mu    sync.Mutex
	value string
}

func (e *endingDriver) Issue(ctx context.Context, r broker.Request) (broker.Token, error) {
	tok, err := e.Driver.Issue(ctx, r)
	e.mu.Lock()
	e.value = tok.Value
	e.mu.Unlock()
	if e.onIssue != nil {
		e.onIssue()
	}
	return tok, err
}

func (e *endingDriver) Revoke(_ context.Context, t broker.Token) error {
	return fmt.Errorf("idp refused revoke of bearer %s", t.Value)
}

func (e *endingDriver) lastValue() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.value
}

// A refused ask whose token could not be revoked is the one case where the
// daemon's refusal hides a live credential, so the daemon must say so.
func TestBrokerTokenLogsATokenItCouldNotRevokeAfterTheSeatEnded(t *testing.T) {
	d := newHandlerDaemon(t)
	m := broker.NewMock(15*time.Minute, time.Now)
	ed := &endingDriver{Driver: m}
	d.UseBrokerDriver(ed, "/opt/marvel/credential-helper")
	key, token := registerSession(t, d, "agent-0", true)
	ed.onIssue = func() { d.sessMgr.SessionEnded(key) }
	logs := captureLog(t)

	resp := d.handleBrokerToken(context.Background(), brokerParams(t, key, token))
	if resp.Error == "" {
		t.Fatal("broker.token for a seat that ended mid-ask was answered, want a refusal")
	}
	live := m.Live()
	if len(live) != 1 {
		t.Fatalf("live tokens = %v, want the one that could not be revoked", live)
	}
	out := logs.String()
	if !strings.Contains(out, key) || !strings.Contains(out, "revoke") {
		t.Errorf("log = %q, want a warning naming the session and the failed revoke", out)
	}
	if v := ed.lastValue(); v == "" || strings.Contains(out, v) {
		t.Errorf("log carries the token value %q: %q", v, out)
	}
}

// The warning for a failed revoke at the end of a seat logs what the driver
// said, which can echo the bearer value.
func TestBrokerRevokeWarningCarriesNoTokenValue(t *testing.T) {
	d := newHandlerDaemon(t)
	ed := &endingDriver{Driver: broker.NewMock(15*time.Minute, time.Now)}
	d.UseBrokerDriver(ed, "/opt/marvel/credential-helper")
	key, token := registerSession(t, d, "agent-0", true)
	if resp := d.handleBrokerToken(context.Background(), brokerParams(t, key, token)); resp.Error != "" {
		t.Fatalf("broker.token: %s", resp.Error)
	}
	logs := captureLog(t)

	d.sessMgr.SessionEnded(key)

	out := logs.String()
	if !strings.Contains(out, "revoke") {
		t.Fatalf("log = %q, want a warning for the failed revoke", out)
	}
	if v := ed.lastValue(); v == "" || strings.Contains(out, v) {
		t.Errorf("log carries the token value %q: %q", v, out)
	}
}

// flakyRevokeDriver fails Revoke while down is set.
type flakyRevokeDriver struct {
	broker.Driver
	down atomic.Bool
}

func (f *flakyRevokeDriver) Revoke(ctx context.Context, t broker.Token) error {
	if f.down.Load() {
		return errors.New("idp down")
	}
	return f.Driver.Revoke(ctx, t)
}

// A seat deleted while the provider was down is not reported again, so the
// daemon retries its tokens on a tick, and leaves a live seat's tokens alone.
func TestBrokerRetryPassRevokesTokensOfSeatsThatAreGone(t *testing.T) {
	d := newHandlerDaemon(t)
	m := broker.NewMock(15*time.Minute, time.Now)
	fd := &flakyRevokeDriver{Driver: m}
	d.UseBrokerDriver(fd, "/opt/marvel/credential-helper")
	goneKey, goneToken := registerSession(t, d, "agent-0", true)
	hereKey, hereToken := registerSession(t, d, "agent-1", true)
	for _, c := range []struct{ key, token string }{{goneKey, goneToken}, {hereKey, hereToken}} {
		if resp := d.handleBrokerToken(context.Background(), brokerParams(t, c.key, c.token)); resp.Error != "" {
			t.Fatalf("broker.token: %s", resp.Error)
		}
	}

	fd.down.Store(true)
	if err := d.sessMgr.Delete(goneKey); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if live := m.Live(); len(live) != 2 {
		t.Fatalf("live tokens with the provider down = %v, want both", live)
	}

	d.retryBrokerRevokes(context.Background())
	if live := m.Live(); len(live) != 2 {
		t.Fatalf("live tokens after a pass with the provider still down = %v, want both", live)
	}

	fd.down.Store(false)
	d.retryBrokerRevokes(context.Background())
	if live := m.Live(); len(live) != 1 {
		t.Errorf("live tokens after the provider recovered = %v, want only the live seat's", live)
	}
}
